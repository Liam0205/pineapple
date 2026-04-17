package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	pine "github.com/Liam0205/pineapple"
	_ "github.com/Liam0205/pineapple/operators"
)

var enginePtr atomic.Pointer[pine.Engine]

func main() {
	configPath := flag.String("config", "", "Path to pipeline JSON config file")
	addr := flag.String("addr", ":8080", "Listen address")
	flag.Parse()

	if *configPath == "" {
		log.Fatal("usage: pineapple-server -config <path-to-config.json>")
	}

	// Load initial config
	if err := loadEngine(*configPath); err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	log.Printf("engine loaded from %s", *configPath)

	// Start config reload watcher
	go watchConfig(*configPath)

	// Set up routes
	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/execute", handleExecute)
	mux.HandleFunc("/stats", handleStats)

	srv := &http.Server{Addr: *addr, Handler: mux}

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

	log.Printf("listening on %s", *addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
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

	result, err := engine.Execute(r.Context(), pineReq)

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
