// pineapple-cancel-probe: embedded-API cancellation parity probe (issue #217).
//
// Runs a fixed set of scenarios through Engine::execute with a cancelled or
// timing-out RequestContext and prints one line per scenario:
//   scenario=<name> error=<err or "nil"> common=[<sorted common keys>]
// cross-validate Section 22 asserts stdout is byte-identical to pine-go's
// pine-cancel-probe and pine-java's CancelProbe. Where a cancel must land
// mid-run, an operator issues it itself, so no timing race decides which
// nodes run. The context (and any deadline or cancel timer) is created only
// after the engine is built, so a slow engine construction cannot use up the
// deadline before the run starts.
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

// How run() builds the context, once the engine exists.
enum class Ctx {
  kPreCancelled,  // cancelled before the run
  kSelfCancel,    // CancelSelf cancels it from inside the run
  kDeadline,      // deadline that cuts bench_sleep short
  kCancelLater,   // cancelled from another thread during the run
};

// The deadline / cancel delay only has to outlast the start of the run, well
// under the 10 s sleep it cuts short.
constexpr auto kDelay = std::chrono::milliseconds(500);

void run(const std::string& name, const char* config, Ctx kind) {
  pine::Engine engine(pine::load_config_from_json(config));
  using pine::RequestContext;
  auto ctx = kind == Ctx::kDeadline ? RequestContext::with_timeout(RequestContext::background(), kDelay)
                                    : RequestContext::with_cancel(RequestContext::background());
  g_cancel_hook = [] {};
  std::thread canceller;
  if (kind == Ctx::kPreCancelled) {
    ctx.cancel();
  } else if (kind == Ctx::kSelfCancel) {
    g_cancel_hook = [&ctx] { ctx.cancel(); };
  } else if (kind == Ctx::kCancelLater) {
    canceller = std::thread([&ctx] {
      std::this_thread::sleep_for(kDelay);
      ctx.cancel();
    });
  }
  pine::TracedResult traced;
  std::string err = "nil";
  try {
    engine.execute_traced_into(pine::Request{}, ctx, &traced);
  } catch (const std::exception& e) {
    err = e.what();
  }
  if (canceller.joinable()) {
    canceller.join();
  }
  g_cancel_hook = [] {};
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
  run("pre_cancel", kChainConfig, Ctx::kPreCancelled);
  // op_a cancels mid-run and completes; op_b is skipped.
  run("cancel_mid_run", kChainConfig, Ctx::kSelfCancel);
  // Cancelled while the only node runs: nothing was skipped, so success.
  run("cancel_last_node", kSingleConfig, Ctx::kSelfCancel);
  // transform_bench_sleep stops at the deadline and fails itself.
  run("deadline_sleep", kSleepConfig, Ctx::kDeadline);
  // ... and on a cancel from another thread.
  run("cancel_sleep", kSleepConfig, Ctx::kCancelLater);
  return 0;
}
