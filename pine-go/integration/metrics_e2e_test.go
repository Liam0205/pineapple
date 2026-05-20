package integration

import (
	"context"
	"sync"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
	"github.com/Liam0205/pineapple/pine-go/pkg/metrics"
	_ "github.com/Liam0205/pineapple/pine-go/operators"
)

type recordingCounter struct {
	mu    sync.Mutex
	count int
	kids  map[string]*recordingCounter
}

func newRecordingCounter() *recordingCounter {
	return &recordingCounter{kids: make(map[string]*recordingCounter)}
}

func (c *recordingCounter) With(labelValues ...string) metrics.Counter {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := ""
	for _, v := range labelValues {
		key += v + ","
	}
	if kid, ok := c.kids[key]; ok {
		return kid
	}
	kid := newRecordingCounter()
	c.kids[key] = kid
	return kid
}

func (c *recordingCounter) Inc() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
}

func (c *recordingCounter) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	sum := c.count
	for _, kid := range c.kids {
		sum += kid.total()
	}
	return sum
}

type recordingGauge struct{}

func (g *recordingGauge) With(...string) metrics.Gauge { return g }
func (g *recordingGauge) Set(float64)                  {}
func (g *recordingGauge) Add(float64)                  {}

type recordingHistogram struct {
	mu    sync.Mutex
	count int
}

func (h *recordingHistogram) With(...string) metrics.Histogram { return h }
func (h *recordingHistogram) Observe(float64) {
	h.mu.Lock()
	h.count++
	h.mu.Unlock()
}

type recordingProvider struct {
	opExecTotal *recordingCounter
	dagTotal    *recordingCounter
	dagDuration *recordingHistogram
}

func (p *recordingProvider) NewCounter(opts metrics.MetricOpts) metrics.Counter {
	switch opts.Name {
	case "pine_operator_exec_total":
		return p.opExecTotal
	case "pine_dag_executions_total":
		return p.dagTotal
	default:
		return newRecordingCounter()
	}
}

func (p *recordingProvider) NewGauge(metrics.MetricOpts) metrics.Gauge {
	return &recordingGauge{}
}

func (p *recordingProvider) NewHistogram(opts metrics.HistogramOpts) metrics.Histogram {
	if opts.Name == "pine_dag_execution_duration_seconds" {
		return p.dagDuration
	}
	return &recordingHistogram{}
}

func TestMetricsRecordedAfterExecute(t *testing.T) {
	prov := &recordingProvider{
		opExecTotal: newRecordingCounter(),
		dagTotal:    newRecordingCounter(),
		dagDuration: &recordingHistogram{},
	}

	cfg := loadConfig(t, "../testdata/e2e_lua_pipeline.json")
	engine, err := pine.NewEngine(cfg, pine.WithMetrics(prov))
	if err != nil {
		t.Fatal(err)
	}

	req := &pine.Request{
		Common: map[string]any{"user_age": 25},
		Items:  []map[string]any{},
	}

	_, err = engine.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	if prov.opExecTotal.total() == 0 {
		t.Error("pine_operator_exec_total should be non-zero after Execute")
	}
	if prov.dagTotal.total() == 0 {
		t.Error("pine_dag_executions_total should be non-zero after Execute")
	}
	if prov.dagDuration.count == 0 {
		t.Error("pine_dag_execution_duration_seconds should have observations after Execute")
	}
}

func TestStatsPreInitBeforeExecute(t *testing.T) {
	cfg := loadConfig(t, "../testdata/e2e_lua_pipeline.json")
	engine, err := pine.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}

	stats := engine.Stats()
	if len(stats) == 0 {
		t.Fatal("Stats() should return all operators immediately after NewEngine, before any Execute call")
	}

	for name, snap := range stats {
		if snap.ExecCount != 0 || snap.SkipCount != 0 || snap.ErrorCount != 0 {
			t.Errorf("operator %q: expected all zero counts before Execute, got exec=%d skip=%d error=%d",
				name, snap.ExecCount, snap.SkipCount, snap.ErrorCount)
		}
	}
}
