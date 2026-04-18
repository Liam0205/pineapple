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
	ConfigPath string // Path to pipeline JSON config file
	Addr       string // Listen address (e.g. ":8080")
}

var (
	enginePtr atomic.Pointer[pine.Engine]
	resources *resource.Manager
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
	if err := loadEngine(cfg.ConfigPath); err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	log.Printf("engine loaded from %s", cfg.ConfigPath)

	// Initialize ResourceManager.
	// Register resources here. Example:
	//   resources.Register("feature_index", fetchFeatureIndex, 5*time.Minute)
	resources = resource.NewManager()
	if err := resources.Start(context.Background()); err != nil {
		log.Fatalf("failed to start resource manager: %v", err)
	}
	defer resources.Stop()

	// Start config reload watcher
	go watchConfig(cfg.ConfigPath)

	// Set up routes
	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/execute", handleExecute)
	mux.HandleFunc("/stats", handleStats)

	srv := &http.Server{Addr: cfg.Addr, Handler: mux}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	log.Printf("listening on %s", cfg.Addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func loadEngine(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	engine, err := pine.NewEngine(data)
	if err != nil {
		return err
	}
	enginePtr.Store(engine)
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
			if err := loadEngine(path); err != nil {
				log.Printf("config reload failed: %v", err)
			} else {
				log.Printf("config reloaded from %s", path)
			}
		}
	}
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
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
	ctx := resource.WithResources(r.Context(), resources)
	result, err := engine.Execute(ctx, pineReq)

	resp := executeResponse{}
	if result != nil {
		resp.Common = result.Common
		resp.Items = result.Items
		for _, warn := range result.Warnings {
			resp.Warnings = append(resp.Warnings, warn.Error())
		}
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
	json.NewEncoder(w).Encode(v)
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
