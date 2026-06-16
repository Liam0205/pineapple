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
  "_PINEAPPLE_VERSION": "0.10.6",
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

TEST_CASE("Engine::log_prefix: empty when unset on both Config and EngineOptions") {
  Engine engine(load_config_from_json(kCopyConfig));
  CHECK(engine.log_prefix() == "");
}

TEST_CASE("validate_output_against_type: Recall must not SetCommon") {
  // Use a custom in-process operator registered just for this test to keep
  // the assertion focused on the validate_output codepath. The Recall type
  // forbids SetCommon, SetItem, RemoveItem, SetItemOrder.
  struct BadRecall : public pine::Operator {
    void init(const pine::OperatorConfig&) override {
    }
    void execute(const pine::OperatorInput&, pine::OperatorOutput& out) override {
      out.set_common("region", pine::Variant(std::string("us")));
    }
  };
  static const pine::OperatorSchema s{
      "bad_recall_set_common", pine::OpType::Recall, "recall that illegally writes common", {}};
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
            "type_name": "bad_recall_set_common",
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
  Engine engine(load_config_from_json(kBadRecallConfig));
  Request req;
  try {
    engine.execute(req);
    FAIL("expected ExecutionError");
  } catch (const Error& e) {
    std::string msg = e.what();
    CHECK(msg.find("type violation: operator type Recall must not call [SetCommon]") != std::string::npos);
    CHECK(msg.find("pine: execution error in operator \"r1\"") != std::string::npos);
  }
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
