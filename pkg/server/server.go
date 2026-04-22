// Package server provides a reusable HTTP server for the Pine execution engine.
//
// Third-party projects import this package and call [Run] from a thin
// main.go that also blank-imports their custom operator packages.
package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	pine "github.com/Liam0205/pineapple"
	"github.com/Liam0205/pineapple/pkg/resource"
)

// Config holds the server startup settings.
type Config struct {
	ConfigPath string            // Path to unified JSON config file (pipeline + resources)
	Addr       string            // Listen address (e.g. ":8080")
	Resources  *resource.Manager // Optional: pre-registered ResourceManager (caller registers, Run starts/stops)
}

var (
	enginePtr atomic.Pointer[pine.Engine]
	resources atomic.Pointer[resource.Manager]
)

// Run starts the Pine HTTP server with the given configuration.
// It loads the initial config, starts a config-reload watcher, registers
// HTTP handlers, and blocks until a SIGINT/SIGTERM is received.
func Run(cfg Config) error {
	if cfg.ConfigPath == "" {
		log.Fatal("usage: pineapple-server -config <path-to-config.json>")
	}
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}

	// Load initial config
	configData, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		log.Fatalf("failed to read config: %v", err)
	}
	engine, err := pine.NewEngine(configData)
	if err != nil {
		log.Fatalf("failed to load engine: %v", err)
	}
	enginePtr.Store(engine)
	log.Printf("engine loaded from %s", cfg.ConfigPath)

	// Initialize ResourceManager.
	// If the caller supplied a pre-registered manager, use it;
	// otherwise create an empty one.
	var rm *resource.Manager
	if cfg.Resources != nil {
		rm = cfg.Resources
	} else {
		rm = resource.NewManager()
	}

	// Load resource config from unified JSON.
	if err := rm.LoadFromRootConfig(configData); err != nil {
		log.Fatalf("failed to load resource config: %v", err)
	}

	if err := rm.Start(context.Background()); err != nil {
		log.Fatalf("failed to start resource manager: %v", err)
	}
	resources.Store(rm)
	defer resources.Load().Stop()

	// Validate resource dependencies against pipeline config.
	if err := resource.ValidateResourceDeps(configData, rm); err != nil {
		log.Fatalf("resource dependency check failed: %v", err)
	}

	// Start config reload watcher
	go watchConfig(cfg.ConfigPath)

	// Set up routes
	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/execute", handleExecute)
	mux.HandleFunc("/stats", handleStats)
	mux.HandleFunc("/dag", handleDAG)

	srv := &http.Server{Addr: cfg.Addr, Handler: mux}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	log.Printf("listening on %s", cfg.Addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func reloadConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	engine, err := pine.NewEngine(data)
	if err != nil {
		return err
	}

	newRM := resource.NewManager()
	if err := newRM.LoadFromRootConfig(data); err != nil {
		return err
	}
	if err := newRM.Start(context.Background()); err != nil {
		return err
	}

	if err := resource.ValidateResourceDeps(data, newRM); err != nil {
		newRM.Stop()
		return err
	}

	enginePtr.Store(engine)
	oldRM := resources.Swap(newRM)
	if oldRM != nil {
		oldRM.Stop()
	}
	return nil
}

func watchConfig(path string) {
	var lastMod time.Time
	for {
		time.Sleep(2 * time.Second)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.ModTime().After(lastMod) {
			lastMod = info.ModTime()
			if err := reloadConfig(path); err != nil {
				log.Printf("config reload failed: %v", err)
			} else {
				log.Printf("config reloaded from %s", path)
			}
		}
	}
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

type executeRequest struct {
	Common map[string]any   `json:"common"`
	Items  []map[string]any `json:"items,omitempty"`
}

type executeResponse struct {
	Common   map[string]any   `json:"common"`
	Items    []map[string]any `json:"items"`
	Warnings []string         `json:"warnings,omitempty"`
	Trace    []traceEntry     `json:"trace,omitempty"`
	Error    string           `json:"error,omitempty"`
}

type traceEntry struct {
	Name           string         `json:"name"`
	DurationMs     float64        `json:"duration_ms"`
	Skipped        bool           `json:"skipped,omitempty"`
	InputSnapshot  map[string]any `json:"input_snapshot,omitempty"`
	OutputSnapshot map[string]any `json:"output_snapshot,omitempty"`
}

func handleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	engine := enginePtr.Load()
	if engine == nil {
		http.Error(w, "engine not loaded", http.StatusServiceUnavailable)
		return
	}

	var req executeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, executeResponse{Error: "invalid request: " + err.Error()})
		return
	}

	pineReq := &pine.Request{
		Common: req.Common,
		Items:  req.Items,
	}

	// Inject resources into context so operators can access them.
	ctx := resource.WithResources(r.Context(), resources.Load())
	result, err := engine.Execute(ctx, pineReq)

	resp := executeResponse{}
	if result != nil {
		resp.Common = result.Common
		resp.Items = result.Items
		for _, warn := range result.Warnings {
			resp.Warnings = append(resp.Warnings, warn.Error())
		}
		if returnTrace, _ := req.Common["_return_trace"].(bool); returnTrace {
			for _, t := range result.Trace {
				resp.Trace = append(resp.Trace, traceEntry{
					Name:           t.Name,
					DurationMs:     float64(t.Duration.Microseconds()) / 1000.0,
					Skipped:        t.Skipped,
					InputSnapshot:  t.InputSnapshot,
					OutputSnapshot: t.OutputSnapshot,
				})
			}
		}
	}
	if err != nil {
		resp.Error = err.Error()
		writeJSON(w, http.StatusInternalServerError, resp)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	engine := enginePtr.Load()
	if engine == nil {
		http.Error(w, "engine not loaded", http.StatusServiceUnavailable)
		return
	}

	writeJSON(w, http.StatusOK, engine.Stats())
}

func handleDAG(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	engine := enginePtr.Load()
	if engine == nil {
		http.Error(w, "engine not loaded", http.StatusServiceUnavailable)
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "dot"
	}

	output, err := engine.RenderDAG(format)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch format {
	case "dot":
		w.Header().Set("Content-Type", "text/vnd.graphviz; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	_, _ = w.Write([]byte(output))
}
