package transform

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Liam0205/pineapple/pine-go/pkg/metrics"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func redisClientFor(addr string) *redis.Client {
	return redis.NewClient(&redis.Options{Addr: addr})
}

// metricRecorder records the names of all metrics created through it, so tests
// can assert whether the redis_connection resource emitted its metrics.
type metricRecorder struct {
	mu    sync.Mutex
	names []string
}

func (r *metricRecorder) record(name string) {
	r.mu.Lock()
	r.names = append(r.names, name)
	r.mu.Unlock()
}

func (r *metricRecorder) has(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range r.names {
		if n == name {
			return true
		}
	}
	return false
}

func (r *metricRecorder) NewCounter(opts metrics.MetricOpts) metrics.Counter {
	r.record(opts.Name)
	return metrics.Nop().NewCounter(opts)
}

func (r *metricRecorder) NewGauge(opts metrics.MetricOpts) metrics.Gauge {
	r.record(opts.Name)
	return metrics.Nop().NewGauge(opts)
}

func (r *metricRecorder) NewHistogram(opts metrics.HistogramOpts) metrics.Histogram {
	r.record(opts.Name)
	return metrics.Nop().NewHistogram(opts)
}

var redisResourceMetrics = []string{
	"pine_redis_pool_total_conns",
	"pine_redis_pool_idle_conns",
	"pine_redis_ping_duration_seconds",
	"pine_redis_up",
}

// recordingProvider extends metricRecorder with per-call label tuple capture.
// The base recorder only knows whether a metric was created; the command-
// hook tests need to verify the (name, command, status) tuples each Inc/
// Observe was actually bound under, which the Counter/Histogram contract
// surfaces only via the With(...) chain. We snapshot the labels at Inc/
// Observe time so the test can assert against the rendered tuple instead
// of reasoning about the metric's internal state.
type recordingProvider struct {
	metricRecorder
	mu              sync.Mutex
	counterLabels   map[string]map[string]int // metric name -> joinedLabels -> calls
	histogramLabels map[string]map[string]int // metric name -> joinedLabels -> samples
	counterCreate   map[string]bool
	histogramCreate map[string]bool
}

func (r *recordingProvider) NewCounter(opts metrics.MetricOpts) metrics.Counter {
	r.mu.Lock()
	if r.counterCreate == nil {
		r.counterCreate = make(map[string]bool)
	}
	r.counterCreate[opts.Name] = true
	r.mu.Unlock()
	return &recordingCounter{r: r, name: opts.Name}
}

func (r *recordingProvider) NewGauge(opts metrics.MetricOpts) metrics.Gauge {
	r.metricRecorder.record(opts.Name)
	return metrics.Nop().NewGauge(opts)
}

func (r *recordingProvider) NewHistogram(opts metrics.HistogramOpts) metrics.Histogram {
	r.mu.Lock()
	if r.histogramCreate == nil {
		r.histogramCreate = make(map[string]bool)
	}
	r.histogramCreate[opts.Name] = true
	r.mu.Unlock()
	return &recordingHistogram{r: r, name: opts.Name}
}

func (r *recordingProvider) noteCounter(name, key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.counterLabels == nil {
		r.counterLabels = make(map[string]map[string]int)
	}
	if r.counterLabels[name] == nil {
		r.counterLabels[name] = make(map[string]int)
	}
	r.counterLabels[name][key]++
}

func (r *recordingProvider) noteHistogram(name, key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.histogramLabels == nil {
		r.histogramLabels = make(map[string]map[string]int)
	}
	if r.histogramLabels[name] == nil {
		r.histogramLabels[name] = make(map[string]int)
	}
	r.histogramLabels[name][key]++
}

func (r *recordingProvider) counterCreated(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counterCreate[name]
}

func (r *recordingProvider) histogramCreated(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.histogramCreate[name]
}

func (r *recordingProvider) counterObserved(name string, labels ...string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counterLabels[name][joinLabels(labels)] > 0
}

func (r *recordingProvider) histogramObserved(name string, labels ...string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.histogramLabels[name][joinLabels(labels)] > 0
}

func joinLabels(labels []string) string {
	out := ""
	for i, v := range labels {
		if i > 0 {
			out += "|"
		}
		out += v
	}
	return out
}

type recordingCounter struct {
	r      *recordingProvider
	name   string
	labels []string
}

func (c *recordingCounter) With(values ...string) metrics.Counter {
	combined := append(append([]string{}, c.labels...), values...)
	return &recordingCounter{r: c.r, name: c.name, labels: combined}
}

