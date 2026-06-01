package metrics

import (
	"encoding/json"
	"testing"
)

func TestCollectorSnapshotShape(t *testing.T) {
	c := NewCollector()

	g := c.NewGauge(MetricOpts{Name: "pine_redis_pool_total_conns", Help: "test"})
	g.With("cache").Set(3)

	idle := c.NewGauge(MetricOpts{Name: "pine_redis_pool_idle_conns", Help: "test"})
	idle.With("cache").Set(2)

	up := c.NewGauge(MetricOpts{Name: "pine_redis_up", Help: "test"})
	up.With("cache").Set(1)

	h := c.NewHistogram(HistogramOpts{
		MetricOpts: MetricOpts{Name: "pine_redis_ping_duration_seconds", Help: "test"},
	})
	h.With("cache").Observe(0.001) // 1ms → 1_000_000 ns

	snap := c.Snapshot()

	total, ok := snap["pine_redis_pool_total_conns"].(map[string]float64)
	if !ok || total["cache"] != 3 {
		t.Fatalf("total_conns: got %#v", snap["pine_redis_pool_total_conns"])
	}
	if up := snap["pine_redis_up"].(map[string]float64); up["cache"] != 1 {
		t.Fatalf("up: got %#v", up)
	}

	hist, ok := snap["pine_redis_ping_duration_seconds"].(map[string]any)
	if !ok {
		t.Fatalf("histogram shape: got %#v", snap["pine_redis_ping_duration_seconds"])
	}
	cell := hist["cache"].(map[string]int64)
	if cell["count"] != 1 || cell["sum_ns"] != 1_000_000 {
		t.Fatalf("histogram cell: got %#v", cell)
	}
}

func TestCollectorSnapshotJSONDeterministic(t *testing.T) {
	c := NewCollector()
	g := c.NewGauge(MetricOpts{Name: "pine_redis_up", Help: "test"})
	g.With("cache").Set(1)
	h := c.NewHistogram(HistogramOpts{MetricOpts: MetricOpts{Name: "pine_redis_ping_duration_seconds", Help: "test"}})
	h.With("cache").Observe(0.0005)

	b1, _ := json.Marshal(c.Snapshot())
	b2, _ := json.Marshal(c.Snapshot())
	if string(b1) != string(b2) {
		t.Fatalf("non-deterministic JSON: %s vs %s", b1, b2)
	}
	want := `{"pine_redis_ping_duration_seconds":{"cache":{"count":1,"sum_ns":500000}},"pine_redis_up":{"cache":1}}`
	if string(b1) != want {
		t.Fatalf("unexpected JSON:\n got: %s\nwant: %s", b1, want)
	}
}
