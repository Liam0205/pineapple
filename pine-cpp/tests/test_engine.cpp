#include "pine/operator.hpp"
#include "pine/operator_input.hpp"
#include "pine/pine.hpp"

#include <doctest/doctest.h>

#include <atomic>
#include <thread>
#include <vector>

using namespace pine;

namespace {

constexpr const char* kCopyConfig = R"({
  "_PINEAPPLE_VERSION": "0.10.16",
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

TEST_CASE("Engine::execute: runs simple transform_copy") {
  Engine engine(load_config_from_json(kCopyConfig));
  Request req;
  req.common["src"] = Variant(std::string("hello"));
  auto result = engine.execute(req);
  REQUIRE(result.common.count("dst") == 1);
  CHECK(result.common.at("dst").as_string() == "hello");
}

TEST_CASE("Engine::execute_traced: produces trace entries") {
  Engine engine(load_config_from_json(kCopyConfig));
  Request req;
  req.common["src"] = Variant(std::string("v"));
  std::map<std::string, Variant> resources;
  auto traced = engine.execute_traced(req, resources);
  REQUIRE(traced.trace.size() == 1);
  CHECK(traced.trace[0].name == "copy");
  CHECK(traced.trace[0].skipped == false);
  CHECK(traced.result.common.at("dst").as_string() == "v");
}

TEST_CASE("Engine::render_dag: returns non-empty output for both formats") {
  Engine engine(load_config_from_json(kCopyConfig));
  auto dot = engine.render_dag("dot");
  CHECK(dot.find("digraph") != std::string::npos);
  auto mer = engine.render_dag("mermaid");
  CHECK(!mer.empty());
}

TEST_CASE("Engine::render_dag: rejects unknown format") {
  Engine engine(load_config_from_json(kCopyConfig));
  CHECK_THROWS(engine.render_dag("unknown"));
}

namespace {

constexpr const char* kCopyConfigWithLogPrefix = R"({
  "log_prefix": "[from-config] ",
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

TEST_CASE("Config::log_prefix: parsed from root and exposed by Engine") {
  auto config = load_config_from_json(kCopyConfigWithLogPrefix);
  CHECK(config.log_prefix == "[from-config] ");
  Engine engine(std::move(config));
  CHECK(engine.log_prefix() == "[from-config] ");
}

TEST_CASE("EngineOptions::log_prefix: overrides Config.log_prefix") {
  auto config = load_config_from_json(kCopyConfigWithLogPrefix);
  EngineOptions options;
  options.log_prefix = std::string("[override] ");
  Engine engine(std::move(config), std::move(options));
  CHECK(engine.log_prefix() == "[override] ");
}

// An explicit empty-string option must override a non-empty Config prefix
// (std::optional tri-state, cross-runtime parity with Go/Java).
TEST_CASE("EngineOptions::log_prefix: explicit empty string overrides Config") {
  auto config = load_config_from_json(kCopyConfigWithLogPrefix);
  EngineOptions options;
  options.log_prefix = std::string("");
  Engine engine(std::move(config), std::move(options));
  CHECK(engine.log_prefix() == "");
}

TEST_CASE("Engine::log_prefix: empty when unset on both Config and EngineOptions") {
  Engine engine(load_config_from_json(kCopyConfig));
  CHECK(engine.log_prefix() == "");
}

// Issue #172: log_prefix is engine-scoped. Multiple engines in one process
// each keep their own prefix; construction order must not matter (the Go
// implementation regressed to first-engine-wins via a global sync.Once).
TEST_CASE("Engine::log_prefix: per-engine isolation across two engines") {
  Engine first(load_config_from_json(kCopyConfigWithLogPrefix));
  EngineOptions options;
  options.log_prefix = std::string("[second] ");
  Engine second(load_config_from_json(kCopyConfig), std::move(options));
  CHECK(first.log_prefix() == "[from-config] ");
  CHECK(second.log_prefix() == "[second] ");
}

TEST_CASE("validate_output_against_type: Recall may SetCommon (downstream consumable)") {
  // Recall is allowed to write common (e.g. a recall-generated request id
  // that downstream operators consume). The common write participates in
  // normal DAG hazard tracking. Recall still must NOT SetItem/RemoveItem/
  // SetItemOrder — see the next test case.
  struct CommonWritingRecall : public pine::Operator {
    void init(const pine::OperatorConfig&) override {
    }
    void execute(const pine::OperatorInput&, pine::OperatorOutput& out) override {
      out.set_common("region", pine::Variant(std::string("us")));
    }
  };
  static const pine::OperatorSchema s{
      "recall_set_common", pine::OpType::Recall, "recall that writes common", {}};
  static bool registered = false;
  if (!registered) {
    pine::register_operator_typed<CommonWritingRecall>(s);
    registered = true;
  }

  static const char* kConfig = R"({
      "_PINEAPPLE_VERSION": "0.9.0",
      "pipeline_config": {
        "operators": {
          "r1": {
            "type_name": "recall_set_common",
            "$metadata": {"common_output": ["region"]}
          }
        },
        "pipeline_map": {
          "stage": {"pipeline": ["r1"]}
        }
      },
      "pipeline_group": {
        "main": {"pipeline": ["stage"]}
      },
      "flow_contract": {
        "common_input": [],
        "item_input": [],
        "common_output": ["region"],
        "item_output": []
      }
    })";
  Engine engine(load_config_from_json(kConfig));
  Request req;
  auto resp = engine.execute(req);  // must not throw
  // The recall-written common field is projected into the response common.
  REQUIRE(resp.common.count("region") == 1);
  CHECK(resp.common.at("region").as_string() == "us");
}

