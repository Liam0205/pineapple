// Package metrics defines a pluggable metrics interface for Pine.
//
// Pine instruments its internal components (scheduler, config reload, Lua pool)
// through this interface. The default [Nop] provider discards all observations
// at zero cost. To export metrics to Prometheus or another backend, implement
// [Provider] and pass it via [pine.WithMetrics].
package metrics

import "time"

// Counter is a cumulative metric that only goes up.
type Counter interface {
	With(labelValues ...string) Counter
	Inc()
}

// Gauge is a metric that can go up and down.
type Gauge interface {
	With(labelValues ...string) Gauge
	Set(value float64)
	Add(delta float64)
}

// Histogram records observations into configurable buckets.
type Histogram interface {
	With(labelValues ...string) Histogram
	Observe(value float64)
}

// MetricOpts configures a Counter or Gauge.
type MetricOpts struct {
	Name       string
	Help       string
	LabelNames []string
}

// HistogramOpts configures a Histogram.
type HistogramOpts struct {
	MetricOpts
	Buckets []float64 // nil → implementation-chosen defaults
}

// Provider creates typed metrics. Implementations register metrics with
// their backend (e.g., Prometheus registry) inside these factory methods.
type Provider interface {
	NewCounter(opts MetricOpts) Counter
	NewGauge(opts MetricOpts) Gauge
	NewHistogram(opts HistogramOpts) Histogram
}

// DurationSeconds converts a time.Duration to fractional seconds,
// the standard unit for Prometheus duration histograms.
func DurationSeconds(d time.Duration) float64 {
	return d.Seconds()
}
