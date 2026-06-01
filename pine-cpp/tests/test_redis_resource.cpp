#include "pine/operator.hpp"
#include "pine/operator_input.hpp"
#include "pine/pine.hpp"

#include <doctest/doctest.h>

#include <algorithm>
#include <memory>
#include <string>
#include <vector>

#include "redis/connection_pool.hpp"

using namespace pine;

namespace {

// transform_redis_get pipeline that references a redis_connection resource by
// name but is run with NO resource_provider injected — exercising the borrow
// degrade path (no provider → cache miss, no connection attempt).
constexpr const char* kRedisGetConfig = R"({
  "pipeline_config": {
    "operators": {
      "get": {
        "type_name": "transform_redis_get",
        "resource_name": "redis_conn",
        "key_prefix": "k:",
        "data_type": "string",
        "$metadata": {
          "common_input": ["user_id"],
          "common_output": ["value", "cache_hit"]
        }
      }
    },
    "pipeline_map": {
      "stage": {"pipeline": ["get"]}
    }
  },
  "pipeline_group": {
    "main": {"pipeline": ["stage"]}
  },
  "flow_contract": {
    "common_input": ["user_id"],
    "item_input": [],
    "common_output": ["value", "cache_hit"],
    "item_output": []
  }
})";

constexpr const char* kRedisSetConfig = R"({
  "pipeline_config": {
    "operators": {
      "set": {
        "type_name": "transform_redis_set",
        "resource_name": "redis_conn",
        "key_prefix": "k:",
        "data_type": "string",
        "$metadata": {
          "common_input": ["user_id", "value"],
          "common_output": []
        }
      }
    },
    "pipeline_map": {
      "stage": {"pipeline": ["set"]}
    }
  },
  "pipeline_group": {
    "main": {"pipeline": ["stage"]}
  },
  "flow_contract": {
    "common_input": ["user_id", "value"],
    "item_input": [],
    "common_output": [],
    "item_output": []
  }
})";

// RecordingProvider records the name of every metric created through it, so a
// test can assert which metrics RedisConnResource registered. The returned
// metric objects are inert (observations are discarded). Mirrors pine-go's
// metricRecorder and pine-java's RecordingProvider.
class RecordingProvider : public metrics::Provider {
 public:
  std::vector<std::string> names;

  metrics::Counter* new_counter(const metrics::MetricOpts& opts) override {
    names.push_back(opts.name);
    counters_.push_back(std::make_unique<NopCounter>());
    return counters_.back().get();
  }
  metrics::Gauge* new_gauge(const metrics::MetricOpts& opts) override {
    names.push_back(opts.name);
    gauges_.push_back(std::make_unique<NopGauge>());
    return gauges_.back().get();
  }
  metrics::Histogram* new_histogram(const metrics::HistogramOpts& opts) override {
    names.push_back(opts.opts.name);
    histograms_.push_back(std::make_unique<NopHistogram>());
    return histograms_.back().get();
  }

  bool has(const std::string& name) const {
    return std::find(names.begin(), names.end(), name) != names.end();
  }

 private:
  struct NopCounter : metrics::Counter {
    Counter* with(const std::vector<std::string>&) override {
      return this;
    }
    void inc() override {
    }
  };
  struct NopGauge : metrics::Gauge {
    Gauge* with(const std::vector<std::string>&) override {
      return this;
    }
    void set(double) override {
    }
    void add(double) override {
    }
  };
  struct NopHistogram : metrics::Histogram {
    Histogram* with(const std::vector<std::string>&) override {
      return this;
    }
    void observe(double) override {
    }
  };
  std::vector<std::unique_ptr<NopCounter>> counters_;
  std::vector<std::unique_ptr<NopGauge>> gauges_;
  std::vector<std::unique_ptr<NopHistogram>> histograms_;
};

const std::vector<std::string> kRedisMetrics = {"pine_redis_pool_total_conns", "pine_redis_pool_idle_conns",
                                                "pine_redis_ping_duration_seconds", "pine_redis_up"};

}  // namespace

TEST_CASE("redis metrics gate: empty metrics_name registers no metrics") {
  RecordingProvider rec;
  // Points at a non-listening port; construction never connects. The probe
  // thread is not started when metrics are disabled, so no metric is created.
  redis::RedisConnResource r("127.0.0.1", 1, "", 0, "", &rec);
  for (const auto& name : kRedisMetrics) {
    CHECK_FALSE(rec.has(name));
  }
}

TEST_CASE("redis metrics gate: non-empty metrics_name registers all metrics") {
  RecordingProvider rec;
  redis::RedisConnResource r("127.0.0.1", 1, "", 0, "cache", &rec);
  for (const auto& name : kRedisMetrics) {
    CHECK(rec.has(name));
  }
}

TEST_CASE("redis metrics gate: null provider registers no metrics") {
  redis::RedisConnResource r("127.0.0.1", 1, "", 0, "cache", nullptr);
  // Construction with metrics_name set but a null provider must not crash and
  // must not start a probe thread; nothing to assert beyond clean teardown.
  CHECK(r.host() == "127.0.0.1");
}

TEST_CASE("redis_get degrades to cache miss when no resource provider is injected") {
  Engine engine(load_config_from_json(kRedisGetConfig));
  Request req;
  req.common["user_id"] = Variant(std::string("42"));

  auto result = engine.execute(req);

  // Borrow returns null (no provider), so the operator reports a cache miss
  // and never attempts a connection — mirrors pine-go's borrowRedis ok=false.
  REQUIRE(result.common.count("cache_hit") == 1);
  CHECK(result.common.at("cache_hit").as_bool() == false);
  CHECK(result.common.count("value") == 0);
}

TEST_CASE("redis_set is a no-op when no resource provider is injected") {
  Engine engine(load_config_from_json(kRedisSetConfig));
  Request req;
  req.common["user_id"] = Variant(std::string("42"));
  req.common["value"] = Variant(std::string("v"));

  // Null borrow → silent no-op; must not throw and must not connect.
  CHECK_NOTHROW(engine.execute(req));
}
