package metrics

import "testing"

// recProvider records every metric op so a test can assert the tee fanned out.
type recProvider struct {
	counterIncs int
	gaugeSets   []float64
	histObs     []float64
	lastLabels  []string
}

func (r *recProvider) NewCounter(MetricOpts) Counter        { return &recCounter{r: r} }
func (r *recProvider) NewGauge(MetricOpts) Gauge            { return &recGauge{r: r} }
func (r *recProvider) NewHistogram(HistogramOpts) Histogram { return &recHist{r: r} }

type recCounter struct{ r *recProvider }

func (c *recCounter) With(lv ...string) Counter { c.r.lastLabels = lv; return c }
func (c *recCounter) Inc()                      { c.r.counterIncs++ }

type recGauge struct{ r *recProvider }

func (g *recGauge) With(lv ...string) Gauge { g.r.lastLabels = lv; return g }
func (g *recGauge) Set(v float64)           { g.r.gaugeSets = append(g.r.gaugeSets, v) }
func (g *recGauge) Add(float64)             {}

type recHist struct{ r *recProvider }

func (h *recHist) With(lv ...string) Histogram { h.r.lastLabels = lv; return h }
func (h *recHist) Observe(v float64)           { h.r.histObs = append(h.r.histObs, v) }

func TestTeeFansOut(t *testing.T) {
	a, b := &recProvider{}, &recProvider{}
	tee := Tee(a, b)

	c := tee.NewCounter(MetricOpts{Name: "x"}).With("l")
	c.Inc()
	c.Inc()
	if a.counterIncs != 2 || b.counterIncs != 2 {
		t.Fatalf("counter not fanned out: a=%d b=%d", a.counterIncs, b.counterIncs)
	}

	g := tee.NewGauge(MetricOpts{Name: "g"}).With("cache")
	g.Set(5)
	if len(a.gaugeSets) != 1 || a.gaugeSets[0] != 5 || len(b.gaugeSets) != 1 {
		t.Fatalf("gauge not fanned out: a=%v b=%v", a.gaugeSets, b.gaugeSets)
	}
	if a.lastLabels[0] != "cache" || b.lastLabels[0] != "cache" {
		t.Fatalf("labels not propagated: a=%v b=%v", a.lastLabels, b.lastLabels)
	}

	h := tee.NewHistogram(HistogramOpts{MetricOpts: MetricOpts{Name: "h"}})
	h.Observe(0.001)
	if len(a.histObs) != 1 || len(b.histObs) != 1 {
		t.Fatalf("histogram not fanned out: a=%v b=%v", a.histObs, b.histObs)
	}
}

// Tee + Collector: the collector aggregates, the recorder confirms the same
// observations reach the injected provider — mirrors the server's fan-out.
func TestTeeWithCollector(t *testing.T) {
	rec := &recProvider{}
	col := NewCollector()
	tee := Tee(rec, col)

	tee.NewGauge(MetricOpts{Name: "pine_redis_up"}).With("cache").Set(1)

	if len(rec.gaugeSets) != 1 || rec.gaugeSets[0] != 1 {
		t.Fatalf("injected provider missed gauge: %v", rec.gaugeSets)
	}
	snap := col.Snapshot()
	up := snap["pine_redis_up"].(map[string]float64)
	if up["cache"] != 1 {
		t.Fatalf("collector missed gauge: %v", up)
	}
}
