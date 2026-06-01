package metrics

// Tee returns a Provider that fans out every metric to all the given providers.
// Each created Counter/Gauge/Histogram forwards With/Inc/Set/Add/Observe to the
// corresponding metric from every underlying provider.
//
// The bundled server uses it to hand the ResourceManager a provider that writes
// both to the caller-injected Provider (e.g. a Prometheus adapter) and to a
// dedicated in-memory [Collector] exposed under /stats.resources — so resource
// metrics reach Prometheus AND /stats without the engine's own metrics leaking
// into the resources subtree (only the ResourceManager writes through the tee).
//
// Providers with zero entries yield no-op metrics. A single provider is wrapped
// transparently.
func Tee(providers ...Provider) Provider {
	return &teeProvider{providers: providers}
}

type teeProvider struct {
	providers []Provider
}

func (t *teeProvider) NewCounter(opts MetricOpts) Counter {
	cs := make([]Counter, len(t.providers))
	for i, p := range t.providers {
		cs[i] = p.NewCounter(opts)
	}
	return &teeCounter{counters: cs}
}

func (t *teeProvider) NewGauge(opts MetricOpts) Gauge {
	gs := make([]Gauge, len(t.providers))
	for i, p := range t.providers {
		gs[i] = p.NewGauge(opts)
	}
	return &teeGauge{gauges: gs}
}

func (t *teeProvider) NewHistogram(opts HistogramOpts) Histogram {
	hs := make([]Histogram, len(t.providers))
	for i, p := range t.providers {
		hs[i] = p.NewHistogram(opts)
	}
	return &teeHistogram{histograms: hs}
}

type teeCounter struct {
	counters []Counter
}

func (t *teeCounter) With(labelValues ...string) Counter {
	cs := make([]Counter, len(t.counters))
	for i, c := range t.counters {
		cs[i] = c.With(labelValues...)
	}
	return &teeCounter{counters: cs}
}

func (t *teeCounter) Inc() {
	for _, c := range t.counters {
		c.Inc()
	}
}

type teeGauge struct {
	gauges []Gauge
}

func (t *teeGauge) With(labelValues ...string) Gauge {
	gs := make([]Gauge, len(t.gauges))
	for i, g := range t.gauges {
		gs[i] = g.With(labelValues...)
	}
	return &teeGauge{gauges: gs}
}

func (t *teeGauge) Set(value float64) {
	for _, g := range t.gauges {
		g.Set(value)
	}
}

func (t *teeGauge) Add(delta float64) {
	for _, g := range t.gauges {
		g.Add(delta)
	}
}

type teeHistogram struct {
	histograms []Histogram
}

func (t *teeHistogram) With(labelValues ...string) Histogram {
	hs := make([]Histogram, len(t.histograms))
	for i, h := range t.histograms {
		hs[i] = h.With(labelValues...)
	}
	return &teeHistogram{histograms: hs}
}

func (t *teeHistogram) Observe(value float64) {
	for _, h := range t.histograms {
		h.Observe(value)
	}
}