TEST_CASE("validate_output_against_type: Recall must not SetItem") {
  // Guards the still-forbidden item-mutation half of the Recall contract:
  // relaxing SetCommon must not accidentally relax SetItem.
  struct BadRecall : public pine::Operator {
    void init(const pine::OperatorConfig&) override {
    }
    void execute(const pine::OperatorInput&, pine::OperatorOutput& out) override {
      out.set_item(0, "score", pine::Variant(1.0));
    }
  };
  static const pine::OperatorSchema s{
      "bad_recall_set_item", pine::OpType::Recall, "recall that illegally writes an item", {}};
  static bool registered = false;
  if (!registered) {
    pine::register_operator_typed<BadRecall>(s);
    registered = true;
  }

  static const char* kBadRecallConfig = R"({
      "_PINEAPPLE_VERSION": "0.9.0",
      "pipeline_config": {
        "operators": {
          "r1": {
            "type_name": "bad_recall_set_item",
            "$metadata": {"item_output": ["score"]}
          }
        },
        "pipeline_map": {
          "stage": {"pipeline": ["r1"]}
        }
      },
      "pipeline_group": {
        "main": {"pipeline": ["stage"]}
      },
      "flow_contract": {
        "common_input": [],
        "item_input": [],
        "common_output": [],
        "item_output": ["score"]
      }
    })";
  Engine engine(load_config_from_json(kBadRecallConfig));
  Request req;
  try {
    engine.execute(req);
    FAIL("expected ExecutionError");
  } catch (const Error& e) {
    std::string msg = e.what();
    CHECK(msg.find("type violation: operator type Recall must not call [SetItem]") != std::string::npos);
    CHECK(msg.find("pine: execution error in operator \"r1\"") != std::string::npos);
  }
}

