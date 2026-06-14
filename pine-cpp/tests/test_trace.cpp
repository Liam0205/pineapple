#include "pine/pine.hpp"

#include <doctest/doctest.h>

#include <chrono>

using namespace pine;

namespace {

constexpr const char* kBaseConfigNoDebug = R"({
  "_PINEAPPLE_VERSION": "0.10.2",
  "pipeline_config": {
    "operators": {
      "op": {
        "type_name": "transform_copy",
        "direction": "common_to_common",
        "$metadata": {
          "common_input": ["x"],
          "common_output": ["y"]
        }
      }
    },
    "pipeline_map": {
      "stage": {"pipeline": ["op"]}
    }
  },
  "pipeline_group": {
    "main": {"pipeline": ["stage"]}
  },
  "flow_contract": {
    "common_input": ["x"],
    "item_input": [],
    "common_output": ["y"],
    "item_output": []
  }
})";

constexpr const char* kPerOpDebugConfig = R"({
  "_PINEAPPLE_VERSION": "0.9.0",
  "pipeline_config": {
    "operators": {
      "op": {
        "type_name": "transform_copy",
        "direction": "common_to_common",
        "debug": true,
        "$metadata": {
          "common_input": ["x"],
          "common_output": ["y"]
        }
      }
    },
    "pipeline_map": {
      "stage": {"pipeline": ["op"]}
    }
  },
  "pipeline_group": {
    "main": {"pipeline": ["stage"]}
  },
  "flow_contract": {
    "common_input": ["x"],
    "item_input": [],
    "common_output": ["y"],
    "item_output": []
  }
})";

constexpr const char* kRootDebugConfig = R"({
  "_PINEAPPLE_VERSION": "0.9.0",
  "debug": true,
  "pipeline_config": {
    "operators": {
      "op": {
        "type_name": "transform_copy",
        "direction": "common_to_common",
        "$metadata": {
          "common_input": ["x"],
          "common_output": ["y"]
        }
      }
    },
    "pipeline_map": {
      "stage": {"pipeline": ["op"]}
    }
  },
  "pipeline_group": {
    "main": {"pipeline": ["stage"]}
  },
  "flow_contract": {
    "common_input": ["x"],
    "item_input": [],
    "common_output": ["y"],
    "item_output": []
  }
})";

Request make_request() {
  Request req;
  req.common["x"] = Variant(1.0);
  return req;
}

}  // namespace

TEST_CASE("OpTrace: debug=false yields no snapshots, but start_time_us is populated") {
  Engine engine(load_config_from_json(kBaseConfigNoDebug));
  auto traced = engine.execute_traced(make_request(), {});
  REQUIRE(traced.trace.size() == 1);
  CHECK(traced.trace[0].name == "op");
  CHECK_FALSE(traced.trace[0].has_input_snapshot);
  CHECK_FALSE(traced.trace[0].has_output_snapshot);
  CHECK(traced.trace[0].start_time_us > 0);
}

TEST_CASE("OpTrace: per-op debug=true populates InputSnapshot/OutputSnapshot") {
  Engine engine(load_config_from_json(kPerOpDebugConfig));
  auto traced = engine.execute_traced(make_request(), {});
  REQUIRE(traced.trace.size() == 1);
  CHECK(traced.trace[0].has_input_snapshot);
  CHECK(traced.trace[0].has_output_snapshot);

  REQUIRE(traced.trace[0].input_snapshot.is_object());
  const auto* common = traced.trace[0].input_snapshot.find("common");
  REQUIRE(common != nullptr);
  REQUIRE(common->is_object());
  REQUIRE(common->find("x") != nullptr);
  CHECK(common->find("x")->as_number() == 1.0);

  REQUIRE(traced.trace[0].output_snapshot.is_object());
  const auto* cw = traced.trace[0].output_snapshot.find("common_writes");
  REQUIRE(cw != nullptr);
  REQUIRE(cw->is_object());
  REQUIRE(cw->find("y") != nullptr);
  CHECK(cw->find("y")->as_number() == 1.0);
}

TEST_CASE("OpTrace: root-level debug=true enables snapshots for all ops") {
  Engine engine(load_config_from_json(kRootDebugConfig));
  auto traced = engine.execute_traced(make_request(), {});
  REQUIRE(traced.trace.size() == 1);
  CHECK(traced.trace[0].has_input_snapshot);
  CHECK(traced.trace[0].has_output_snapshot);
}

TEST_CASE("EngineOptions::debug=true overrides per-op default false") {
  EngineOptions opts;
  opts.debug = true;
  Engine engine(load_config_from_json(kBaseConfigNoDebug), opts);
  auto traced = engine.execute_traced(make_request(), {});
  REQUIRE(traced.trace.size() == 1);
  CHECK(traced.trace[0].has_input_snapshot);
  CHECK(traced.trace[0].has_output_snapshot);
}

TEST_CASE("EngineOptions::debug=false suppresses root-level debug=true") {
  EngineOptions opts;
  opts.debug = false;
  Engine engine(load_config_from_json(kRootDebugConfig), opts);
  auto traced = engine.execute_traced(make_request(), {});
  REQUIRE(traced.trace.size() == 1);
  CHECK_FALSE(traced.trace[0].has_input_snapshot);
  CHECK_FALSE(traced.trace[0].has_output_snapshot);
}

TEST_CASE("OpTrace::start_time_us is close to wall-clock time") {
  using namespace std::chrono;
  int64_t before = duration_cast<microseconds>(system_clock::now().time_since_epoch()).count();
  Engine engine(load_config_from_json(kBaseConfigNoDebug));
  auto traced = engine.execute_traced(make_request(), {});
  int64_t after = duration_cast<microseconds>(system_clock::now().time_since_epoch()).count();
  REQUIRE(traced.trace.size() == 1);
  CHECK(traced.trace[0].start_time_us >= before);
  CHECK(traced.trace[0].start_time_us <= after);
}
