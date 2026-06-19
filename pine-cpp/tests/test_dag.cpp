#include "pine/pine.hpp"

#include <doctest/doctest.h>

using namespace pine;

namespace {

constexpr const char* kConfig = R"({
  "_PINEAPPLE_VERSION": "0.10.8",
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

// ---- 3-bucket DAG edge propagation (issue #74) ----------------------------

namespace {
bool has_pred(const Graph& g, const std::string& name, const std::string& pred) {
  int idx = g.name_to_index.at(name);
  int p_idx = g.name_to_index.at(pred);
  for (int p : g.nodes[idx].preds) {
    if (p == p_idx) {
      return true;
    }
  }
  return false;
}
}  // namespace

TEST_CASE("build_dag: RAW edge from common_input_skip bucket") {
  // op_a writes _if_branch into common; op_b declares it ONLY in
  // common_input_skip. The DAG must still wire the a->b RAW edge.
  const char* json = R"({
    "pipeline_config": {
      "operators": {
        "op_a": {
          "type_name": "transform_copy",
          "direction": "common_to_common",
          "$metadata": {"common_input": [], "common_output": ["_if_branch"]}
        },
        "op_b": {
          "type_name": "transform_copy",
          "direction": "common_to_common",
          "skip": ["_if_branch"],
          "$metadata": {
            "common_input": [],
            "common_input_skip": ["_if_branch"],
            "common_output": ["out"]
          }
        }
      },
      "pipeline_map": {"stage": {"pipeline": ["op_a", "op_b"]}}
    },
    "pipeline_group": {"main": {"pipeline": ["stage"]}}
  })";
  Config cfg = load_config_from_json(json);
  apply_registry_traits(cfg);
  auto expanded = expand_operator_sequence_with_subflows(cfg);
  auto graph = build_dag(cfg, expanded);
  CHECK(has_pred(graph, "op_b", "op_a"));
}

TEST_CASE("build_dag: RAW edge from common_input_template bucket") {
  // op_a writes tenant_id; op_b declares it ONLY in
  // common_input_template. DAG must still wire a->b.
  const char* json = R"({
    "pipeline_config": {
      "operators": {
        "op_a": {
          "type_name": "transform_copy",
          "direction": "common_to_common",
          "$metadata": {"common_input": [], "common_output": ["tenant_id"]}
        },
        "op_b": {
          "type_name": "transform_copy",
          "direction": "common_to_common",
          "$metadata": {
            "common_input": ["uid"],
            "common_input_template": ["tenant_id"],
            "common_output": ["out"]
          }
        }
      },
      "pipeline_map": {"stage": {"pipeline": ["op_a", "op_b"]}}
    },
    "pipeline_group": {"main": {"pipeline": ["stage"]}}
  })";
  Config cfg = load_config_from_json(json);
  apply_registry_traits(cfg);
  auto expanded = expand_operator_sequence_with_subflows(cfg);
  auto graph = build_dag(cfg, expanded);
  CHECK(has_pred(graph, "op_b", "op_a"));
}
