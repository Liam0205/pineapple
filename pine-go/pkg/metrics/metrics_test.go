package metrics

import "testing"

func TestNopProvider(t *testing.T) {
	p := Nop()

	c := p.NewCounter(MetricOpts{Name: "test_counter", Help: "test"})
	c.Inc()
	c.With("label_val").Inc()

	g := p.NewGauge(MetricOpts{Name: "test_gauge", Help: "test"})
	g.Set(1.0)
	g.Add(2.0)
	g.With("label_val").Add(-1.0)

	h := p.NewHistogram(HistogramOpts{
		MetricOpts: MetricOpts{Name: "test_histogram", Help: "test"},
		Buckets:    []float64{0.01, 0.1, 1.0},
	})
	h.Observe(0.05)
	h.With("label_val").Observe(0.5)
}

func TestDurationSeconds(t *testing.T) {
	got := DurationSeconds(1500000) // 1.5ms in nanoseconds
	if got < 0.0014 || got > 0.0016 {
		t.Errorf("DurationSeconds(1.5ms) = %v, want ~0.0015", got)
	}
}
