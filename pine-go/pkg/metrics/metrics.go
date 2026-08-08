// Package metrics defines a pluggable metrics interface for Pine.
//
// Pine instruments its internal components (scheduler, config reload, Lua pool)
// through this interface. The default [Nop] provider discards all observations
// at zero cost. To export metrics to Prometheus or another backend, implement
// [Provider] and pass it via [pine.WithMetrics].
//
// # Implementer's contract
//
// This section is the AUTHORITATIVE definition of what Pine guarantees to a
// Provider implementation and what it requires in return. pine-java's
// page.liam.pine.metrics and pine-cpp's include/pine/metrics.hpp align to this
// file rather than restating it; design_doc/08_observability.md points here.
//
// Four things a Provider must know, none of which are inferable from the method
// signatures:
//
//  1. CONCURRENCY. Observe, Inc, Set, Add and With are called concurrently.
//     Pine's scheduler runs independent operators in parallel goroutines, and
//     each records into the same metric, so an implementation MUST be safe for
//     concurrent use. Pine does no locking on its behalf. The bundled
//     implementations satisfy this — Collector holds a mutex, Nop is stateless —
//     which means a racy Provider will not be caught by Pine's own tests.
//
//  2. UNITS. Duration histograms receive SECONDS as a float64, produced by
//     [DurationSeconds]. This is a convention, not something the type enforces:
//     Observe takes a bare float64, so an implementation that assumes
//     milliseconds will silently misreport by 1000x.
//
//  3. BUCKETS ARE A SUGGESTION. HistogramOpts.Buckets carries Pine's suggested
//     boundaries, chosen to match observed operator latency ranges. No bundled
//     Provider reads them — Collector aggregates count and sum only — so they
//     exist purely as advice to an external backend. An implementation is free
//     to honour, replace, or ignore them (for example to use Prometheus native
//     histograms, which have no fixed boundaries).
//
//  4. LABEL VALUE LIFETIME. With returns a metric narrowed to the given label
//     values. Pine calls With with no subsequent observation during startup
//     pre-initialisation, so that backends expose every operator's series with
//     zero values before the first request. An implementation must tolerate
//     With-without-Observe, and must not assume the number of distinct label
//     value sets is bounded by traffic — at startup it equals the operator
//     count.
//
// What Pine does NOT promise: that any bundled Provider computes quantiles.
// Collector stores {count, sum} per series, which yields an average and nothing
// more. Percentiles require an implementation backed by a real histogram. The
// engine's own /stats endpoint is a separate mechanism (runtime.Stats) that
// reports total/max/avg duration and is cross-validated; see
// llmdoc/reference/metrics-observability.md for how the two layers divide.
package metrics

import "time"

// Counter is a cumulative metric that only goes up. Implementations must be
// safe for concurrent use; see the package doc's implementer's contract.
type Counter interface {
	With(labelValues ...string) Counter
	Inc()
}

// Gauge is a metric that can go up and down. Implementations must be safe for
// concurrent use; see the package doc's implementer's contract.
type Gauge interface {
	With(labelValues ...string) Gauge
	Set(value float64)
	Add(delta float64)
}

// Histogram records observations. Implementations must be safe for concurrent
// use; duration histograms receive seconds. See the package doc's implementer's
// contract for both, plus why HistogramOpts.Buckets is only a suggestion.
type Histogram interface {
	With(labelValues ...string) Histogram
	// Observe records one value. For duration histograms the value is in
	// SECONDS (see DurationSeconds) — the signature cannot enforce this.
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
	// Buckets carries Pine's SUGGESTED boundaries; see the package doc. A nil
	// slice means "no suggestion, choose your own".
	//
	// Both cases really occur, so a Provider must handle both: the engine and
	// server histograms pass a suggestion, while the Redis probe histograms in
	// operators/transform pass nil. A Provider that wants its own defaults
	// therefore cannot wait for nil — it has to ignore a non-nil suggestion
	// actively — and one that trusts the suggestion cannot assume it is present.
	Buckets []float64
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
