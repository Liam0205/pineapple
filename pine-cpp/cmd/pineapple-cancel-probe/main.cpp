// pineapple-cancel-probe: embedded-API cancellation parity probe (issue #217).
//
// Runs a fixed set of scenarios through Engine::execute with a cancelled or
// timing-out RequestContext and prints one line per scenario:
//   scenario=<name> error=<err or "nil"> common=[<sorted common keys>]
// cross-validate Section 22 asserts stdout is byte-identical to pine-go's
// pine-cancel-probe and pine-java's CancelProbe. Where a cancel must land
// mid-run, an operator issues it itself, so no timing race decides which
// nodes run.
#include "pine/operator.hpp"
#include "pine/operator_input.hpp"
#include "pine/pine.hpp"

#include <chrono>
#include <exception>
#include <functional>
#include <iostream>
#include <string>
#include <thread>
#include <vector>

namespace {

// Set per scenario; CancelSelf calls it during execute.
std::function<void()> g_cancel_hook = [] {};

// Cancels the request's context from inside the run, ignores it, writes x.
struct CancelSelf : public pine::Operator {
  void execute(const pine::RequestContext& /*ctx*/, const pine::OperatorInput& /*input*/,
               pine::OperatorOutput& out) override {
    g_cancel_hook();
    out.set_common("x", pine::Variant(true));
  }
};

// Writes common y from x.
struct CopyXToY : public pine::Operator {
  void execute(const pine::RequestContext& /*ctx*/, const pine::OperatorInput& input,
               pine::OperatorOutput& out) override {
    out.set_common("y", input.common("x"));
  }
};

constexpr const char* kChainConfig = R"({
  "pipeline_config": {
    "operators": {
      "op_a": {"type_name": "probe217_cancel_self",
               "$metadata": {"common_input": [], "common_output": ["x"], "item_input": [], "item_output": []}},
      "op_b": {"type_name": "probe217_copy_x_y",
               "$metadata": {"common_input": ["x"], "common_output": ["y"], "item_input": [], "item_output": []}}
    },
    "pipeline_map": {"s": {"pipeline": ["op_a", "op_b"]}}
  },
  "pipeline_group": {"main": {"pipeline": ["s"]}},
  "flow_contract": {"common_input": [], "common_output": ["x", "y"], "item_input": [], "item_output": []}
})";

constexpr const char* kSingleConfig = R"({
  "pipeline_config": {
    "operators": {
      "op_a": {"type_name": "probe217_cancel_self",
               "$metadata": {"common_input": [], "common_output": ["x"], "item_input": [], "item_output": []}}
    },
    "pipeline_map": {"s": {"pipeline": ["op_a"]}}
  },
  "pipeline_group": {"main": {"pipeline": ["s"]}},
  "flow_contract": {"common_input": [], "common_output": ["x"], "item_input": [], "item_output": []}
})";

constexpr const char* kSleepConfig = R"({
  "pipeline_config": {
    "operators": {
      "sleep": {"type_name": "transform_bench_sleep", "delay_ms": 10000,
                "$metadata": {"common_input": [], "common_output": [], "item_input": [], "item_output": []}}
    },
    "pipeline_map": {"s": {"pipeline": ["sleep"]}}
  },
  "pipeline_group": {"main": {"pipeline": ["s"]}},
  "flow_contract": {"common_input": [], "common_output": [], "item_input": [], "item_output": []}
})";

void run(const std::string& name, const char* config, const pine::RequestContext& ctx) {
  pine::Engine engine(pine::load_config_from_json(config));
  pine::TracedResult traced;
  std::string err = "nil";
  try {
    engine.execute_traced_into(pine::Request{}, ctx, &traced);
  } catch (const std::exception& e) {
    err = e.what();
  }
  engine.close();
  // Result::common is an ordered map, so keys come out sorted.
  std::string keys;
  for (const auto& [k, v] : traced.result.common) {
    if (v.is_null()) {
      continue;
    }
    if (!keys.empty()) {
      keys += ",";
    }
    keys += k;
  }
  std::cout << "scenario=" << name << " error=" << err << " common=[" << keys << "]\n";
}

}  // namespace

int main() {
  pine::register_operator_typed<CancelSelf>({"probe217_cancel_self",
                                             pine::OpType::Transform,
                                             "probe: cancels the request context, then writes x",
                                             {},
                                             {}});
  pine::register_operator_typed<CopyXToY>(
      {"probe217_copy_x_y", pine::OpType::Transform, "probe: copies common x to y", {}, {}});

  // A context cancelled before the run: nothing runs.
  {
    auto ctx = pine::RequestContext::with_cancel(pine::RequestContext::background());
    ctx.cancel();
    g_cancel_hook = [] {};
    run("pre_cancel", kChainConfig, ctx);
  }
  // op_a cancels mid-run and completes; op_b is skipped.
  {
    auto ctx = pine::RequestContext::with_cancel(pine::RequestContext::background());
    g_cancel_hook = [&ctx] { ctx.cancel(); };
    run("cancel_mid_run", kChainConfig, ctx);
  }
  // Cancelled while the only node runs: nothing was skipped, so success.
  {
    auto ctx = pine::RequestContext::with_cancel(pine::RequestContext::background());
    g_cancel_hook = [&ctx] { ctx.cancel(); };
    run("cancel_last_node", kSingleConfig, ctx);
  }
  g_cancel_hook = [] {};
  // transform_bench_sleep stops at the deadline and fails itself.
  {
    auto ctx =
        pine::RequestContext::with_timeout(pine::RequestContext::background(), std::chrono::milliseconds(50));
    run("deadline_sleep", kSleepConfig, ctx);
  }
  // ... and on a cancel from another thread.
  {
    auto ctx = pine::RequestContext::with_cancel(pine::RequestContext::background());
    std::thread canceller([&ctx] {
      std::this_thread::sleep_for(std::chrono::milliseconds(50));
      ctx.cancel();
    });
    run("cancel_sleep", kSleepConfig, ctx);
    canceller.join();
  }
  return 0;
}
