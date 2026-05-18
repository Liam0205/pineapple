// Package server provides a reusable HTTP server for the Pine execution engine.
//
// Third-party projects import this package and call [Run] from a thin
// main.go that also blank-imports their custom operator packages.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	pine "github.com/Liam0205/pineapple/pine-go"
	"github.com/Liam0205/pineapple/pine-go/pkg/metrics"
	"github.com/Liam0205/pineapple/pine-go/pkg/resource"
)

// errorResponse is a lightweight JSON error envelope used for non-200 responses.
type errorResponse struct {
	Error string `json:"error"`
}

// Config holds the server startup settings.
type Config struct {
	ConfigPath  string                           // Path to unified JSON config file (pipeline + resources)
	Addr        string                           // Listen address (e.g. ":8080")
	Resources   *resource.Manager                // Optional: pre-registered ResourceManager (caller registers, Run starts/stops)
	Metrics     metrics.Provider                 // Optional: metrics provider (nil → no-op)
	Middlewares []func(http.Handler) http.Handler // Optional: HTTP middlewares applied outer-to-inner

	// Timeouts for the HTTP server. Zero means no timeout.
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration

	// MaxRequestBodySize limits the size of request bodies in bytes.
	// Zero means use the default (10 MB).
	MaxRequestBodySize int64
}

// serverSnapshot bundles engine and resources into a single atomic unit
// so that requests always observe a consistent pair.
type serverSnapshot struct {
	engine    *pine.Engine
	resources *resource.Manager
}

// Server holds all instance-level mutable state for the Pine HTTP server.
type Server struct {
	snapshot             atomic.Pointer[serverSnapshot]
	maxRequestBodySize   int64
	reloadCount          atomic.Int64
	reloadErrorCount     atomic.Int64
	lastReloadDurationNs atomic.Int64
	metricsProvider      metrics.Provider
	srvMetrics           struct {
		reloadTotal    metrics.Counter
		reloadErrors   metrics.Counter
		reloadDuration metrics.Histogram
	}
}

// Run starts the Pine HTTP server with the given configuration.
// It loads the initial config, starts a config-reload watcher, registers
// HTTP handlers, and blocks until a SIGINT/SIGTERM is received.
func Run(cfg Config) error {
	s := &Server{}
	return s.run(cfg)
}

func (s *Server) run(cfg Config) error {
	if cfg.ConfigPath == "" {
		log.Fatal("usage: pineapple-server -config <path-to-config.json>")
	}
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}

	// Set effective max request body size.
	s.maxRequestBodySize = cfg.MaxRequestBodySize
	if s.maxRequestBodySize == 0 {
		s.maxRequestBodySize = 10 << 20 // 10 MB default
	}

	// Initialize metrics provider
	mp := cfg.Metrics
	if mp == nil {
		mp = metrics.Nop()
	}
	s.metricsProvider = mp
	s.srvMetrics.reloadTotal = mp.NewCounter(metrics.MetricOpts{
		Name: "pine_config_reload_total",
		Help: "Total successful config reloads.",
	})
	s.srvMetrics.reloadErrors = mp.NewCounter(metrics.MetricOpts{
		Name: "pine_config_reload_errors_total",
		Help: "Total failed config reloads.",
	})
	s.srvMetrics.reloadDuration = mp.NewHistogram(metrics.HistogramOpts{
		MetricOpts: metrics.MetricOpts{
			Name: "pine_config_reload_duration_seconds",
			Help: "Config reload duration in seconds.",
		},
		Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0},
	})

	// Load initial config
	configData, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		log.Fatalf("failed to read config: %v", err)
	}
	engine, err := pine.NewEngine(configData, pine.WithMetrics(mp))
	if err != nil {
		log.Fatalf("failed to load engine: %v", err)
	}
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

	s.snapshot.Store(&serverSnapshot{engine: engine, resources: rm})
	defer s.snapshot.Load().resources.Stop()

	// Validate resource dependencies against pipeline config.
	if err := resource.ValidateResourceDeps(configData, rm); err != nil {
		log.Fatalf("resource dependency check failed: %v", err)
	}

	// Start config reload watcher
	watchCtx, watchCancel := context.WithCancel(context.Background())
	defer watchCancel()
	go s.watchConfig(watchCtx, cfg.ConfigPath)

	// Set up routes
	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/execute", s.handleExecute)
	mux.HandleFunc("/stats", s.handleStats)
	mux.HandleFunc("/dag", s.handleDAG)

	// Apply HTTP metrics as innermost middleware (measures handler duration
	// excluding user middleware overhead).
	handler := httpMetricsMiddleware(mp, mux)

	// Apply user middlewares (outer-to-inner: first middleware sees request first)
	for i := len(cfg.Middlewares) - 1; i >= 0; i-- {
		handler = cfg.Middlewares[i](handler)
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: withDefault(cfg.ReadHeaderTimeout, 10*time.Second),
		ReadTimeout:       withDefault(cfg.ReadTimeout, 30*time.Second),
		WriteTimeout:      withDefault(cfg.WriteTimeout, 60*time.Second),
		IdleTimeout:       withDefault(cfg.IdleTimeout, 120*time.Second),
	}

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

