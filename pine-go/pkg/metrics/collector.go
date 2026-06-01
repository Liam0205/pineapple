package metrics

import (
	"math"
	"sync"
)

// Collector is a Provider that aggregates observations in memory so they can be
// exported through an internal endpoint (e.g. the bundled server's /stats)
// without an external Prometheus backend. The server hands a Collector to its
// ResourceManager, making resource-level metrics (e.g. redis pool gauges +
// PING-probe latency) observable via /stats while keeping engine metrics out of
// that subtree.
//
// Snapshot returns a deterministic, JSON-serializable view shaped like the
// http subtree: metric name -> label-value key -> value. Counters/gauges hold a
// float64; histograms hold {count, sum_ns}. The label-value key joins a
// metric's label values with a single space (matching the http convention); a
// metric with no labels uses the empty string "".
type Collector struct {
	mu         sync.Mutex
	counters   map[string]map[string]float64
	gauges     map[string]map[string]float64
	histograms map[string]map[string]*histCell
}

type histCell struct {
	count int64
	sumNs int64
}

// NewCollector returns an empty Collector.
func NewCollector() *Collector {
	return &Collector{
		counters:   map[string]map[string]float64{},
		gauges:     map[string]map[string]float64{},
		histograms: map[string]map[string]*histCell{},
	}
}

func (c *Collector) NewCounter(opts MetricOpts) Counter {
	c.mu.Lock()
	if c.counters[opts.Name] == nil {
		c.counters[opts.Name] = map[string]float64{}
	}
	c.mu.Unlock()
	return &collectorCounter{c: c, name: opts.Name}
}

func (c *Collector) NewGauge(opts MetricOpts) Gauge {
	c.mu.Lock()
	if c.gauges[opts.Name] == nil {
		c.gauges[opts.Name] = map[string]float64{}
	}
	c.mu.Unlock()
	return &collectorGauge{c: c, name: opts.Name}
}

func (c *Collector) NewHistogram(opts HistogramOpts) Histogram {
	c.mu.Lock()
	if c.histograms[opts.Name] == nil {
		c.histograms[opts.Name] = map[string]*histCell{}
	}
	c.mu.Unlock()
	return &collectorHistogram{c: c, name: opts.Name}
}

// Snapshot returns the current aggregated values. Output is JSON-serializable;
// encoding/json sorts map keys, so the byte output is deterministic.
func (c *Collector) Snapshot() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]any, len(c.counters)+len(c.gauges)+len(c.histograms))
	for name, cells := range c.counters {
		m := make(map[string]float64, len(cells))
		for k, v := range cells {
			m[k] = v
		}
		out[name] = m
	}
	for name, cells := range c.gauges {
		m := make(map[string]float64, len(cells))
		for k, v := range cells {
			m[k] = v
		}
		out[name] = m
	}
	for name, cells := range c.histograms {
		m := make(map[string]any, len(cells))
		for k, cell := range cells {
			m[k] = map[string]int64{"count": cell.count, "sum_ns": cell.sumNs}
		}
		out[name] = m
	}
	return out
}

func labelKey(values []string) string {
	out := ""
	for i, v := range values {
		if i > 0 {
			out += " "
		}
		out += v
	}
	return out
}

type collectorCounter struct {
	c    *Collector
	name string
	key  string
}

func (m *collectorCounter) With(labelValues ...string) Counter {
	return &collectorCounter{c: m.c, name: m.name, key: labelKey(labelValues)}
}

func (m *collectorCounter) Inc() {
	m.c.mu.Lock()
	m.c.counters[m.name][m.key]++
	m.c.mu.Unlock()
}

type collectorGauge struct {
	c    *Collector
	name string
	key  string
}

func (m *collectorGauge) With(labelValues ...string) Gauge {
	return &collectorGauge{c: m.c, name: m.name, key: labelKey(labelValues)}
}

func (m *collectorGauge) Set(value float64) {
	m.c.mu.Lock()
	m.c.gauges[m.name][m.key] = value
	m.c.mu.Unlock()
}

func (m *collectorGauge) Add(delta float64) {
	m.c.mu.Lock()
	m.c.gauges[m.name][m.key] += delta
	m.c.mu.Unlock()
}

type collectorHistogram struct {
	c    *Collector
	name string
	key  string
}

func (m *collectorHistogram) With(labelValues ...string) Histogram {
	return &collectorHistogram{c: m.c, name: m.name, key: labelKey(labelValues)}
}

// Observe takes a value in seconds (the Provider convention for duration
// histograms) and accumulates it as integer nanoseconds, matching the http
// subtree's sum_ns so /stats stays float-free and byte-exact across runtimes.
func (m *collectorHistogram) Observe(value float64) {
	m.c.mu.Lock()
	cell := m.c.histograms[m.name][m.key]
	if cell == nil {
		cell = &histCell{}
		m.c.histograms[m.name][m.key] = cell
	}
	cell.count++
	cell.sumNs += int64(math.Round(value * 1e9))
	m.c.mu.Unlock()
}
