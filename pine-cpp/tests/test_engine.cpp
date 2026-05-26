#include <doctest/doctest.h>

#include "pine/pine.hpp"

using namespace pine;

namespace {

constexpr const char* kCopyConfig = R"({
  "_PINEAPPLE_VERSION": "0.8.0",
  "pipeline_config": {
    "operators": {
      "copy": {
        "type_name": "transform_copy",
        "direction": "common_to_common",
        "source": "src",
        "target": "dst",
        "$metadata": {
          "common_input": ["src"],
          "common_output": ["dst"]
        }
      }
    },
    "pipeline_map": {
      "stage": {"pipeline": ["copy"]}
    }
  },
  "pipeline_group": {
    "main": {"pipeline": ["stage"]}
  },
  "flow_contract": {
    "common_input": ["src"],
    "item_input": [],
    "common_output": ["dst"],
    "item_output": []
  }
})";

}  // namespace

TEST_CASE("Engine::execute: runs simple transform_copy") {
    Engine engine(load_config_from_json(kCopyConfig));
    Request req;
    req.common["src"] = JsonValue(std::string("hello"));
    auto result = engine.execute(req);
    REQUIRE(result.common.count("dst") == 1);
    CHECK(result.common.at("dst").as_string() == "hello");
}

TEST_CASE("Engine::execute_traced: produces trace entries") {
    Engine engine(load_config_from_json(kCopyConfig));
    Request req;
    req.common["src"] = JsonValue(std::string("v"));
    std::map<std::string, JsonValue> resources;
    auto traced = engine.execute_traced(req, resources);
    REQUIRE(traced.trace.size() == 1);
    CHECK(traced.trace[0].name == "copy");
    CHECK(traced.trace[0].skipped == false);
    CHECK(traced.result.common.at("dst").as_string() == "v");
}

TEST_CASE("Engine::render_dag: returns non-empty output for both formats") {
    Engine engine(load_config_from_json(kCopyConfig));
    auto dot = engine.render_dag("dot");
    CHECK(dot.find("digraph") != std::string::npos);
    auto mer = engine.render_dag("mermaid");
    CHECK(!mer.empty());
}

TEST_CASE("Engine::render_dag: rejects unknown format") {
    Engine engine(load_config_from_json(kCopyConfig));
    CHECK_THROWS(engine.render_dag("unknown"));
}

namespace {

constexpr const char* kCopyConfigWithLogPrefix = R"({
  "log_prefix": "[from-config] ",
  "pipeline_config": {
    "operators": {
      "copy": {
        "type_name": "transform_copy",
        "direction": "common_to_common",
        "source": "src",
        "target": "dst",
        "$metadata": {
          "common_input": ["src"],
          "common_output": ["dst"]
        }
      }
    },
    "pipeline_map": {
      "stage": {"pipeline": ["copy"]}
    }
  },
  "pipeline_group": {
    "main": {"pipeline": ["stage"]}
  },
  "flow_contract": {
    "common_input": ["src"],
    "item_input": [],
    "common_output": ["dst"],
    "item_output": []
  }
})";

}  // namespace

TEST_CASE("Config::log_prefix: parsed from root and exposed by Engine") {
    auto config = load_config_from_json(kCopyConfigWithLogPrefix);
    CHECK(config.log_prefix == "[from-config] ");
    Engine engine(std::move(config));
    CHECK(engine.log_prefix() == "[from-config] ");
}

TEST_CASE("EngineOptions::log_prefix: overrides Config.log_prefix") {
    auto config = load_config_from_json(kCopyConfigWithLogPrefix);
    EngineOptions options;
    options.log_prefix = std::string("[override] ");
    Engine engine(std::move(config), std::move(options));
    CHECK(engine.log_prefix() == "[override] ");
}

TEST_CASE("Engine::log_prefix: empty when unset on both Config and EngineOptions") {
    Engine engine(load_config_from_json(kCopyConfig));
    CHECK(engine.log_prefix() == "");
}