func (c *recordingCounter) Inc() {
	c.r.noteCounter(c.name, joinLabels(c.labels))
}

type recordingHistogram struct {
	r      *recordingProvider
	name   string
	labels []string
}

func (h *recordingHistogram) With(values ...string) metrics.Histogram {
	combined := append(append([]string{}, h.labels...), values...)
	return &recordingHistogram{r: h.r, name: h.name, labels: combined}
}

func (h *recordingHistogram) Observe(_ float64) {
	h.r.noteHistogram(h.name, joinLabels(h.labels))
}

func TestRedisConnResource_MetricsGate_Disabled(t *testing.T) {
	s := miniredis.RunT(t)
	rec := &metricRecorder{}
	// Empty metrics_name → no metrics, no probe goroutine.
	r := newRedisConnResource(redisClientFor(s.Addr()), "", rec)
	defer func() { _ = r.Close() }()

	for _, name := range redisResourceMetrics {
		if rec.has(name) {
			t.Errorf("metric %q created when metrics_name is empty", name)
		}
	}
}

func TestRedisConnResource_MetricsGate_Enabled(t *testing.T) {
	s := miniredis.RunT(t)
	rec := &metricRecorder{}
	r := newRedisConnResource(redisClientFor(s.Addr()), "cache", rec)
	defer func() { _ = r.Close() }()

	for _, name := range redisResourceMetrics {
		if !rec.has(name) {
			t.Errorf("metric %q not created when metrics_name is set", name)
		}
	}
}

