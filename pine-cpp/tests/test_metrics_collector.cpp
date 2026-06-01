#include "pine/metrics_collector.hpp"
#include "pine/metrics.hpp"

#include <doctest/doctest.h>

#include <map>
#include <memory>
#include <string>
#include <vector>

using namespace pine;

namespace {

// Recording provider used to assert tee fan-out: each metric records the
// last label values seen plus call counts.
class RecProvider : public metrics::Provider {
 public:
  struct CounterImpl : metrics::Counter {
    int64_t inc_calls = 0;
    std::vector<std::string> last_labels;
    metrics::Counter* with(const std::vector<std::string>& lv) override {
      last_labels = lv;
      return this;
    }
    void inc() override { inc_calls++; }
  };
  struct GaugeImpl : metrics::Gauge {
    double last_set = 0;
    int64_t set_calls = 0;
    metrics::Gauge* with(const std::vector<std::string>&) override { return this; }
    void set(double v) override {
      last_set = v;
      set_calls++;
    }
    void add(double) override {}
  };
  struct HistogramImpl : metrics::Histogram {
    int64_t observe_calls = 0;
    double last_observe = 0;
    metrics::Histogram* with(const std::vector<std::string>&) override { return this; }
    void observe(double v) override {
      observe_calls++;
      last_observe = v;
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

  CounterImpl* counter(const std::string& n) { return counters_.count(n) ? counters_[n].get() : nullptr; }
  GaugeImpl* gauge(const std::string& n) { return gauges_.count(n) ? gauges_[n].get() : nullptr; }
  HistogramImpl* histogram(const std::string& n) {
    return histograms_.count(n) ? histograms_[n].get() : nullptr;
  }

 private:
  std::map<std::string, std::unique_ptr<CounterImpl>> counters_;
  std::map<std::string, std::unique_ptr<GaugeImpl>> gauges_;
  std::map<std::string, std::unique_ptr<HistogramImpl>> histograms_;
};

}  // namespace

TEST_CASE("collector: snapshot shape sorted byte-exact") {
  metrics::Collector c;

  // Histogram observation: 0.0005s → 500000 ns.
  metrics::HistogramOpts hopts;
  hopts.opts.name = "pine_redis_ping_duration_seconds";
  hopts.opts.label_names = {"name"};
  auto* ping = c.new_histogram(hopts);
  ping->with({"cache"})->observe(0.0005);

  // Gauge: pine_redis_up{cache} = 1.
  metrics::MetricOpts gopts;
  gopts.name = "pine_redis_up";
  gopts.label_names = {"name"};
  auto* up = c.new_gauge(gopts);
  up->with({"cache"})->set(1);

  // Names sort lexicographically; histogram name < up name.
  CHECK(c.to_json() ==
        "{\"pine_redis_ping_duration_seconds\":{\"cache\":{\"count\":1,\"sum_ns\":500000}},"
        "\"pine_redis_up\":{\"cache\":1}}");
}

TEST_CASE("collector: integer-valued gauge serializes without decimal") {
  metrics::Collector c;
  metrics::MetricOpts gopts;
  gopts.name = "pine_redis_pool_total_conns";
  gopts.label_names = {"name"};
  auto* g = c.new_gauge(gopts);
  g->with({"cache"})->set(8);
  CHECK(c.to_json() == "{\"pine_redis_pool_total_conns\":{\"cache\":8}}");
}

TEST_CASE("collector: unobserved metric still appears with empty object") {
  metrics::Collector c;
  metrics::MetricOpts gopts;
  gopts.name = "pine_redis_up";
  c.new_gauge(gopts);
  CHECK(c.to_json() == "{\"pine_redis_up\":{}}");
}

TEST_CASE("tee: fans out to all providers") {
  RecProvider a;
  RecProvider b;
  metrics::TeeProvider tee(std::vector<metrics::Provider*>{&a, &b});

  metrics::MetricOpts copts;
  copts.name = "c";
  auto* tc = tee.new_counter(copts);
  tc->with({"x"})->inc();

  REQUIRE(a.counter("c"));
  REQUIRE(b.counter("c"));
  CHECK(a.counter("c")->inc_calls == 1);
  CHECK(b.counter("c")->inc_calls == 1);
  CHECK(a.counter("c")->last_labels == std::vector<std::string>{"x"});
  CHECK(b.counter("c")->last_labels == std::vector<std::string>{"x"});

  metrics::MetricOpts gopts;
  gopts.name = "g";
  auto* tg = tee.new_gauge(gopts);
  tg->set(3.5);
  CHECK(a.gauge("g")->last_set == doctest::Approx(3.5));
  CHECK(b.gauge("g")->last_set == doctest::Approx(3.5));

  metrics::HistogramOpts hopts;
  hopts.opts.name = "h";
  auto* th = tee.new_histogram(hopts);
  th->observe(0.1);
  CHECK(a.histogram("h")->observe_calls == 1);
  CHECK(b.histogram("h")->observe_calls == 1);
}

TEST_CASE("tee: collector branch captures resource metrics") {
  RecProvider injected;
  metrics::Collector collector;
  metrics::TeeProvider tee(std::vector<metrics::Provider*>{&injected, &collector});

  metrics::MetricOpts gopts;
  gopts.name = "pine_redis_up";
  gopts.label_names = {"name"};
  auto* up = tee.new_gauge(gopts);
  up->with({"cache"})->set(1);

  // Reaches the injected provider (e.g. Prometheus) AND the collector.
  CHECK(injected.gauge("pine_redis_up")->last_set == doctest::Approx(1));
  CHECK(collector.to_json() == "{\"pine_redis_up\":{\"cache\":1}}");
}
