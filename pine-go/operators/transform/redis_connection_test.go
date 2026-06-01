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
