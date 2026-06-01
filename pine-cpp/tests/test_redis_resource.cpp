#include "pine/operator.hpp"
#include "pine/operator_input.hpp"
#include "pine/pine.hpp"

#include <doctest/doctest.h>

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

}  // namespace

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
