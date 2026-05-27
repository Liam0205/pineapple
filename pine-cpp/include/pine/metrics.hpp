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
  virtual Histogram* with(const std::vector<std::string>& label_values) = 0;
  virtual void observe(double value) = 0;
};

struct MetricOpts {
  std::string name;
  std::string help;
  std::vector<std::string> label_names;
};

struct HistogramOpts {
  MetricOpts opts;
  std::vector<double> buckets;  // empty → implementation-chosen defaults
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
