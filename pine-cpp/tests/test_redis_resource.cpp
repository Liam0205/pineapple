#include "pine/operator.hpp"
#include "pine/operator_input.hpp"
#include "pine/pine.hpp"
#include "pine/resource.hpp"

#include <doctest/doctest.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>

#include <algorithm>
#include <atomic>
#include <chrono>
#include <memory>
#include <string>
#include <thread>
#include <vector>

#include "redis/connection_pool.hpp"
#include "redis/redis_client.hpp"

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

const std::vector<std::string> kRedisMetrics = {
    "pine_redis_pool_total_conns",         "pine_redis_pool_idle_conns",
    "pine_redis_ping_duration_seconds",    "pine_redis_up",
    "pine_redis_command_duration_seconds", "pine_redis_command_total"};

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

// Locks the cross-engine-shared cascade-safety defaults on the registered
// schema. Mirrors pine-go's TestRedisOptionsFromParams_Defaults and pine-java's
// schemaExposesCascadeSafetyParams. Regression test for the 2026-06-22
// tipsy-recsys outage.
TEST_CASE("redis_connection schema exposes cascade-safety defaults") {
  resource::ResourceSchema schema;
  bool found = false;
  for (auto& s : resource::all_resource_schemas()) {
    if (s.name == "redis_connection") {
      schema = s;
      found = true;
      break;
    }
  }
  REQUIRE(found);

  REQUIRE(schema.params.count("dial_timeout_ms") == 1);
  CHECK(schema.params.at("dial_timeout_ms").default_value.as_number() == 2000.0);
  REQUIRE(schema.params.count("read_timeout_ms") == 1);
  CHECK(schema.params.at("read_timeout_ms").default_value.as_number() == 2000.0);
  REQUIRE(schema.params.count("write_timeout_ms") == 1);
  CHECK(schema.params.at("write_timeout_ms").default_value.as_number() == 2000.0);
  REQUIRE(schema.params.count("pool_timeout_ms") == 1);
  CHECK(schema.params.at("pool_timeout_ms").default_value.as_number() == 2000.0);
  REQUIRE(schema.params.count("pool_size") == 1);
  CHECK(schema.params.at("pool_size").default_value.as_number() == 0.0);

  CHECK(schema.params.at("addr").required == true);
  CHECK(schema.params.at("read_timeout_ms").required == false);
}

namespace {

// Bind a TCP listener on 127.0.0.1 + ephemeral port so the test can hand the
// resolved port back to the connecting client. Returns -1 + port=0 on failure
// so the test can FAIL with a useful message rather than hang.
int bind_ephemeral_listener(int* out_port) {
  int srv = socket(AF_INET, SOCK_STREAM, 0);
  if (srv < 0) {
    *out_port = 0;
    return -1;
  }
  int one = 1;
  setsockopt(srv, SOL_SOCKET, SO_REUSEADDR, &one, sizeof(one));
  sockaddr_in addr{};
  addr.sin_family = AF_INET;
  addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
  addr.sin_port = 0;  // kernel picks an ephemeral port
  if (bind(srv, reinterpret_cast<sockaddr*>(&addr), sizeof(addr)) != 0) {
    close(srv);
    *out_port = 0;
    return -1;
  }
  if (listen(srv, 1) != 0) {
    close(srv);
    *out_port = 0;
    return -1;
  }
  socklen_t alen = sizeof(addr);
  if (getsockname(srv, reinterpret_cast<sockaddr*>(&addr), &alen) != 0) {
    close(srv);
    *out_port = 0;
    return -1;
  }
  *out_port = ntohs(addr.sin_port);
  return srv;
}

}  // namespace

// Regression test for the operator-contract gap that the 2026-06-22 review
// surfaced: AUTH / SELECT failures during Client construction must NOT
// propagate as exceptions. transform_redis_get and friends default
// fail_on_error=false and rely on connected()=false to take the cache-miss
// degrade path; an exception escaping ConnectionPool::acquire would tear down
// the request instead, asymmetric with how the dial-timeout / connect-failure
// paths already silently fail.
TEST_CASE("redis Client AUTH/SELECT failure marks connected=false instead of throwing") {
  int port = 0;
  int srv = bind_ephemeral_listener(&port);
  REQUIRE_MESSAGE(srv >= 0, "could not bind ephemeral listener");

  // Server thread: accept the incoming connection then immediately close it.
  // The client will TCP-connect successfully (so dial_timeout doesn't fire),
  // then send "AUTH pw\r\n", and either fail at write() with EPIPE or fail
  // at expect_ok()'s read(). Either path used to throw std::runtime_error
  // out of the Client ctor; we assert it is now contained.
  std::atomic<bool> accepted{false};
  std::thread server_thread([&] {
    sockaddr_in client_addr{};
    socklen_t clen = sizeof(client_addr);
    int conn = accept(srv, reinterpret_cast<sockaddr*>(&client_addr), &clen);
    if (conn >= 0) {
      accepted = true;
      close(conn);
    }
  });

  // Construct with non-empty password so AUTH is attempted.
  redis::Client client("127.0.0.1", port, "any-password", 0);
  // The TCP layer succeeded but AUTH failed; connected() must reflect that
  // and the ctor must not have thrown.
  CHECK(client.connected() == false);

  server_thread.join();
  close(srv);
  CHECK(accepted == true);  // sanity: the server side did see the connect
}
