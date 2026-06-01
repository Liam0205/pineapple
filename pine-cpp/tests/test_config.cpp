#include "pine/pine.hpp"

#include <doctest/doctest.h>

#include <algorithm>

using namespace pine;

namespace {

constexpr const char* kSimpleConfig = R"({
  "_PINEAPPLE_VERSION": "0.9.9",
  "pipeline_config": {
    "operators": {
      "first": {
        "type_name": "transform_copy",
        "direction": "common_to_common",
        "$metadata": {
          "common_input": ["a"],
          "common_output": ["b"]
        }
      },
      "second": {
        "type_name": "filter_truncate",
        "top_n": 5,
        "$metadata": {
          "common_input": ["b"],
          "common_output": [],
          "item_input": [],
          "item_output": []
        }
      }
    },
    "pipeline_map": {
      "stage": {"pipeline": ["first", "second"]}
    }
  },
  "pipeline_group": {
    "main": {"pipeline": ["stage"]}
  },
  "flow_contract": {
    "common_input": ["a"],
    "item_input": [],
    "common_output": ["b"],
    "item_output": []
  }
})";

}  // namespace

TEST_CASE("load_config_from_json: parses pipeline operators") {
  Config cfg = load_config_from_json(kSimpleConfig);
  REQUIRE(cfg.operators.size() == 2);
  CHECK(cfg.operators.count("first") == 1);
  CHECK(cfg.operators.count("second") == 1);
  CHECK(cfg.operators.at("first").type_name == "transform_copy");
  CHECK(cfg.operators.at("second").type_name == "filter_truncate");
}

TEST_CASE("apply_registry_traits: fills operator_type from registry") {
  Config cfg = load_config_from_json(kSimpleConfig);
  apply_registry_traits(cfg);
  // filter_truncate is a "filter" in the registry → mutates row set.
  CHECK(cfg.operators.at("second").operator_type == "filter");
}

TEST_CASE("expand_operator_sequence_with_subflows: produces topological order") {
  Config cfg = load_config_from_json(kSimpleConfig);
  auto expanded = expand_operator_sequence_with_subflows(cfg);
  REQUIRE(expanded.sequence.size() == 2);
  // "first" must precede "second" because second consumes b which first produces.
  auto first_idx =
      std::find(expanded.sequence.begin(), expanded.sequence.end(), "first") - expanded.sequence.begin();
  auto second_idx =
      std::find(expanded.sequence.begin(), expanded.sequence.end(), "second") - expanded.sequence.begin();
  CHECK(first_idx < second_idx);
}

TEST_CASE("load_config_from_json: invalid JSON throws ConfigError") {
  CHECK_THROWS_AS(load_config_from_json("{not valid json"), ConfigError);
}

namespace {

constexpr const char* kConfigWithMetadata = R"({
  "_PINEAPPLE_VERSION": "1.2.3",
  "_PINEAPPLE_CREATE_TIME": "2026-05-22T10:00:00Z",
  "resource_config": {
    "user_profile": {
      "type": "static_table",
      "interval": 60,
      "params": {"path": "/tmp/profile.json"}
    },
    "no_interval_default": {
      "type": "noop",
      "params": {}
    }
  },
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

TEST_CASE("load_config_from_json: parses _PINEAPPLE_VERSION and _PINEAPPLE_CREATE_TIME") {
  Config cfg = load_config_from_json(kConfigWithMetadata);
  CHECK(cfg.pineapple_version == "1.2.3");
  CHECK(cfg.pineapple_create_time == "2026-05-22T10:00:00Z");
}

TEST_CASE("load_config_from_json: parses resource_config entries") {
  Config cfg = load_config_from_json(kConfigWithMetadata);
  REQUIRE(cfg.resource_config.size() == 2);
  const auto& up = cfg.resource_config.at("user_profile");
  CHECK(up.type == "static_table");
  CHECK(up.interval == 60);
  REQUIRE(up.params.is_object());
  CHECK(up.params.as_object().at("path").as_string() == "/tmp/profile.json");
  const auto& nd = cfg.resource_config.at("no_interval_default");
  CHECK(nd.type == "noop");
  CHECK(nd.interval == 0);
}

TEST_CASE("load_config_from_json: omits resource_config when absent") {
  Config cfg = load_config_from_json(kSimpleConfig);
  CHECK(cfg.resource_config.empty());
  CHECK(cfg.pineapple_create_time == "");
}
