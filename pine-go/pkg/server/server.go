// Package server provides a reusable HTTP server for the Pine execution engine.
//
// Third-party projects import this package and call [Run] from a thin
// main.go that also blank-imports their custom operator packages.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/pprof"
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

// ErrEngineNotLoaded is returned by Execute when no engine snapshot is live.
var ErrEngineNotLoaded = errors.New("engine not loaded")

// defaultKnownPaths is the set of built-in endpoints. It seeds the dynamic
// known-path set used for HTTP metrics normalization and for detecting custom
// route conflicts. Custom Config.Routes are layered on top of these.
var defaultKnownPaths = map[string]bool{
	"/execute": true,
	"/health":  true,
	"/stats":   true,
	"/dag":     true,
}

// errorResponse is a lightweight JSON error envelope used for non-200 responses.
type errorResponse struct {
	Error string `json:"error"`
}

// Ingress converts an incoming HTTP request into a pine.Request. Returning
// an error aborts execution; the Server responds via the route's Egress
// with a nil result and that error.
type Ingress func(r *http.Request) (*pine.Request, error)

// Egress writes the pipeline outcome to the response. It receives the
// engine result (nil on ingress/execute error) and any error, and owns
// the entire HTTP response.
type Egress func(w http.ResponseWriter, r *http.Request, result *pine.Result, err error)

// Route declares a custom endpoint layered on top of the Pine engine.
type Route struct {
	Method  string // e.g. http.MethodPost; empty means any method
	Path    string // exact path, e.g. "/api/v1/report"
	Ingress Ingress
	Egress  Egress
}