func TestRedisConnResource_Close_Idempotent(t *testing.T) {
	s := miniredis.RunT(t)
	r := newRedisConnResource(redisClientFor(s.Addr()), "cache", &metricRecorder{})
	if err := r.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestRedisConnResource_ProbeStopsOnClose(t *testing.T) {
	s := miniredis.RunT(t)
	r := newRedisConnResource(redisClientFor(s.Addr()), "cache", &metricRecorder{})
	// Close must join the probe goroutine without hanging.
	done := make(chan struct{})
	go func() {
		_ = r.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return; probe goroutine not joined")
	}
}

// TestRedisOptionsFromParams_Defaults verifies the cascade-safety timeouts
// land on the *redis.Options when the user does not configure them. This is
// the regression test for the 2026-06-22 tipsy-recsys incident, where the
// resource was inheriting go-redis v9's defaults (read/write 3s) and a brief
// Redis hiccup escalated into a 30-minute /execute outage.
func TestRedisOptionsFromParams_Defaults(t *testing.T) {
	opts, metricsName, err := redisOptionsFromParams(map[string]any{
		"addr": "127.0.0.1:6379",
	})
	if err != nil {
		t.Fatalf("redisOptionsFromParams: %v", err)
	}
	if opts.Addr != "127.0.0.1:6379" {
		t.Errorf("Addr=%q, want 127.0.0.1:6379", opts.Addr)
	}
	if opts.DialTimeout != 2*time.Second {
		t.Errorf("DialTimeout=%v, want 2s", opts.DialTimeout)
	}
	if opts.ReadTimeout != 2*time.Second {
		t.Errorf("ReadTimeout=%v, want 2s", opts.ReadTimeout)
	}
	if opts.WriteTimeout != 2*time.Second {
		t.Errorf("WriteTimeout=%v, want 2s", opts.WriteTimeout)
	}
	if opts.PoolTimeout != 2*time.Second {
		t.Errorf("PoolTimeout=%v, want 2s", opts.PoolTimeout)
	}
	// pool_size=0 routes through to the client default (10*GOMAXPROCS) — we
	// preserve that historical value for deployments that have not raised it.
	if opts.PoolSize != 0 {
		t.Errorf("PoolSize=%d, want 0 (client default)", opts.PoolSize)
	}
	if metricsName != "" {
		t.Errorf("metricsName=%q, want \"\"", metricsName)
	}
}

// TestRedisOptionsFromParams_Explicit verifies user-supplied timeouts are
// passed through verbatim. Production deployments tighten these (e.g.
// 500ms read/write) once they have measured their server's healthy P99.
func TestRedisOptionsFromParams_Explicit(t *testing.T) {
	opts, metricsName, err := redisOptionsFromParams(map[string]any{
		"addr":             "127.0.0.1:6379",
		"password":         "secret",
		"db":               int64(3),
		"dial_timeout_ms":  int64(800),
		"read_timeout_ms":  int64(500),
		"write_timeout_ms": int64(500),
		"pool_timeout_ms":  int64(1000),
		"pool_size":        int64(50),
		"metrics_name":     "cache",
	})
	if err != nil {
		t.Fatalf("redisOptionsFromParams: %v", err)
	}
	if opts.Password != "secret" {
		t.Errorf("Password=%q, want secret", opts.Password)
	}
	if opts.DB != 3 {
		t.Errorf("DB=%d, want 3", opts.DB)
	}
	if opts.DialTimeout != 800*time.Millisecond {
		t.Errorf("DialTimeout=%v, want 800ms", opts.DialTimeout)
	}
	if opts.ReadTimeout != 500*time.Millisecond {
		t.Errorf("ReadTimeout=%v, want 500ms", opts.ReadTimeout)
	}
	if opts.WriteTimeout != 500*time.Millisecond {
		t.Errorf("WriteTimeout=%v, want 500ms", opts.WriteTimeout)
	}
	if opts.PoolTimeout != time.Second {
		t.Errorf("PoolTimeout=%v, want 1s", opts.PoolTimeout)
	}
	if opts.PoolSize != 50 {
		t.Errorf("PoolSize=%d, want 50", opts.PoolSize)
	}
	if metricsName != "cache" {
		t.Errorf("metricsName=%q, want cache", metricsName)
	}
}

// TestRedisOptionsFromParams_RequireAddr keeps the existing required-field
// guard intact across the refactor.
func TestRedisOptionsFromParams_RequireAddr(t *testing.T) {
	if _, _, err := redisOptionsFromParams(map[string]any{}); err == nil {
		t.Fatal("missing addr should error")
	}
}

// TestRedisCommandStatus pins the command-error -> metric-label-status
// taxonomy. The dashboard built on these labels distinguishes "Redis is
// slow" (timeout) from "we ran out of pool" (pool_timeout) from "Redis
// rejected the command" (error); merging any two of these into a single
// bucket would re-introduce the classification gap that the 2026-06-22
// outage exposed (the only signal then was PING latency, which conflated
// all three).
func TestRedisCommandStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, "ok"},
		{"redis_nil_is_cache_miss", redis.Nil, "ok"},
		{"deadline_exceeded", context.DeadlineExceeded, "timeout"},
		{"context_canceled", context.Canceled, "timeout"},
		{"net_timeout", &timeoutNetError{}, "timeout"},
		{"pool_timeout", redis.ErrPoolTimeout, "pool_timeout"},
		{"plain_error", errors.New("connection refused"), "error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := redisCommandStatus(c.err); got != c.want {
				t.Errorf("redisCommandStatus(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

// timeoutNetError is a minimal net.Error fake that reports Timeout()=true.
// Mirrors the contract of net.OpError when the underlying syscall returns
// EAGAIN after SO_RCVTIMEO fires — go-redis surfaces these directly
// without wrapping in context.DeadlineExceeded, so we must classify them
// via the net.Error branch.
type timeoutNetError struct{}

func (*timeoutNetError) Error() string   { return "i/o timeout" }
func (*timeoutNetError) Timeout() bool   { return true }
func (*timeoutNetError) Temporary() bool { return true }

// TestRedisCommandHook_RecordsLatencyAndStatus exercises the hook end-to-end
// through a miniredis fixture: the resource stays exactly as production code
// constructs it (factory + metrics name), every command goes through the
// hook, and we verify both metrics fire with the expected (command, status)
// labels.
func TestRedisCommandHook_RecordsLatencyAndStatus(t *testing.T) {
	s := miniredis.RunT(t)
	rec := &recordingProvider{}

	r := newRedisConnResource(redisClientFor(s.Addr()), "cache", rec)
	defer func() { _ = r.Close() }()

	// Drive a few commands. miniredis is in-process, so latency is tiny but
	// the hook still fires; we assert label tuples were observed.
	ctx := context.Background()
	if err := r.Client().Set(ctx, "k", "v", 0).Err(); err != nil {
		t.Fatalf("SET: %v", err)
	}
	if _, err := r.Client().Get(ctx, "k").Result(); err != nil {
		t.Fatalf("GET: %v", err)
	}
	// Cache miss: redis.Nil should classify as ok, not error.
	if _, err := r.Client().Get(ctx, "missing").Result(); err != redis.Nil {
		t.Fatalf("GET missing: %v (want redis.Nil)", err)
	}

	// Both Counter and Histogram must have observed each call. The recorder
	// stores the last-bound label tuple per metric instance; we confirm the
	// names exist and the GET-with-cache-miss landed under "ok" not "error".
	if !rec.counterObserved("pine_redis_command_total", "cache", "SET", "ok") {
		t.Errorf("SET ok counter not observed: %v", rec.counterLabels)
	}
	if !rec.counterObserved("pine_redis_command_total", "cache", "GET", "ok") {
		t.Errorf("GET ok counter not observed: %v", rec.counterLabels)
	}
	if !rec.histogramObserved("pine_redis_command_duration_seconds", "cache", "GET", "ok") {
		t.Errorf("GET histogram not observed: %v", rec.histogramLabels)
	}
}

// TestRedisCommandHook_GatedByMetricsName confirms the no-metrics path: when
// metrics_name is empty no hook is attached and no metric is created. This
// keeps deployments that opt out of resource metrics paying zero observability
// cost.
func TestRedisCommandHook_GatedByMetricsName(t *testing.T) {
	s := miniredis.RunT(t)
	rec := &recordingProvider{}

	r := newRedisConnResource(redisClientFor(s.Addr()), "", rec)
	defer func() { _ = r.Close() }()

	if err := r.Client().Set(context.Background(), "k", "v", 0).Err(); err != nil {
		t.Fatalf("SET: %v", err)
	}
	if rec.counterCreated("pine_redis_command_total") {
		t.Error("command counter created with empty metrics_name")
	}
	if rec.histogramCreated("pine_redis_command_duration_seconds") {
		t.Error("command histogram created with empty metrics_name")
	}
}

// TestRedisCommandHook_FiltersProtocolAndProbe verifies the hook elides
// connection-lifecycle (HELLO / CLIENT / AUTH / SELECT) and probe (PING)
// commands so the per-command metric reflects only business traffic. This
// is what keeps the cells byte-comparable across pine-go / pine-java /
// pine-cpp under the same workload (the latter two instrument at the
// operator-level facade and never see lifecycle traffic in the first
// place).
func TestRedisCommandHook_FiltersProtocolAndProbe(t *testing.T) {
	s := miniredis.RunT(t)
	rec := &recordingProvider{}

	r := newRedisConnResource(redisClientFor(s.Addr()), "cache", rec)
	defer func() { _ = r.Close() }()

	ctx := context.Background()
	// Real workload command — must be recorded.
	if err := r.Client().Set(ctx, "k", "v", 0).Err(); err != nil {
		t.Fatalf("SET: %v", err)
	}
	// Probe-equivalent — must NOT be recorded under SET's command label
	// nor under PING.
	if err := r.Client().Ping(ctx).Err(); err != nil {
		t.Fatalf("PING: %v", err)
	}

	if !rec.counterObserved("pine_redis_command_total", "cache", "SET", "ok") {
		t.Errorf("SET should be recorded: %v", rec.counterLabels)
	}
	if rec.counterObserved("pine_redis_command_total", "cache", "PING", "ok") {
		t.Errorf("PING should be filtered out, but was recorded: %v", rec.counterLabels)
	}
	if rec.counterObserved("pine_redis_command_total", "cache", "HELLO", "ok") {
		t.Errorf("HELLO should be filtered out: %v", rec.counterLabels)
	}
	if rec.counterObserved("pine_redis_command_total", "cache", "CLIENT", "ok") {
		t.Errorf("CLIENT should be filtered out: %v", rec.counterLabels)
	}
}

// TestIsProtocolOrProbeCommand pins the filter set so a future expansion
// (e.g. adding QUIT / RESET) doesn't accidentally drop a command operators
// rely on. Case-insensitive: go-redis surfaces command names lower-cased,
// but we keep the filter robust to either casing because miniredis +
// real Redis differ in their reply formatting and a third-party fork
// might too.
func TestIsProtocolOrProbeCommand(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"hello", true}, {"HELLO", true},
		{"client", true}, {"CLIENT", true},
		{"ping", true}, {"PING", true},
		{"auth", true}, {"AUTH", true},
		{"select", true}, {"SELECT", true},
		{"get", false}, {"GET", false},
		{"set", false}, {"SET", false},
		{"zrangebyscore", false},
	}
	for _, c := range cases {
		if got := isProtocolOrProbeCommand(c.name); got != c.want {
			t.Errorf("isProtocolOrProbeCommand(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