// ---------------------------------------------------------------------------
// validate_declared_outputs (issue #205): every written field must be in
// $metadata's common_output / item_output. Mirrors pine-go
// types.ValidateDeclaredOutputs tests and pine-java OutputContractTest so the
// runtimes enforce the same matrix with byte-identical messages. The helper
// lives in an anonymous namespace in engine.cpp, so each case drives it
// end-to-end through Engine::execute with a probe operator.
// ---------------------------------------------------------------------------
namespace {

// Builds a single-operator pipeline whose $metadata declares exactly
// `common_output` / `item_output` (JSON array literals, e.g. R"(["a"])").
std::string single_op_config(const std::string& type_name, const std::string& common_output,
                             const std::string& item_output) {
  return std::string(R"({
      "_PINEAPPLE_VERSION": "0.9.0",
      "pipeline_config": {
        "operators": {
          "p": {
            "type_name": ")") +
         type_name + R"(",
            "$metadata": {"common_output": )" +
         common_output + R"(, "item_output": )" + item_output + R"(}
          }
        },
        "pipeline_map": {"stage": {"pipeline": ["p"]}}
      },
      "pipeline_group": {"main": {"pipeline": ["stage"]}},
      "flow_contract": {
        "common_input": [],
        "item_input": [],
        "common_output": )" +
         common_output + R"(,
        "item_output": )" +
         item_output + R"(
      }
    })";
}

// Runs the pipeline and returns the full error text, or "" when it succeeded.
std::string execute_and_capture(const std::string& config, Request req) {
  Engine engine(load_config_from_json(config));
  try {
    engine.execute(req);
    return "";
  } catch (const Error& e) {
    return e.what();
  }
}

// One probe per write path. Each writes a fixed field name; the test decides
// whether that name is declared by varying the $metadata it emits.
struct ProbeSetCommon : public pine::Operator {
  void init(const pine::OperatorConfig&) override {
  }
  void execute(const pine::OperatorInput&, pine::OperatorOutput& out) override {
    out.set_common("probe_common", pine::Variant(1.0));
  }
};
struct ProbeSetItem : public pine::Operator {
  void init(const pine::OperatorConfig&) override {
  }
  void execute(const pine::OperatorInput& in, pine::OperatorOutput& out) override {
    for (std::size_t i = 0; i < in.item_count(); ++i) {
      out.set_item(static_cast<int>(i), "probe_item", pine::Variant(1.0));
    }
  }
};
struct ProbeColumnWrite : public pine::Operator {
  void init(const pine::OperatorConfig&) override {
  }
  void execute(const pine::OperatorInput& in, pine::OperatorOutput& out) override {
    out.set_item_column_double("probe_col", std::vector<double>(in.item_count(), 1.0));
  }
};
struct ProbeAddItem : public pine::Operator, public pine::AdditiveWritesRowSet {
  void init(const pine::OperatorConfig&) override {
  }
  void execute(const pine::OperatorInput&, pine::OperatorOutput& out) override {
    pine::Variant::object_t row;
    row["undeclared_added"] = pine::Variant(1.0);
    out.add_item(std::move(row));
  }
};
// Writes on both channels and several item paths at once, to pin
// common-first reporting plus item de-duplication and byte ordering.
struct ProbeMixed : public pine::Operator {
  void init(const pine::OperatorConfig&) override {
  }
  void execute(const pine::OperatorInput& in, pine::OperatorOutput& out) override {
    out.set_common("bad_common", pine::Variant(1.0));
    for (std::size_t i = 0; i < in.item_count(); ++i) {
      out.set_item(static_cast<int>(i), "b", pine::Variant(1.0));
      out.set_item(static_cast<int>(i), "a", pine::Variant(1.0));
    }
    out.set_item_column_double("b", std::vector<double>(in.item_count(), 1.0));
  }
};
struct ProbeItemOnlyMixed : public pine::Operator {
  void init(const pine::OperatorConfig&) override {
  }
  void execute(const pine::OperatorInput& in, pine::OperatorOutput& out) override {
    for (std::size_t i = 0; i < in.item_count(); ++i) {
      out.set_item(static_cast<int>(i), "b", pine::Variant(1.0));
      out.set_item(static_cast<int>(i), "a", pine::Variant(1.0));
    }
    out.set_item_column_double("b", std::vector<double>(in.item_count(), 1.0));
    // U+FFFD (EF BF BD) sorts before U+10000 (F0 90 80 80) in byte order —
    // the order Go's sort.Strings reports; a UTF-16 comparison would flip it.
    out.set_item(0, "\xF0\x90\x80\x80", pine::Variant(1.0));
    out.set_item(0, "\xEF\xBF\xBD", pine::Variant(1.0));
  }
};

