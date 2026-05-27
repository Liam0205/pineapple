#include "pine/metrics.hpp"

#include <doctest/doctest.h>

#include <atomic>
#include <map>
#include <memory>
#include <string>
#include <vector>

#include "server/server.hpp"

namespace {

class RecorderProvider : public pine::metrics::Provider {
 public:
  struct CounterImpl : pine::metrics::Counter {
    std::vector<std::vector<std::string>> labels;
    pine::metrics::Counter* with(const std::vector<std::string>& v) override {
      labels.push_back(v);
      return this;
    }
    void inc() override {
    }
  };
  struct GaugeImpl : pine::metrics::Gauge {
    pine::metrics::Gauge* with(const std::vector<std::string>&) override {
      return this;
    }
    void set(double) override {
    }
    void add(double) override {
    }
  };
  struct HistogramImpl : pine::metrics::Histogram {
    std::vector<std::vector<std::string>> labels;
    std::vector<double> values;
    pine::metrics::Histogram* with(const std::vector<std::string>& v) override {
      labels.push_back(v);
      return this;
    }
    void observe(double v) override {
      values.push_back(v);
    }
  };
  pine::metrics::Counter* new_counter(const pine::metrics::MetricOpts& o) override {
    return counters_.emplace(o.name, std::make_unique<CounterImpl>()).first->second.get();
  }
  pine::metrics::Gauge* new_gauge(const pine::metrics::MetricOpts& o) override {
    return gauges_.emplace(o.name, std::make_unique<GaugeImpl>()).first->second.get();
  }
  pine::metrics::Histogram* new_histogram(const pine::metrics::HistogramOpts& o) override {
    return histograms_.emplace(o.opts.name, std::make_unique<HistogramImpl>()).first->second.get();
  }
  CounterImpl* counter(const std::string& n) {
    return counters_.count(n) ? counters_[n].get() : nullptr;
  }
  HistogramImpl* histogram(const std::string& n) {
    return histograms_.count(n) ? histograms_[n].get() : nullptr;
  }

 private:
  std::map<std::string, std::unique_ptr<CounterImpl>> counters_;
  std::map<std::string, std::unique_ptr<GaugeImpl>> gauges_;
  std::map<std::string, std::unique_ptr<HistogramImpl>> histograms_;
};

}  // namespace

TEST_CASE("http_metrics: records requests_total + duration with bucketed status/path") {
  using namespace pine::server;
  RecorderProvider provider;
  Middleware mw = http_metrics_middleware(&provider);

  MiddlewareContext ctx;
  ctx.method = "POST";
  ctx.path = "/execute";
  ctx.normalized_path = "/execute";
  ctx.status = 200;

  mw(ctx, []() { /* inner handler no-op; status stays 200 */ });

  auto* total = provider.counter("pine_http_requests_total");
  REQUIRE(total);
  REQUIRE(total->labels.size() == 1);
  CHECK(total->labels[0] == std::vector<std::string>{"POST", "/execute", "2xx"});

  auto* dur = provider.histogram("pine_http_request_duration_seconds");
  REQUIRE(dur);
  REQUIRE(dur->labels.size() == 1);
  CHECK(dur->labels[0] == std::vector<std::string>{"POST", "/execute"});
  CHECK(dur->values.size() == 1);
  CHECK(dur->values[0] >= 0.0);
}

TEST_CASE("http_metrics: status_bucket maps 4xx/5xx") {
  using namespace pine::server;
  RecorderProvider provider;
  Middleware mw = http_metrics_middleware(&provider);

  {
    MiddlewareContext ctx;
    ctx.method = "GET";
    ctx.normalized_path = "/health";
    mw(ctx, [&]() { ctx.status = 404; });
  }
  {
    MiddlewareContext ctx;
    ctx.method = "POST";
    ctx.normalized_path = "/execute";
    mw(ctx, [&]() { ctx.status = 500; });
  }

  auto* total = provider.counter("pine_http_requests_total");
  REQUIRE(total);
  REQUIRE(total->labels.size() == 2);
  CHECK(total->labels[0][2] == "4xx");
  CHECK(total->labels[1][2] == "5xx");
}
