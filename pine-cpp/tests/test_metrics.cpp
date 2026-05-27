#include "pine/metrics.hpp"
#include "pine/pine.hpp"

#include <doctest/doctest.h>

#include <atomic>
#include <map>
#include <string>
#include <vector>

using namespace pine;

namespace {

// Recording metrics provider for assertions. Reuses one instance per metric name.
class RecordingProvider : public metrics::Provider {
 public:
  struct CounterImpl : metrics::Counter {
    std::atomic<int64_t> count{0};
    metrics::Counter* with(const std::vector<std::string>&) override {
      return this;
    }
    void inc() override {
      count.fetch_add(1, std::memory_order_relaxed);
    }
  };
  struct GaugeImpl : metrics::Gauge {
    std::atomic<int64_t> last_set{0};
    std::atomic<int64_t> set_calls{0};
    metrics::Gauge* with(const std::vector<std::string>&) override {
      return this;
    }
    void set(double v) override {
      last_set.store(static_cast<int64_t>(v), std::memory_order_relaxed);
      set_calls.fetch_add(1, std::memory_order_relaxed);
    }
    void add(double) override {
    }
  };
  struct HistogramImpl : metrics::Histogram {
    std::atomic<int64_t> observe_calls{0};
    metrics::Histogram* with(const std::vector<std::string>&) override {
      return this;
    }
    void observe(double) override {
      observe_calls.fetch_add(1, std::memory_order_relaxed);
    }
  };

  metrics::Counter* new_counter(const metrics::MetricOpts& o) override {
    return counters_.emplace(o.name, std::make_unique<CounterImpl>()).first->second.get();
  }
  metrics::Gauge* new_gauge(const metrics::MetricOpts& o) override {
    return gauges_.emplace(o.name, std::make_unique<GaugeImpl>()).first->second.get();
  }
  metrics::Histogram* new_histogram(const metrics::HistogramOpts& o) override {
    return histograms_.emplace(o.opts.name, std::make_unique<HistogramImpl>()).first->second.get();
  }

  CounterImpl* counter(const std::string& name) {
    return counters_.count(name) ? counters_[name].get() : nullptr;
  }
  GaugeImpl* gauge(const std::string& name) {
    return gauges_.count(name) ? gauges_[name].get() : nullptr;
  }
  HistogramImpl* histogram(const std::string& name) {
    return histograms_.count(name) ? histograms_[name].get() : nullptr;
  }

 private:
  std::map<std::string, std::unique_ptr<CounterImpl>> counters_;
  std::map<std::string, std::unique_ptr<GaugeImpl>> gauges_;
  std::map<std::string, std::unique_ptr<HistogramImpl>> histograms_;
};

const char* kConfig = R"({
  "pipeline_config": {
    "operators": {
      "src": {
        "type_name": "recall_static",
        "items": [{"id": "a"}, {"id": "b"}],
        "$metadata": {"common_input": [], "common_output": [], "item_input": [], "item_output": ["id"]}
      }
    }
  },
  "pipeline_group": {"main": {"pipeline": ["src"]}},
  "flow_contract": {"common_input": [], "item_input": [], "common_output": [], "item_output": ["id"]}
})";

}  // namespace

TEST_CASE("metrics: nop provider is the default and discards observations") {
  auto cfg = load_config_from_json(kConfig);
  Engine engine(std::move(cfg));
  CHECK(engine.metrics_provider() == metrics::nop_provider());

  Request req;
  Result result = engine.execute(req);
  CHECK(result.items.size() == 2);
}

TEST_CASE("metrics: provider receives scheduler/op/dag observations") {
  RecordingProvider provider;
  EngineOptions opts;
  opts.metrics_provider = &provider;

  auto cfg = load_config_from_json(kConfig);
  Engine engine(std::move(cfg), opts);

  Request req;
  engine.execute(req);

  auto* runs = provider.counter("pine_scheduler_runs_total");
  REQUIRE(runs);
  CHECK(runs->count.load() == 1);

  auto* op_exec = provider.counter("pine_operator_exec_total");
  REQUIRE(op_exec);
  CHECK(op_exec->count.load() == 1);

  auto* dag_total = provider.counter("pine_dag_executions_total");
  REQUIRE(dag_total);
  CHECK(dag_total->count.load() >= 1);

  auto* dur = provider.histogram("pine_operator_exec_duration_seconds");
  REQUIRE(dur);
  CHECK(dur->observe_calls.load() == 1);

  auto* dag_dur = provider.histogram("pine_dag_execution_duration_seconds");
  REQUIRE(dag_dur);
  CHECK(dag_dur->observe_calls.load() == 1);

  auto* active = provider.gauge("pine_operator_active");
  REQUIRE(active);
  // Each op invokes set twice — once at entry (current high
  // water-mark) and once at ActiveGuard destruction (after dec).
  CHECK(active->set_calls.load() == 2);
}