void register_output_contract_probes() {
  static bool registered = false;
  if (registered) {
    return;
  }
  static const pine::OperatorSchema s_common{
      "probe205_set_common", pine::OpType::Transform, "test-only: writes common probe_common", {}};
  static const pine::OperatorSchema s_item{
      "probe205_set_item", pine::OpType::Transform, "test-only: writes item probe_item", {}};
  static const pine::OperatorSchema s_col{
      "probe205_column_write", pine::OpType::Transform, "test-only: whole-column probe_col", {}};
  static const pine::OperatorSchema s_add{
      "probe205_add_item", pine::OpType::Recall, "test-only: adds item with undeclared_added", {}};
  static const pine::OperatorSchema s_mixed{
      "probe205_mixed", pine::OpType::Transform, "test-only: violates both channels", {}};
  static const pine::OperatorSchema s_item_mixed{
      "probe205_item_mixed", pine::OpType::Transform, "test-only: several undeclared item paths", {}};
  pine::register_operator_typed<ProbeSetCommon>(s_common);
  pine::register_operator_typed<ProbeSetItem>(s_item);
  pine::register_operator_typed<ProbeColumnWrite>(s_col);
  pine::register_operator_typed<ProbeAddItem>(s_add);
  pine::register_operator_typed<ProbeMixed>(s_mixed);
  pine::register_operator_typed<ProbeItemOnlyMixed>(s_item_mixed);
  registered = true;
}

Request one_item_request() {
  Request req;
  pine::Variant::object_t row;
  row["id"] = pine::Variant(std::string("a"));
  req.items.push_back(std::move(row));
  return req;
}

}  // namespace

TEST_CASE("validate_declared_outputs: set_common undeclared is rejected, declared accepted") {
  register_output_contract_probes();
  CHECK(execute_and_capture(single_op_config("probe205_set_common", R"(["declared"])", "[]"), Request{}) ==
        "pine: execution error in operator \"p\": output contract violation: operator wrote undeclared "
        "common output field(s) [probe_common]");
  CHECK(execute_and_capture(single_op_config("probe205_set_common", R"(["probe_common"])", "[]"),
                            Request{}) == "");
}

TEST_CASE("validate_declared_outputs: set_item undeclared is rejected, declared accepted") {
  register_output_contract_probes();
  CHECK(execute_and_capture(single_op_config("probe205_set_item", "[]", R"(["declared"])"),
                            one_item_request()) ==
        "pine: execution error in operator \"p\": output contract violation: operator wrote undeclared "
        "item output field(s) [probe_item]");
  CHECK(execute_and_capture(single_op_config("probe205_set_item", "[]", R"(["probe_item"])"),
                            one_item_request()) == "");
}

TEST_CASE("validate_declared_outputs: whole-column write undeclared is rejected, declared accepted") {
  register_output_contract_probes();
  CHECK(execute_and_capture(single_op_config("probe205_column_write", "[]", R"(["declared"])"),
                            one_item_request()) ==
        "pine: execution error in operator \"p\": output contract violation: operator wrote undeclared "
        "item output field(s) [probe_col]");
  CHECK(execute_and_capture(single_op_config("probe205_column_write", "[]", R"(["probe_col"])"),
                            one_item_request()) == "");
}

