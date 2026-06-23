#include "pine/pine.hpp"

#include <doctest/doctest.h>

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
  "_PINEAPPLE_VERSION": "0.10.10",
  "pipeline_config": {
    "operators": {
      "bad": {
        "type_name": "transform_copy",
        "direction": "common_to_common",
        "strict_common": ["a"],
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
  req.common["a"] = Variant();
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

TEST_CASE("PanicError captures stack trace via std::stacktrace") {
  pine::PanicError p("opX", "boom");
  CHECK(std::string(p.what()).find("pine: panic in operator \"opX\": boom") != std::string::npos);
  // stack may be empty if the toolchain lacks std::stacktrace linkage,
  // but with PINE_HAS_STACKTRACE the field should be populated.
#if defined(PINE_HAS_STACKTRACE)
  CHECK(!p.stack().empty());
  std::string detailed = p.detailed_error();
  CHECK(detailed.find("stack trace:") != std::string::npos);
  CHECK(detailed.find(p.what()) != std::string::npos);
#else
  CHECK(p.stack().empty());
  CHECK(p.detailed_error() == std::string(p.what()));
#endif
}
