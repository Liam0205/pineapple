#include "pine/operator.hpp"
#include "pine/operator_input.hpp"
#include "pine/pine.hpp"

#include <doctest/doctest.h>

#include <atomic>
#include <map>
#include <memory>
#include <string>

using namespace pine;

namespace {
using pine::Engine;  // Fix ambiguity with pine::Engine/Engine

// Package-level counters: operator instances are owned by the Engine and not
// otherwise reachable from the test, so they record close() calls here.
std::atomic<int64_t> g_close_calls{0};

// CloserMockOp records each close() call. Implementing Closer makes the engine
// call close() on retirement.
class CloserMockOp : public Operator, public Closer {
 public:
  void execute(const OperatorInput&, OperatorOutput&) override {
  }
  void close() override {
    g_close_calls.fetch_add(1, std::memory_order_relaxed);
  }
};

// NonCloserMockOp does NOT implement Closer; the engine must skip it.
class NonCloserMockOp : public Operator {
 public:
  void execute(const OperatorInput&, OperatorOutput&) override {
  }
};

void register_ops() {
  OperatorSchema schema1{
      .name = "test_closer_mock",
      .type = OpType::Recall,
      .description = "mock",
      .params = {},
  };
  register_operator_typed<CloserMockOp>(std::move(schema1));

  OperatorSchema schema2{
      .name = "test_non_closer_mock",
      .type = OpType::Recall,
      .description = "mock",
      .params = {},
  };
  register_operator_typed<NonCloserMockOp>(std::move(schema2));
}
const bool _reg = (register_ops(), true);

const char* kConfig = R"({
  "pipeline_config": {
    "operators": {
      "closer1": {
        "type_name": "test_closer_mock",
        "$metadata": {"common_input": [], "common_output": [], "item_input": [], "item_output": []}
      },
      "plain1": {
        "type_name": "test_non_closer_mock",
        "$metadata": {"common_input": [], "common_output": [], "item_input": [], "item_output": []}
      }
    }
  },
  "pipeline_group": {"main": {"pipeline": ["closer1", "plain1"]}},
  "flow_contract": {"common_input": [], "item_input": [], "common_output": [], "item_output": []}
})";

}  // namespace

TEST_CASE("Engine::close: invokes Closer on operators that implement it") {
  g_close_calls.store(0, std::memory_order_relaxed);
  auto cfg = load_config_from_json(kConfig);
  pine::Engine engine(std::move(cfg));

  // Two operators, only one implements Closer.
  engine.close();
  CHECK(g_close_calls.load(std::memory_order_relaxed) == 1);
}

TEST_CASE("Engine::close: is safe to call more than once") {
  g_close_calls.store(0, std::memory_order_relaxed);
  auto cfg = load_config_from_json(kConfig);
  pine::Engine engine(std::move(cfg));

  engine.close();
  engine.close();
  CHECK(g_close_calls.load(std::memory_order_relaxed) == 2);
}