TEST_CASE("validate_declared_outputs: add_item undeclared is rejected, declared accepted") {
  register_output_contract_probes();
  // Full user-visible message, byte-identical to pine-go / pine-java for the
  // same probe shape (operator "p_add" in the Go probe; "p" here).
  CHECK(execute_and_capture(single_op_config("probe205_add_item", "[]", R"(["declared"])"), Request{}) ==
        "pine: execution error in operator \"p\": output contract violation: operator wrote undeclared "
        "item output field(s) [undeclared_added]");
  // `_source` is injected by apply_output AFTER this check, so a recall whose
  // added items are fully declared passes without any _source exemption.
  CHECK(execute_and_capture(single_op_config("probe205_add_item", "[]", R"(["undeclared_added"])"),
                            Request{}) == "");
}

TEST_CASE("validate_declared_outputs: common channel is reported alone and first") {
  register_output_contract_probes();
  // Both channels violate; item names must not leak into the message.
  CHECK(execute_and_capture(single_op_config("probe205_mixed", "[]", "[]"), one_item_request()) ==
        "pine: execution error in operator \"p\": output contract violation: operator wrote undeclared "
        "common output field(s) [bad_common]");
}

TEST_CASE("validate_declared_outputs: item names deduped across paths, sorted by bytes") {
  register_output_contract_probes();
  // "b" arrives via two set_item calls and one column write → once. The list
  // is Go's %v of a sorted []string: brackets, single space, no quotes.
  CHECK(execute_and_capture(single_op_config("probe205_item_mixed", "[]", "[]"), one_item_request()) ==
        "pine: execution error in operator \"p\": output contract violation: operator wrote undeclared "
        "item output field(s) [a b \xEF\xBF\xBD \xF0\x90\x80\x80]");
}

TEST_CASE("Engine::execute honors external stop_token") {
  static const char* kCfg = R"({
      "_PINEAPPLE_VERSION": "0.9.0",
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
        "pipeline_map": {"stage": {"pipeline": ["copy"]}}
      },
      "pipeline_group": {"main": {"pipeline": ["stage"]}},
      "flow_contract": {
        "common_input": ["src"],
        "item_input": [],
        "common_output": ["dst"],
        "item_output": []
      }
    })";
  Engine engine(load_config_from_json(kCfg));
  Request req;
  req.common["src"] = Variant(std::string("v"));
  std::stop_source src;
  src.request_stop();  // pre-cancelled
  static const std::map<std::string, Variant> empty_res;
  // Pre-cancelled token: run_dag should see stop_requested at every wait
  // and either return early or finish the trivial DAG. Either way the
  // call must not deadlock and not throw spuriously.
  auto result = engine.execute(req, empty_res, src.get_token());
  // The simple linear DAG may have completed before observing cancel —
  // both outcomes are valid; the API contract is "no deadlock, no UB".
  CHECK(true);
}

