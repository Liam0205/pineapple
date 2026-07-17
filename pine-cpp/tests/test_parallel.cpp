#include "pine/pine.hpp"

#include <doctest/doctest.h>

using namespace pine;

namespace {

constexpr const char* kParallelConfig = R"({
  "_PINEAPPLE_VERSION": "0.10.13",
  "pipeline_config": {
    "operators": {
      "copy_tag": {
        "type_name": "transform_copy",
        "direction": "common_to_item",
        "data_parallel": 3,
        "$metadata": {
          "common_input": ["tag"],
          "common_output": [],
          "item_input": [],
          "item_output": ["tag"]
        }
      },
      "dispatch_scene": {
        "type_name": "transform_dispatch",
        "data_parallel": 2,
        "$metadata": {
          "common_input": ["scene"],
          "common_output": [],
          "item_input": [],
          "item_output": ["item_scene"]
        }
      }
    },
    "pipeline_map": {
      "stage": {"pipeline": ["copy_tag", "dispatch_scene"]}
    }
  },
  "pipeline_group": {
    "main": {"pipeline": ["stage"]}
  },
  "flow_contract": {
    "common_input": ["tag", "scene"],
    "item_input": ["id"],
    "common_output": [],
    "item_output": ["id", "tag", "item_scene"]
  }
})";

Request make_request(const std::vector<std::string>& ids, const std::string& tag, const std::string& scene) {
  Request req;
  req.common["tag"] = Variant(tag);
  req.common["scene"] = Variant(scene);
  for (const auto& id : ids) {
    Variant::object_t row;
    row["id"] = Variant(id);
    req.items.push_back(std::move(row));
  }
  return req;
}

}  // namespace

TEST_CASE("parallel_execute: 6 items across 3 shards preserves order and field writes") {
  Engine engine(load_config_from_json(kParallelConfig));
  auto result = engine.execute(make_request({"a", "b", "c", "d", "e", "f"}, "promo", "feed"));

  REQUIRE(result.items.size() == 6);
  std::vector<std::string> expected_ids = {"a", "b", "c", "d", "e", "f"};
  for (std::size_t i = 0; i < result.items.size(); ++i) {
    CHECK(result.items[i].at("id").as_string() == expected_ids[i]);
    CHECK(result.items[i].at("tag").as_string() == "promo");
    CHECK(result.items[i].at("item_scene").as_string() == "feed");
  }
}

TEST_CASE("parallel_execute: fewer items than parallelism degrades to N=total") {
  Engine engine(load_config_from_json(kParallelConfig));
  auto result = engine.execute(make_request({"x", "y"}, "vip", "search"));

  REQUIRE(result.items.size() == 2);
  CHECK(result.items[0].at("id").as_string() == "x");
  CHECK(result.items[0].at("tag").as_string() == "vip");
  CHECK(result.items[0].at("item_scene").as_string() == "search");
  CHECK(result.items[1].at("id").as_string() == "y");
}

TEST_CASE("parallel_execute: single item with parallelism>1 — degenerate shard") {
  Engine engine(load_config_from_json(kParallelConfig));
  auto result = engine.execute(make_request({"z"}, "solo", "home"));

  REQUIRE(result.items.size() == 1);
  CHECK(result.items[0].at("id").as_string() == "z");
  CHECK(result.items[0].at("tag").as_string() == "solo");
  CHECK(result.items[0].at("item_scene").as_string() == "home");
}

TEST_CASE("parallel_execute: zero items passthrough is safe") {
  Engine engine(load_config_from_json(kParallelConfig));
  auto result = engine.execute(make_request({}, "none", "empty"));
  CHECK(result.items.empty());
}

TEST_CASE("parallel_execute: traced execution still records one entry per operator") {
  Engine engine(load_config_from_json(kParallelConfig));
  std::map<std::string, Variant> resources;
  auto traced = engine.execute_traced(make_request({"a", "b", "c", "d", "e", "f"}, "x", "y"), resources);
  REQUIRE(traced.trace.size() == 2);
  CHECK(traced.trace[0].name == "copy_tag");
  CHECK(traced.trace[0].skipped == false);
  CHECK(traced.trace[1].name == "dispatch_scene");
}
