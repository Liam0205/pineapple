#pragma once

// In-memory metrics aggregation + fan-out for the bundled server.
//
// Mirrors pine-go/pkg/metrics (collector.go + tee.go) and pine-java
// (MetricsCollector + TeeProvider):
//
//   - Collector implements Provider, aggregating observations so resource-level
//     metrics (redis pool gauges + PING-probe latency) can be exported through
//     GET /stats.resources without an external Prometheus backend. to_json()
//     returns a byte-exact, deterministic view: metric name -> label-value key
//     -> value; counters/gauges hold a number, histograms hold {count, sum_ns}.
//
//   - TeeProvider implements Provider, fanning every metric out to all the
//     given providers. The server hands the ResourceManager a
//     Tee(injected_provider, collector), so resource metrics reach both the
//     caller's Provider (e.g. Prometheus) and /stats.resources, while engine
//     metrics (which use the injected provider directly) stay out of the
//     resources subtree.

#include "pine/metrics.hpp"

#include <cstdint>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <vector>

namespace pine {
namespace metrics {

class Collector : public Provider {
 public:
  Collector();
  ~Collector() override;

  Counter* new_counter(const MetricOpts& opts) override;
  Gauge* new_gauge(const MetricOpts& opts) override;
  Histogram* new_histogram(const HistogramOpts& opts) override;

  // Serialized JSON object with keys sorted lexicographically at every level,
  // byte-exact with pine-go's Collector.Snapshot under encoding/json.
  std::string to_json() const;

 private:
  class CollectorCounter;
  class CollectorGauge;
  class CollectorHistogram;

  Counter* intern_counter(const std::string& name, const std::string& key);
  Gauge* intern_gauge(const std::string& name, const std::string& key);
  Histogram* intern_histogram(const std::string& name, const std::string& key);

  void inc_counter(const std::string& name, const std::string& key);
  void set_gauge(const std::string& name, const std::string& key, double value);
  void add_gauge(const std::string& name, const std::string& key, double delta);
  void observe_hist(const std::string& name, const std::string& key, double value);

  struct HistCell {
    int64_t count = 0;
    int64_t sum_ns = 0;
  };

  mutable std::mutex mu_;
  std::map<std::string, std::map<std::string, double>> counters_;
  std::map<std::string, std::map<std::string, double>> gauges_;
  std::map<std::string, std::map<std::string, HistCell>> histograms_;
  std::vector<std::unique_ptr<Counter>> counter_objs_;
  std::vector<std::unique_ptr<Gauge>> gauge_objs_;
  std::vector<std::unique_ptr<Histogram>> hist_objs_;
};

class TeeProvider : public Provider {
 public:
  explicit TeeProvider(std::vector<Provider*> providers);
  ~TeeProvider() override;

  Counter* new_counter(const MetricOpts& opts) override;
  Gauge* new_gauge(const MetricOpts& opts) override;
  Histogram* new_histogram(const HistogramOpts& opts) override;

 private:
  class TeeCounter;
  class TeeGauge;
  class TeeHistogram;

  Counter* intern_counter(std::vector<Counter*> children);
  Gauge* intern_gauge(std::vector<Gauge*> children);
  Histogram* intern_histogram(std::vector<Histogram*> children);

  std::mutex mu_;
  std::vector<Provider*> providers_;
  std::vector<std::unique_ptr<Counter>> counter_objs_;
  std::vector<std::unique_ptr<Gauge>> gauge_objs_;
  std::vector<std::unique_ptr<Histogram>> hist_objs_;
};

}  // namespace metrics
}  // namespace pine