TEST_CASE("Engine::execute external cancel mid-flight on multi-node DAG (R10-4)") {
  // Register a slow operator that sleeps N ms in execute. Registers once
  // per process; if already registered (e.g. previous test invocation in
  // the same binary) the existing schema is reused.
  struct SlowOp : public pine::Operator {
    void init(const pine::OperatorConfig&) override {
    }
    void execute(const pine::OperatorInput&, pine::OperatorOutput&) override {
      // Long enough that the watcher thread can deliver the cancel.
      std::this_thread::sleep_for(std::chrono::milliseconds(500));
    }
  };
  static const pine::OperatorSchema s{"r10_slow_op",
                                      pine::OpType::Transform,
                                      "test operator: sleeps 500 ms to validate mid-flight cancel",
                                      {}};
  static bool registered = false;
  if (!registered) {
    pine::register_operator_typed<SlowOp>(s);
    registered = true;
  }

  // 3-node linear DAG (Transform only — Transform allows zero writes,
  // so SlowOp's empty OperatorOutput passes ValidateOutput).
  static const char* kCfg = R"({
      "_PINEAPPLE_VERSION": "0.9.0",
      "pipeline_config": {
        "operators": {
          "s1": {"type_name": "r10_slow_op", "$metadata": {"item_input": [], "item_output": [], "common_input": [], "common_output": []}},
          "s2": {"type_name": "r10_slow_op", "$metadata": {"item_input": [], "item_output": [], "common_input": [], "common_output": []}},
          "s3": {"type_name": "r10_slow_op", "$metadata": {"item_input": [], "item_output": [], "common_input": [], "common_output": []}}
        },
        "pipeline_map": {"stage": {"pipeline": ["s1", "s2", "s3"]}}
      },
      "pipeline_group": {"main": {"pipeline": ["stage"]}}
    })";
  Engine engine(load_config_from_json(kCfg));
  Request req;
  static const std::map<std::string, Variant> empty_res;

  std::stop_source src;
  auto cancel_token = src.get_token();

  // Fire the cancel 200 ms in — well inside s1's sleep but before s1/s2/s3 finish.
  std::thread canceller([&src]() {
    std::this_thread::sleep_for(std::chrono::milliseconds(200));
    src.request_stop();
  });

  auto t0 = std::chrono::steady_clock::now();
  // execute should observe cancel and return before the full 3 × 500 ms.
  // It may or may not throw — the contract is "no deadlock, return soon".
  try {
    engine.execute(req, empty_res, cancel_token);
  } catch (const Error&) {
    // ok — engine can rethrow a cancel-shaped error
  }
  auto elapsed = std::chrono::steady_clock::now() - t0;
  canceller.join();

  // Without cancel, this would take ~1500 ms (3 × 500 ms). With cancel
  // mid-s1, total time should be at most ~750 ms (current s1 finishes
  // + a few ms cleanup). Use 1.2 s as a generous bound to keep CI
  // noise-tolerant while still proving cancel took effect.
  auto ms = std::chrono::duration_cast<std::chrono::milliseconds>(elapsed).count();
  INFO("elapsed=" << ms << "ms");
  CHECK(ms < 1200);
}

TEST_CASE("Engine::execute is safe to call concurrently from many threads on the same Plan") {
  // Direct doctest (TSan-runnable) coverage for shared `const Engine` use.
  // Server-layer integration tests already pound execute() concurrently via
  // the HTTP path, but those are coarse: a TSan failure surfaces only by
  // way of a serialized stack trace inside the request handler, which
  // hides the engine-internal race. This case calls Engine::execute
  // directly from N threads with the same Plan and per-thread Request
  // payloads, asserting both: (1) every thread observes its own input
  // round-tripped through transform_copy → its own output (no cross-talk
  // through any shared mutable state inside Engine), and (2) the total
  // call survives the run with no crash, no hang.
  Engine engine(load_config_from_json(kCopyConfig));

  constexpr int kThreads = 16;
  constexpr int kPerThread = 32;
  std::atomic<int> mismatches{0};
  std::atomic<int> exceptions{0};

  std::vector<std::thread> ts;
  ts.reserve(kThreads);
  for (int t = 0; t < kThreads; ++t) {
    ts.emplace_back([&, t]() {
      for (int i = 0; i < kPerThread; ++i) {
        // Each (thread, iteration) pair gets a unique input string so
        // any cross-pollination between concurrent execute() calls is
        // detectable on output.
        std::string payload = "t" + std::to_string(t) + "-i" + std::to_string(i);
        Request req;
        req.common["src"] = Variant(payload);
        try {
          auto result = engine.execute(req);
          auto it = result.common.find("dst");
          if (it == result.common.end() || it->second.as_string() != payload) {
            mismatches.fetch_add(1, std::memory_order_relaxed);
          }
        } catch (...) {
          exceptions.fetch_add(1, std::memory_order_relaxed);
        }
      }
    });
  }
  for (auto& th : ts) {
    th.join();
  }
  CHECK(mismatches.load() == 0);
  CHECK(exceptions.load() == 0);
  // peak_concurrency() is a cumulative atomic across all execute() calls.
  // With kThreads concurrent runs we expect it to be > 0 (the scheduler
  // observed at least one in-flight node), independent of the precise
  // peak — a stronger bound would be flaky on under-provisioned CI.
  CHECK(engine.peak_concurrency() > 0);
}

