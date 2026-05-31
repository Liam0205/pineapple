#include "pine/pine.hpp"

#include <doctest/doctest.h>

using namespace pine;

namespace {

constexpr const char* kConfig = R"({
  "_PINEAPPLE_VERSION": "0.9.7",
  "pipeline_config": {
    "operators": {
      "a": {
        "type_name": "transform_copy",
        "direction": "common_to_common",
        "$metadata": {
          "common_input": ["x"],
          "common_output": ["y"]
        }
      },
      "b": {
        "type_name": "transform_copy",
        "direction": "common_to_common",
        "$metadata": {
          "common_input": ["y"],
          "common_output": ["z"]
        }
      }
    },
    "pipeline_map": {
      "stage": {"pipeline": ["a", "b"]}
    }
  },
  "pipeline_group": {
    "main": {"pipeline": ["stage"]}
  },
  "flow_contract": {
    "common_input": ["x"],
    "item_input": [],
    "common_output": ["z"],
    "item_output": []
  }
})";

}  // namespace

TEST_CASE("build_dag: produces ordered nodes with edges") {
  Config cfg = load_config_from_json(kConfig);
  apply_registry_traits(cfg);
  auto expanded = expand_operator_sequence_with_subflows(cfg);
  auto graph = build_dag(cfg, expanded);

  REQUIRE(graph.nodes.size() == 2);
  REQUIRE(graph.name_to_index.count("a") == 1);
  REQUIRE(graph.name_to_index.count("b") == 1);

  int a_idx = graph.name_to_index.at("a");
  int b_idx = graph.name_to_index.at("b");
  // a -> b edge must exist.
  bool has_edge = false;
  for (int succ : graph.nodes[a_idx].succs) {
    if (succ == b_idx) {
      has_edge = true;
      break;
    }
  }
  CHECK(has_edge);
  CHECK(graph.nodes[b_idx].preds.size() >= 1);
}

TEST_CASE("render_dot: emits digraph header") {
  Config cfg = load_config_from_json(kConfig);
  apply_registry_traits(cfg);
  auto expanded = expand_operator_sequence_with_subflows(cfg);
  auto graph = build_dag(cfg, expanded);

  auto dot = render_dot(graph);
  CHECK(dot.find("digraph") != std::string::npos);
  CHECK(dot.find("\"a\"") != std::string::npos);
  CHECK(dot.find("\"b\"") != std::string::npos);
}

TEST_CASE("render_mermaid: emits graph header") {
  Config cfg = load_config_from_json(kConfig);
  apply_registry_traits(cfg);
  auto expanded = expand_operator_sequence_with_subflows(cfg);
  auto graph = build_dag(cfg, expanded);

  auto mer = render_mermaid(graph);
  CHECK((mer.find("graph") != std::string::npos || mer.find("flowchart") != std::string::npos));
  CHECK(mer.find("a") != std::string::npos);
  CHECK(mer.find("b") != std::string::npos);
}