func (s *Server) reloadConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	engine, err := pine.NewEngine(data, pine.WithMetrics(s.metricsProvider))
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

	old := s.snapshot.Swap(&serverSnapshot{engine: engine, resources: newRM})
	if old != nil {
		old.resources.Stop()
	}
	return nil
}

func (s *Server) watchConfig(ctx context.Context, path string) {
	// Initialize lastMod to current file mtime to avoid spurious reload on first tick.
	var lastMod time.Time
	if info, err := os.Stat(path); err == nil {
		lastMod = info.ModTime()
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.ModTime().After(lastMod) {
			lastMod = info.ModTime()
			start := time.Now()
			if err := s.reloadConfig(path); err != nil {
				s.reloadErrorCount.Add(1)
				s.srvMetrics.reloadErrors.Inc()
				log.Printf("config reload failed: %v", err)
			} else {
				dur := time.Since(start)
				s.reloadCount.Add(1)
				s.lastReloadDurationNs.Store(dur.Nanoseconds())
				s.srvMetrics.reloadTotal.Inc()
				s.srvMetrics.reloadDuration.Observe(metrics.DurationSeconds(dur))
				log.Printf("config reloaded from %s", path)
			}
		}
	}
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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

func (s *Server) handleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	snap := s.snapshot.Load()
	if snap == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "engine not loaded"})
		return
	}

	var req executeRequest
	r.Body = http.MaxBytesReader(w, r.Body, s.effectiveMaxRequestBodySize())
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request: " + err.Error()})
		return
	}

	pineReq := &pine.Request{
		Common: req.Common,
		Items:  req.Items,
	}

	// Inject resources into context so operators can access them.
	ctx := resource.WithResources(r.Context(), snap.resources)
	result, err := snap.engine.Execute(ctx, pineReq)

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
		if de, ok := err.(interface{ DetailedError() string }); ok {
			log.Printf("execute error: %s", de.DetailedError())
		}
		resp.Error = err.Error()
		var ve *pine.ValidationError
		if errors.As(err, &ve) {
			writeJSON(w, http.StatusBadRequest, resp)
		} else {
			writeJSON(w, http.StatusInternalServerError, resp)
		}
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	snap := s.snapshot.Load()
	if snap == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "engine not loaded"})
		return
	}

	resp := map[string]any{
		"operators": snap.engine.Stats(),
		"scheduler": snap.engine.SchedulerStats(),
		"server":    s.serverStats(),
	}
	if custom := snap.engine.OperatorCustomStats(); custom != nil {
		resp["operator_detail"] = custom
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) serverStats() map[string]int64 {
	return map[string]int64{
		"reload_count":            s.reloadCount.Load(),
		"reload_error_count":      s.reloadErrorCount.Load(),
		"last_reload_duration_ns": s.lastReloadDurationNs.Load(),
	}
}

func (s *Server) handleDAG(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	snap := s.snapshot.Load()
	if snap == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "engine not loaded"})
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "dot"
	}

	var opts []pine.RenderOption
	if collapseStr := r.URL.Query().Get("collapse"); collapseStr != "" {
		level, err := strconv.Atoi(collapseStr)
		if err != nil || level < 0 {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "collapse must be a non-negative integer"})
			return
		}
		if level > 0 {
			opts = append(opts, pine.WithCollapse(level))
		}
	}

	output, err := snap.engine.RenderDAG(format, opts...)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
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

// withDefault returns v if non-zero, otherwise def.
func withDefault(v, def time.Duration) time.Duration {
	if v == 0 {
		return def
	}
	return v
}

// effectiveMaxRequestBodySize returns the configured max body size, or 10MB if not set.
func (s *Server) effectiveMaxRequestBodySize() int64 {
	if s.maxRequestBodySize == 0 {
		return 10 << 20
	}
	return s.maxRequestBodySize
}
