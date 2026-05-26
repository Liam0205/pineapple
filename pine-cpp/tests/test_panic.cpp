#include <doctest/doctest.h>

#include "pine/pine.hpp"

#include <stdexcept>

using namespace pine;

TEST_CASE("PanicError: carries operator name + value and formats message") {
    PanicError err("my_op", "boom");
    CHECK(err.operator_name() == "my_op");
    CHECK(err.value() == "boom");
    CHECK(std::string(err.what()) == "pine: panic in operator \"my_op\": boom");
}

TEST_CASE("PanicError: is a pine::Error") {
    PanicError err("op", "x");
    const Error* base = &err;
    CHECK(base != nullptr);
    const std::exception* stdex = &err;
    CHECK(std::string(stdex->what()).find("op") != std::string::npos);
}

namespace {

constexpr const char* kBadCopyConfig = R"({
  "_PINEAPPLE_VERSION": "0.8.0",
  "pipeline_config": {
    "operators": {
      "bad": {
        "type_name": "transform_copy",
        "direction": "common_to_common",
        "$metadata": {
          "common_input": ["a"],
          "common_output": ["b"]
        }
      }
    },
    "pipeline_map": {
      "stage": {"pipeline": ["bad"]}
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

TEST_CASE("dispatch_with_recovery: pine::ExecutionError passes through unwrapped") {
    Engine engine(load_config_from_json(kBadCopyConfig));
    Request req;
    // a is present but null → passes validation, ExecutionError "required field \"a\" is nil"
    req.common["a"] = JsonValue();
    bool caught_execution = false;
    bool caught_panic = false;
    try {
        engine.execute(req);
    } catch (const PanicError&) {
        caught_panic = true;
    } catch (const ExecutionError& e) {
        caught_execution = true;
        std::string msg = e.what();
        CHECK(msg.find("operator \"bad\"") != std::string::npos);
        CHECK(msg.find("required field") != std::string::npos);
    }
    CHECK(caught_execution);
    CHECK_FALSE(caught_panic);
}