// Config holds the server startup settings.
type Config struct {
	ConfigPath  string                            // Path to unified JSON config file (pipeline + resources)
	Addr        string                            // Listen address (e.g. ":8080")
	AdminAddr   string                            // Optional: admin listen address for pprof (e.g. ":6060"); empty disables
	Resources   *resource.Manager                 // Optional: pre-registered ResourceManager (caller registers, Run starts/stops)
	Metrics     metrics.Provider                  // Optional: metrics provider (nil → no-op)
	Middlewares []func(http.Handler) http.Handler // Optional: HTTP middlewares applied outer-to-inner

	// Routes are custom ingress/egress endpoints registered alongside the
	// built-in /execute, /health, /stats and /dag endpoints. Each route's
	// Ingress converts an HTTP request into a pine.Request; the server
	// executes it against the live snapshot; Egress writes the response.
	Routes []Route

	// Watch controls config hot-reload. nil (default) enables the watcher
	// for backward compatibility; non-nil false disables it (config changes
	// require a process restart). Use pine.Bool(true/false) as a helper.
	Watch *bool

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
//
// It carries a reference count so that engine/resource teardown on retirement
// (config hot-reload or shutdown) is deferred until all in-flight requests
// that captured this snapshot have finished — guaranteeing no request ever
// uses an operator resource that has already been closed. The count starts at
// 1 (the "live" baseline reference held by s.snapshot); retiring the snapshot
// drops that baseline, and the last reference to reach zero runs teardown.
type serverSnapshot struct {
	engine    *pine.Engine
	resources *resource.Manager
	// resourceMetrics aggregates resource-level Provider metrics for /stats. It
	// is the provider handed to this snapshot's ResourceManager, so it is
	// recreated on every hot-reload alongside the manager. May be nil when the
	// caller supplied its own pre-built manager.
	resourceMetrics *metrics.Collector
	refs            atomic.Int64
}

// newSnapshot builds a live snapshot with the baseline reference held.
func newSnapshot(engine *pine.Engine, rm *resource.Manager, rmMetrics *metrics.Collector) *serverSnapshot {
	s := &serverSnapshot{engine: engine, resources: rm, resourceMetrics: rmMetrics}
	s.refs.Store(1)
	return s
}

// acquire takes an in-flight reference, returning false if the snapshot has
// already been retired (count reached its baseline drop). The CAS only ever
// increments a positive count, so once teardown is committed (count 0) no new
// reference can revive it. A request that observes false must fall back to the
// current live snapshot.
func (s *serverSnapshot) acquire() bool {
	for {
		n := s.refs.Load()
		if n <= 0 {
			return false
		}
		if s.refs.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

// release drops one reference. When the count reaches zero (baseline dropped
// and all in-flight references released) it runs teardown exactly once.
func (s *serverSnapshot) release() {
	if s.refs.Add(-1) == 0 {
		s.teardown()
	}
}

// teardown stops the resource manager and closes the engine. Called once, by
// whichever goroutine drops the final reference.
func (s *serverSnapshot) teardown() {
	s.resources.Stop()
	if err := s.engine.Close(); err != nil {
		log.Printf("[retire] engine close: %v", err)
	}
}

// acquireSnapshot returns the current live snapshot with an in-flight reference
// held; the caller must release() it when done. It retries if the snapshot is
// retired between Load and acquire, since a retirement always installs a new
// live snapshot first. Returns nil only before the initial snapshot is stored.
func (s *Server) acquireSnapshot() *serverSnapshot {
	for {
		snap := s.snapshot.Load()
		if snap == nil {
			return nil
		}
		if snap.acquire() {
			return snap
		}
	}
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
	httpStats *HttpStats

	// watchCancel stops the config-reload watcher goroutine; watchDone is
	// closed once that goroutine has returned. Both are nil when the watcher
	// is disabled (Config.Watch == false).
	watchCancel context.CancelFunc
	watchDone   chan struct{}
}

// Run starts the Pine HTTP server with the given configuration.
// It builds the Server (via NewServer), registers HTTP handlers (built-in
// endpoints plus any custom Config.Routes), and blocks until a SIGINT/SIGTERM
// is received. The config-reload watcher and graceful shutdown are wired up
// for the built-in net/http server; embedders that only need the engine can
// use NewServer + Execute/Acquire instead.
func Run(cfg Config) error {
	cfg = normalizeConfig(cfg)

	s, err := NewServer(cfg)
	if err != nil {
		return err
	}
	defer s.Close()

	// Validate custom routes up front. known starts from the built-in
	// endpoints plus every custom route path, so it doubles as the
	// low-cardinality path set handed to the HTTP metrics middleware.
	known, err := validateRoutes(cfg.Routes)
	if err != nil {
		return err
	}

	// Set up routes: built-in endpoints on the mux, custom routes behind the
	// "/" fallback. Custom paths are dispatched by exact string lookup — NOT
	// registered as ServeMux patterns — so a trailing slash never becomes a
	// subtree wildcard and "{}" segments are never interpreted, matching the
	// exact-path semantics of pine-java (path guard) and pine-cpp (map lookup).
	customRoutes := make(map[string]http.Handler, len(cfg.Routes))
	for _, route := range cfg.Routes {
		customRoutes[route.Path] = s.routeHandler(route)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/execute", s.handleExecute)
	mux.HandleFunc("/stats", s.handleStats)
	mux.HandleFunc("/dag", s.handleDAG)
	mux.Handle("/", fallbackHandler(customRoutes))

	// Apply HTTP metrics as innermost middleware (measures handler duration
	// excluding user middleware overhead). Pass the dynamic known-path set so
	// custom routes are reported under their own path, not "_other".
	s.httpStats = NewHttpStats()
	handler := httpMetricsMiddleware(s.metricsProvider, s.httpStats, known, mux)

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

	// Start admin server for pprof on a separate port (opt-in).
	// No WriteTimeout so long-running profiles (e.g. ?seconds=120) are not truncated.
	var adminSrv *http.Server
	if cfg.AdminAddr != "" {
		adminSrv = &http.Server{
			Addr:              cfg.AdminAddr,
			Handler:           newAdminMux(),
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			log.Printf("admin (pprof) listening on %s", cfg.AdminAddr)
			if err := adminSrv.ListenAndServe(); err != http.ErrServerClosed {
				log.Printf("admin server error: %v", err)
			}
		}()
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if adminSrv != nil {
			_ = adminSrv.Shutdown(ctx)
		}
		_ = srv.Shutdown(ctx)
	}()

	log.Printf("listening on %s", cfg.Addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// normalizeConfig applies documented defaults to zero-value fields. Both Run
// (which reads cfg.Addr when building the http.Server) and NewServer call it,
// so an empty Addr always means ":8080" — never net/http's ":http" (port 80).
func normalizeConfig(cfg Config) Config {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	return cfg
}

// fallbackHandler dispatches the ServeMux "/" catch-all: exact string lookup
// into the custom-route table, else the standard 404. Keeping custom routes
// out of ServeMux pattern registration preserves their documented exact-path
// semantics (no trailing-slash subtrees, no "{}" wildcards, no pattern panics).
func fallbackHandler(customRoutes map[string]http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, ok := customRoutes[r.URL.Path]; ok {
			h.ServeHTTP(w, r)
			return
		}
		handleNotFound(w, r)
	})
}

// NewServer builds a Server from cfg: it loads the config, creates the engine,
// creates and starts the ResourceManager, stores the live snapshot, and (unless
// Config.Watch is explicitly false) starts the config-reload watcher.
//
// It is the shared foundation of the built-in Run() server and of embedding
// pine-go into an existing HTTP framework (see Execute / Acquire). The caller
// owns the returned Server and must call Close when done to stop the watcher
// and release the engine/resource baseline.
func NewServer(cfg Config) (*Server, error) {
	if cfg.ConfigPath == "" {
		return nil, errors.New("server: Config.ConfigPath must not be empty")
	}
	cfg = normalizeConfig(cfg)

	s := &Server{}

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
		return nil, fmt.Errorf("failed to read config: %w", err)
	}
	engine, err := pine.NewEngine(configData, pine.WithMetrics(mp))
	if err != nil {
		return nil, fmt.Errorf("failed to load engine: %w", err)
	}
	log.Printf("engine loaded from %s", cfg.ConfigPath)

	// Every failure path between here and publishing the snapshot must tear
	// down what has been built so far: NewServer returns errors to a long-lived
	// host (not log.Fatal), so a leaked Engine or a started ResourceManager
	// would accumulate across caller retries. Ownership of cfg.Resources also
	// transfers to the server (a hot-reload replaces and stops it), so it is
	// covered by the same rollback.
	published := false
	var rm *resource.Manager
	defer func() {
		if published {
			return
		}
		if rm != nil {
			rm.Stop()
		}
		if cerr := engine.Close(); cerr != nil {
			log.Printf("[NewServer] engine close during rollback: %v", cerr)
		}
	}()

	// Initialize ResourceManager.
	// If the caller supplied a pre-registered manager, use it;
	// otherwise create an empty one whose resource metrics flow into a
	// dedicated Collector serialized under /stats.resources (keeping engine
	// metrics out of that subtree).
	var rmMetrics *metrics.Collector
	if cfg.Resources != nil {
		rm = cfg.Resources
	} else {
		rmMetrics = metrics.NewCollector()
		// Fan out resource metrics to both the caller-injected provider (e.g.
		// Prometheus) and the dedicated collector for /stats.resources. Only the
		// ResourceManager writes through this tee, so the collector stays scoped
		// to resource metrics; engine metrics use mp directly.
		rm = resource.NewManager(metrics.Tee(mp, rmMetrics))
	}

	// Load resource config from unified JSON.
	if err := rm.LoadFromRootConfig(configData); err != nil {
		return nil, fmt.Errorf("failed to load resource config: %w", err)
	}

	if err := rm.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to start resource manager: %w", err)
	}

	// Validate resource dependencies against pipeline config.
	if err := resource.ValidateResourceDeps(configData, rm); err != nil {
		return nil, fmt.Errorf("resource dependency check failed: %w", err)
	}

	s.snapshot.Store(newSnapshot(engine, rm, rmMetrics))
	published = true

	// Start config reload watcher unless explicitly disabled. Watch defaults to
	// enabled (nil or *true) for backward compatibility; *false disables it so
	// config changes require a process restart.
	if cfg.Watch == nil || *cfg.Watch {
		watchCtx, watchCancel := context.WithCancel(context.Background())
		s.watchCancel = watchCancel
		s.watchDone = make(chan struct{})
		go func() {
			defer close(s.watchDone)
			s.watchConfig(watchCtx, cfg.ConfigPath)
		}()
	}

	return s, nil
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

	rmMetrics := metrics.NewCollector()
	newRM := resource.NewManager(metrics.Tee(s.metricsProvider, rmMetrics))
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

	old := s.snapshot.Swap(newSnapshot(engine, newRM, rmMetrics))
	if old != nil {
		// Drop the baseline reference. Teardown (resource Stop + engine Close)
		// runs once the last in-flight request that captured the old snapshot
		// releases its reference, so no request is ever served with a
		// closed operator resource.
		old.release()
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

func handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
}

// Execute runs the pipeline against the live snapshot. For embedding pine-go
// into existing HTTP frameworks (e.g. Gin) without using the built-in
// net/http server. It acquires an in-flight reference on the live snapshot so
// a concurrent hot-reload never tears down the engine/resources mid-execution,
// injects the snapshot's resources into ctx, and returns ErrEngineNotLoaded
// when no snapshot is live.
func (s *Server) Execute(ctx context.Context, req *pine.Request) (*pine.Result, error) {
	snap := s.acquireSnapshot()
	if snap == nil {
		return nil, ErrEngineNotLoaded
	}
	defer snap.release()
	ctx = resource.WithResources(ctx, snap.resources)
	return snap.engine.Execute(ctx, req)
}

// Handle is an acquired reference to the live snapshot. It keeps the underlying
// engine and resources alive (deferring hot-reload teardown) until Release is
// called, so callers embedding pine-go can drive multiple Execute-equivalent
// operations against a stable engine/resource pair. Always call Release.
type Handle struct{ snap *serverSnapshot }

// Release drops the in-flight reference held by this Handle. After Release the
// Handle must not be used again.
func (h *Handle) Release() { h.snap.release() }

// Engine returns the engine bound to this snapshot.
func (h *Handle) Engine() *pine.Engine { return h.snap.engine }

// Resources returns the ResourceManager bound to this snapshot.
func (h *Handle) Resources() *resource.Manager { return h.snap.resources }

// ResourceMetrics returns the resource-level metrics Collector for this
// snapshot, or nil when the caller supplied a pre-built ResourceManager.
func (h *Handle) ResourceMetrics() *metrics.Collector { return h.snap.resourceMetrics }

// Acquire returns a Handle to the live snapshot with an in-flight reference
// held, or nil if no snapshot is live. The caller must call Release on the
// returned Handle when done.
func (s *Server) Acquire() *Handle {
	snap := s.acquireSnapshot()
	if snap == nil {
		return nil
	}
	return &Handle{snap: snap}
}

// Close stops the config watcher and drops the baseline snapshot reference.
// Teardown (resource Stop + engine Close) runs once the last in-flight
// reference is released. Close is safe to call once; the built-in Run() calls
// it via defer, and embedders must call it when finished with the Server.
func (s *Server) Close() {
	if s.watchCancel != nil {
		s.watchCancel()
		<-s.watchDone
		s.watchCancel = nil
		s.watchDone = nil
	}
	if old := s.snapshot.Swap(nil); old != nil {
		old.release()
	}
}

// validateRoutes checks custom routes for conflicts with built-in endpoints,
// duplicates, malformed paths, and missing Ingress/Egress. On success it
// returns the full known-path set (built-ins + custom paths) used for HTTP
// metrics normalization.
func validateRoutes(routes []Route) (map[string]bool, error) {
	known := make(map[string]bool, len(defaultKnownPaths)+len(routes))
	for p := range defaultKnownPaths {
		known[p] = true
	}
	for _, route := range routes {
		if route.Path == "" || route.Path[0] != '/' {
			return nil, fmt.Errorf("custom route path %q must start with '/'", route.Path)
		}
		if route.Path == "/" {
			return nil, errors.New(`custom route path "/" conflicts with the built-in not-found handler`)
		}
		if defaultKnownPaths[route.Path] {
			return nil, fmt.Errorf("custom route %q conflicts with built-in endpoint", route.Path)
		}
		if known[route.Path] {
			return nil, fmt.Errorf("duplicate custom route %q", route.Path)
		}
		if route.Ingress == nil {
			return nil, fmt.Errorf("custom route %q has nil Ingress", route.Path)
		}
		if route.Egress == nil {
			return nil, fmt.Errorf("custom route %q has nil Egress", route.Path)
		}
		known[route.Path] = true
	}
	return known, nil
}

// routeHandler wraps a custom Route into an http.Handler: it enforces the
// route's method (when set), caps the request body at the server-wide limit,
// runs Ingress to build the request, executes it against the live snapshot,
// and hands the result (or error) to Egress, which owns the response.
func (s *Server) routeHandler(route Route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if route.Method != "" && r.Method != route.Method {
			writeJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
			return
		}
		// Apply MaxRequestBodySize before user Ingress code can read the body,
		// so custom endpoints cannot be used to bypass the server-wide limit.
		// When the Ingress read trips the cap, respond 413 centrally (same
		// bytes as the built-in /execute limit) instead of calling Egress.
		r.Body = http.MaxBytesReader(w, r.Body, s.effectiveMaxRequestBodySize())
		req, err := route.Ingress(r)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
				return
			}
			route.Egress(w, r, nil, err)
			return
		}
		result, err := s.Execute(r.Context(), req)
		route.Egress(w, r, result, err)
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
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

	snap := s.acquireSnapshot()
	if snap == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "engine not loaded"})
		return
	}
	defer snap.release()

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

	snap := s.acquireSnapshot()
	if snap == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "engine not loaded"})
		return
	}
	defer snap.release()

	resp := map[string]any{
		"operators": snap.engine.Stats(),
		"scheduler": snap.engine.SchedulerStats(),
		"server":    s.serverStats(),
	}
	if s.httpStats != nil {
		resp["http"] = s.httpStats.Snapshot()
	}
	if snap.resourceMetrics != nil {
		resp["resources"] = snap.resourceMetrics.Snapshot()
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

	snap := s.acquireSnapshot()
	if snap == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "engine not loaded"})
		return
	}
	defer snap.release()

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

// newAdminMux builds the mux mounted on AdminAddr. It exposes the standard
// net/http/pprof handlers under /debug/pprof/. Kept as a package-private
// helper so tests can assert that pprof is reachable on the admin port and
// — crucially — *not* reachable on the main mux.
func newAdminMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}
