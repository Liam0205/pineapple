package transform

import (
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