TEST_CASE("Engine: cancel mid-flight then immediately destroy is safe (M13)") {
  // Audit M13 — the dual isolated pools (dag_pool_ + shard_pool_) live as
  // unique_ptr members on Engine. When the user fires a stop_token and
  // destroys the Engine right after execute() returns, the pool dtors
  // must drain workers cleanly without racing on tasks that observed
  // cancel and bailed via the fast `propagate_and_signal` path
  // (engine.cpp:828-832). Existing R10-4 case proves cancel works for
  // ONE execute, but never destroys Engine — the dtor path is exercised
  // only by static-storage cleanup at process exit, when no in-flight
  // task ever existed. This test loops cancel→destroy so a sanitizer
  // build (TSan/ASan) can flag any UAF or torn pool teardown.
  struct SlowOp : public pine::Operator {
    void init(const pine::OperatorConfig&) override {
    }
    void execute(const pine::OperatorInput&, pine::OperatorOutput&) override {
      std::this_thread::sleep_for(std::chrono::milliseconds(50));
    }
  };
  static const pine::OperatorSchema s_m13{
      "m13_slow_op", pine::OpType::Transform, "test operator: 50 ms sleep for cancel→destroy loop", {}};
  static bool registered = false;
  if (!registered) {
    pine::register_operator_typed<SlowOp>(s_m13);
    registered = true;
  }

  // 4-node linear DAG so cancel-mid-flight has work still queued in
  // dag_pool when execute() returns. Total without cancel ~200 ms.
  static const char* kCfg = R"({
      "_PINEAPPLE_VERSION": "0.9.0",
      "pipeline_config": {
        "operators": {
          "n1": {"type_name": "m13_slow_op", "$metadata": {"item_input": [], "item_output": [], "common_input": [], "common_output": []}},
          "n2": {"type_name": "m13_slow_op", "$metadata": {"item_input": [], "item_output": [], "common_input": [], "common_output": []}},
          "n3": {"type_name": "m13_slow_op", "$metadata": {"item_input": [], "item_output": [], "common_input": [], "common_output": []}},
          "n4": {"type_name": "m13_slow_op", "$metadata": {"item_input": [], "item_output": [], "common_input": [], "common_output": []}}
        },
        "pipeline_map": {"stage": {"pipeline": ["n1", "n2", "n3", "n4"]}}
      },
      "pipeline_group": {"main": {"pipeline": ["stage"]}}
    })";

  static const std::map<std::string, Variant> empty_res;
  constexpr int kIterations = 12;

  std::atomic<int> survived{0};
  for (int it = 0; it < kIterations; ++it) {
    // Engine in unique_ptr so we can `reset()` to trigger destructor at
    // a precise moment, immediately after execute() returns. RAII alone
    // would also work but `reset()` makes the cancel→destroy ordering
    // explicit at the call site.
    auto engine = std::make_unique<Engine>(load_config_from_json(kCfg));
    Request req;

    std::stop_source src;
    auto tok = src.get_token();

    std::thread canceller([&src]() {
      std::this_thread::sleep_for(std::chrono::milliseconds(20));
      src.request_stop();
    });

    try {
      engine->execute(req, empty_res, tok);
    } catch (const Error&) {
      // Cancel may surface as a thrown Error — accepted.
    }

    // Critical sequence: destroy Engine *while* worker loops in both
    // dag_pool_ and shard_pool_ are still draining their queues. The
    // ThreadPool dtor (thread_pool.cpp:16) flips stopping_=true, notifies,
    // and joins each worker. Any read-after-free of the captured `&` lambda
    // state (cancel_source / fatal_err / done_cv) would land here.
    engine.reset();
    canceller.join();
    survived.fetch_add(1, std::memory_order_relaxed);
  }
  CHECK(survived.load() == kIterations);
}
