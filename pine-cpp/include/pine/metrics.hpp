#pragma once

// Pluggable metrics interfaces for pine-cpp.
//
// Mirrors pine-go/pkg/metrics:
// - Counter / Gauge / Histogram interfaces with optional label values
// - MetricOpts / HistogramOpts factory inputs
// - Provider creates typed metrics; NopProvider discards observations
//
// Engine instruments scheduler + DAG execution through Provider. To export
// metrics, implement Provider and pass via EngineOptions::metrics_provider.
//
// IMPLEMENTER'S CONTRACT — the authoritative copy lives in
// pine-go/pkg/metrics/metrics.go (package doc, "Implementer's contract"). This
// file aligns to it and deliberately does not restate it, so the two cannot
// drift. Read it before implementing Provider; the four points it covers are
// concurrency, units, buckets-as-suggestion and label-value lifetime, and none
// of them are inferable from the signatures below.
//
// The single point repeated here because it affects correctness rather than
// accuracy: observe(), inc(), set(), add() and with() ARE CALLED CONCURRENTLY
// (the scheduler runs independent operators in parallel), so an implementation
// must be safe for concurrent use. The bundled Collector and nop_provider both
// satisfy this, which means a racy Provider will not be caught by pine's tests.

#include <chrono>
#include <memory>
#include <string>
#include <vector>

namespace pine {
namespace metrics {

class Counter {
 public:
  virtual ~Counter() = default;
  // Returns a counter narrowed to the given label values. Returned pointer
  // is owned by the underlying Provider — do not delete.
  virtual Counter* with(const std::vector<std::string>& label_values) = 0;
  virtual void inc() = 0;
};

class Gauge {
 public:
  virtual ~Gauge() = default;
  virtual Gauge* with(const std::vector<std::string>& label_values) = 0;
  virtual void set(double value) = 0;
  virtual void add(double delta) = 0;
};

class Histogram {
 public:
  virtual ~Histogram() = default;
  // Returned pointer is owned by the underlying Provider — do not delete.
  virtual Histogram* with(const std::vector<std::string>& label_values) = 0;
  // For duration histograms the value is in SECONDS; the signature cannot
  // enforce it. See the authoritative contract in pine-go/pkg/metrics.
  virtual void observe(double value) = 0;
};

struct MetricOpts {
  std::string name;
  std::string help;
  std::vector<std::string> label_names;
};

struct HistogramOpts {
  MetricOpts opts;
  // Pine's SUGGESTED boundaries. Empty means "no suggestion". Both cases really
  // occur — the engine and server histograms pass a suggestion, the Redis probe
  // histograms pass none — so a Provider wanting its own defaults cannot wait for
  // empty and must actively ignore a non-empty value. No bundled Provider reads
  // it. See the authoritative contract in pine-go/pkg/metrics/metrics.go.
  std::vector<double> buckets;
};

class Provider {
 public:
  virtual ~Provider() = default;
  virtual Counter* new_counter(const MetricOpts& opts) = 0;
  virtual Gauge* new_gauge(const MetricOpts& opts) = 0;
  virtual Histogram* new_histogram(const HistogramOpts& opts) = 0;
};

// Returns a singleton no-op Provider. Discards every observation at zero cost.
Provider* nop_provider();

inline double duration_seconds(std::chrono::nanoseconds d) {
  return static_cast<double>(d.count()) / 1e9;
}

}  // namespace metrics
}  // namespace pine
