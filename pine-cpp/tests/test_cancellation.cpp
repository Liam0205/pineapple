// Issue #217: request-scoped cancellation. Mirrors pine-go's
// scheduler_test.go cancellation cases and pine-java's CancellationTest.

#include "pine/column_frame.hpp"
#include "pine/error_chain.hpp"
#include "pine/metrics_collector.hpp"
#include "pine/operator.hpp"
#include "pine/operator_input.hpp"
#include "pine/pine.hpp"

#include <doctest/doctest.h>

#include <atomic>
#include <chrono>
#include <latch>
#include <map>
#include <memory>
#include <string>
#include <thread>
#include <vector>

#include "redis/connection_pool.hpp"

using namespace pine;
using namespace std::chrono_literals;

namespace {

// ---- RequestContext primitives ---------------------------------------------

TEST_CASE("RequestContext: background is never done") {
  auto bg = RequestContext::background();
  CHECK_FALSE(bg.cancelled());
  CHECK_FALSE(bg.err().has_value());
  CHECK_FALSE(bg.deadline().has_value());
  CHECK_FALSE(bg.stop_token().stop_possible());
  CHECK_NOTHROW(bg.throw_if_cancelled());
  CHECK_FALSE(bg.wait_for(1ms));
}

TEST_CASE("RequestContext: cancel propagates down, not up") {
  auto parent = RequestContext::with_cancel(RequestContext::background());
  auto child = RequestContext::with_cancel(parent);
  auto grandchild = RequestContext::with_value(child, &child, std::any(1));
  auto sibling = RequestContext::with_cancel(parent);

  child.cancel();
  CHECK(child.cancelled());
  CHECK(grandchild.cancelled());
  CHECK_FALSE(parent.cancelled());
  CHECK_FALSE(sibling.cancelled());

  parent.cancel();
  CHECK(sibling.cancelled());
  REQUIRE(sibling.err().has_value());
  CHECK(std::string(sibling.err()->what()) == "context canceled");
  CHECK_FALSE(sibling.err()->deadline_exceeded());
  CHECK(sibling.stop_token().stop_requested());
}

TEST_CASE("RequestContext: a child of a cancelled parent starts cancelled") {
  auto parent = RequestContext::with_cancel(RequestContext::background());
  parent.cancel();
  auto child = RequestContext::with_cancel(parent);
  CHECK(child.cancelled());
  CHECK_THROWS_AS(child.throw_if_cancelled(), ContextError);
}

TEST_CASE("RequestContext: deadline cancels with deadline exceeded") {
  auto ctx = RequestContext::with_timeout(RequestContext::background(), 20ms);
  REQUIRE(ctx.deadline().has_value());
  CHECK(ctx.wait_for(5s));
  REQUIRE(ctx.err().has_value());
  CHECK(std::string(ctx.err()->what()) == "context deadline exceeded");
  CHECK(ctx.err()->deadline_exceeded());
}

TEST_CASE("RequestContext: a past deadline is done immediately") {
  auto ctx = RequestContext::with_timeout(RequestContext::background(), 0ms);
  CHECK(ctx.cancelled());
  CHECK(ctx.err()->deadline_exceeded());
}

TEST_CASE("RequestContext: an earlier parent deadline wins") {
  auto parent = RequestContext::with_timeout(RequestContext::background(), 20ms);
  auto child = RequestContext::with_timeout(parent, 1h);
  CHECK(child.deadline() == parent.deadline());
  CHECK(child.wait_for(5s));
  CHECK(child.err()->deadline_exceeded());
}

TEST_CASE("RequestContext: cancelling before the deadline reports canceled") {
  auto ctx = RequestContext::with_timeout(RequestContext::background(), 1h);
  ctx.cancel();
  CHECK_FALSE(ctx.err()->deadline_exceeded());
}

TEST_CASE("RequestContext: values and resources resolve to the nearest binding") {
  static const int kKeyA = 0;
  static const int kKeyB = 0;
  std::map<std::string, Variant> res1{{"r", Variant(1.0)}};
  std::map<std::string, Variant> res2{{"r", Variant(2.0)}};
  auto base = RequestContext::with_value(RequestContext::background(), &kKeyA, std::any(std::string("a1")));
  auto with_res = RequestContext::with_resources(base, &res1);
  auto shadow = RequestContext::with_value(with_res, &kKeyA, std::any(std::string("a2")));
  auto inner = RequestContext::with_resources(shadow, &res2);

  REQUIRE(inner.value(&kKeyA) != nullptr);
  CHECK(std::any_cast<std::string>(*inner.value(&kKeyA)) == "a2");
  CHECK(std::any_cast<std::string>(*with_res.value(&kKeyA)) == "a1");
  CHECK(inner.value(&kKeyB) == nullptr);
  CHECK(inner.resources() == &res2);
  CHECK(shadow.resources() == &res1);
  CHECK(base.resources() == nullptr);
  CHECK(inner.resource_provider() == nullptr);
  CHECK_THROWS_AS(RequestContext::with_value(base, nullptr, std::any()), std::invalid_argument);
}

TEST_CASE("RequestContext: wait_for wakes on cancel from another thread") {
  auto ctx = RequestContext::with_cancel(RequestContext::background());
  std::thread t([&ctx] {
    std::this_thread::sleep_for(20ms);
    ctx.cancel();
  });
  auto t0 = std::chrono::steady_clock::now();
  CHECK(ctx.wait_for(10s));
  CHECK(std::chrono::steady_clock::now() - t0 < 5s);
  t.join();
}

TEST_CASE("RequestContext: Cancelable cancels on destruction, a sliced copy observes it") {
  RequestContext observer;
  {
    auto ctx = RequestContext::with_cancel(RequestContext::background());
    observer = ctx;
    CHECK_FALSE(observer.cancelled());
  }
  CHECK(observer.cancelled());
}

TEST_CASE("RequestContext: many short-lived children of a long-lived parent") {
  // A server root context outlives every request; children must unlink on
  // cancel so this stays cheap (and sanitizer-clean).
  auto root = RequestContext::with_cancel(RequestContext::background());
  for (int i = 0; i < 1000; ++i) {
    auto req = RequestContext::with_timeout(root, 1h);
    auto shard = RequestContext::with_cancel(req);
  }
  root.cancel();
  CHECK(root.cancelled());
}

// ---- engine ----------------------------------------------------------------

// Per-test latches for BlockIgnoringCtx (registration is process-wide).
std::atomic<std::latch*> g_started{nullptr};
std::atomic<std::latch*> g_release{nullptr};

struct BlockIgnoringCtx : public Operator {
  void execute(const RequestContext& /*ctx*/, const OperatorInput&, OperatorOutput& out) override {
    g_started.load()->count_down();
    g_release.load()->wait();
    out.set_common("x", Variant(true));
  }
};

struct CopyXToY : public Operator {
  void execute(const RequestContext& /*ctx*/, const OperatorInput& input, OperatorOutput& out) override {
    out.set_common("y", input.common("x"));
  }
};

void register_probes() {
  static bool registered = false;
  if (registered) {
    return;
  }
  register_operator_typed<BlockIgnoringCtx>({"test217_block_ignoring_ctx",
                                             OpType::Transform,
                                             "test-only: blocks until released, ignoring ctx",
                                             {}});
  register_operator_typed<CopyXToY>(
      {"test217_copy_x_y", OpType::Transform, "test-only: copies common x to y", {}});
  registered = true;
}

constexpr const char* kChainConfig = R"({
  "pipeline_config": {
    "operators": {
      "op_a": {"type_name": "test217_block_ignoring_ctx",
               "$metadata": {"common_input": [], "common_output": ["x"], "item_input": [], "item_output": []}},
      "op_b": {"type_name": "test217_copy_x_y",
               "$metadata": {"common_input": ["x"], "common_output": ["y"], "item_input": [], "item_output": []}}
    },
    "pipeline_map": {"s": {"pipeline": ["op_a", "op_b"]}}
  },
  "pipeline_group": {"main": {"pipeline": ["s"]}},
  "flow_contract": {"common_input": [], "common_output": ["x", "y"], "item_input": [], "item_output": []}
})";

struct ChainRun {
  TracedResult traced;
  std::string error;
  bool cancelled_error = false;
  bool deadline_exceeded = false;
};

// Runs op_a -> op_b under ctx: once op_a started, calls trigger, then
// releases op_a only after ctx is done, so op_b always finds it cancelled.
template <typename Trigger>
ChainRun run_chain(const Engine& engine, const RequestContext& ctx, Trigger trigger) {
  std::latch started(1);
  std::latch release(1);
  g_started = &started;
  g_release = &release;
  std::thread helper([&] {
    started.wait();
    trigger();
    ctx.wait_for(10s);
    release.count_down();
  });
  ChainRun run;
  try {
    engine.execute_traced_into(Request{}, ctx, &run.traced);
  } catch (const CancelledError& e) {
    run.error = e.what();
    run.cancelled_error = true;
    run.deadline_exceeded = e.deadline_exceeded();
    CHECK(error_is<ContextError>(e));
  } catch (const std::exception& e) {
    run.error = e.what();
  }
  helper.join();
  return run;
}

TEST_CASE("engine: cancel mid-run reports CancelledError and keeps completed writes") {
  register_probes();
  metrics::Collector collector;
  EngineOptions opts;
  opts.metrics_provider = &collector;
  Engine engine(load_config_from_json(kChainConfig), opts);
  auto ctx = RequestContext::with_cancel(RequestContext::background());

  ChainRun run = run_chain(engine, ctx, [&ctx] { ctx.cancel(); });

  CHECK(run.cancelled_error);
  CHECK(run.error == "pine: execution cancelled: context canceled");
  REQUIRE(run.traced.result.common.count("x") == 1);
  CHECK(run.traced.result.common.at("x").as_bool());
  CHECK(run.traced.result.common.count("y") == 0);
  // No trace assertion: pine-cpp's execute_traced_into leaves trace empty on
  // any run error (pine-go keeps the partial trace) — a pre-existing gap
  // that is not specific to cancellation.
  std::string m = collector.to_json();
  CHECK(m.find("\"pine_dag_executions_total\":{\"error\":1") != std::string::npos);
}

TEST_CASE("engine: deadline mid-run reports deadline exceeded") {
  register_probes();
  Engine engine(load_config_from_json(kChainConfig));
  auto ctx = RequestContext::with_timeout(RequestContext::background(), 20ms);

  ChainRun run = run_chain(engine, ctx, [] {});

  CHECK(run.cancelled_error);
  CHECK(run.deadline_exceeded);
  CHECK(run.error == "pine: execution cancelled: context deadline exceeded");
}

TEST_CASE("engine: cancel during the last node is still a success") {
  register_probes();
  static const char* kOne = R"({
    "pipeline_config": {
      "operators": {
        "op_a": {"type_name": "test217_block_ignoring_ctx",
                 "$metadata": {"common_input": [], "common_output": ["x"], "item_input": [], "item_output": []}}
      },
      "pipeline_map": {"s": {"pipeline": ["op_a"]}}
    },
    "pipeline_group": {"main": {"pipeline": ["s"]}},
    "flow_contract": {"common_input": [], "common_output": ["x"], "item_input": [], "item_output": []}
  })";
  Engine engine(load_config_from_json(kOne));
  auto ctx = RequestContext::with_cancel(RequestContext::background());
  std::latch started(1);
  std::latch release(1);
  g_started = &started;
  g_release = &release;
  std::thread helper([&] {
    started.wait();
    ctx.cancel();
    release.count_down();
  });
  Result r;
  CHECK_NOTHROW(r = engine.execute(Request{}, ctx));
  helper.join();
  REQUIRE(r.common.count("x") == 1);
  CHECK(r.common.at("x").as_bool());
}

TEST_CASE("engine: bench_sleep stops at the deadline and names itself") {
  static const char* kSleep = R"({
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
  Engine engine(load_config_from_json(kSleep));
  auto ctx = RequestContext::with_timeout(RequestContext::background(), 50ms);
  auto t0 = std::chrono::steady_clock::now();
  std::string err;
  try {
    engine.execute(Request{}, ctx);
  } catch (const ExecutionError& e) {
    err = e.what();
    CHECK(error_is<ContextError>(e));
  }
  auto elapsed = std::chrono::steady_clock::now() - t0;
  // The operator returned the context error itself, so it is an
  // ExecutionError naming it, as in pine-go.
  CHECK(err == "pine: execution error in operator \"sleep\": context deadline exceeded");
  CHECK(elapsed < 5s);
}

// First shard to run cancels the caller's context; with one shard worker the
// other shard is still queued and must be skipped.
std::atomic<const RequestContext::Cancelable*> g_cancel_on_first_shard{nullptr};

struct CancelOnFirstShard : public Operator, public ConcurrentSafe {
  void execute(const RequestContext& /*ctx*/, const OperatorInput&, OperatorOutput&) override {
    if (auto* c = g_cancel_on_first_shard.exchange(nullptr)) {
      c->cancel();
    }
  }
};

TEST_CASE("engine: data_parallel shards skipped by cancellation fail the operator") {
  static bool registered = false;
  if (!registered) {
    register_operator_typed<CancelOnFirstShard>({"test217_cancel_on_first_shard",
                                                 OpType::Transform,
                                                 "test-only: first shard cancels the request",
                                                 {}});
    registered = true;
  }
  static const char* kPar = R"({
    "pipeline_config": {
      "operators": {
        "p": {"type_name": "test217_cancel_on_first_shard", "data_parallel": 2,
              "$metadata": {"common_input": [], "common_output": [], "item_input": ["v"], "item_output": []}}
      },
      "pipeline_map": {"s": {"pipeline": ["p"]}}
    },
    "pipeline_group": {"main": {"pipeline": ["s"]}},
    "flow_contract": {"common_input": [], "common_output": [], "item_input": ["v"], "item_output": []}
  })";
  EngineOptions opts;
  opts.shard_pool_size = 1;
  Engine engine(load_config_from_json(kPar), opts);
  Request req;
  req.items = {Variant::object_t{{"v", Variant(1.0)}}, Variant::object_t{{"v", Variant(2.0)}}};
  auto ctx = RequestContext::with_cancel(RequestContext::background());
  g_cancel_on_first_shard = &ctx;
  std::string err;
  try {
    engine.execute(req, ctx);
  } catch (const ExecutionError& e) {
    err = e.what();
    CHECK(error_is<ContextError>(e));
  }
  CHECK(err == "pine: execution error in operator \"p\": context canceled");
}

struct RecordProvider : public Operator {
  static inline std::atomic<const ResourceProvider*> seen{nullptr};
  void execute(const RequestContext& ctx, const OperatorInput&, OperatorOutput&) override {
    seen = ctx.resource_provider();
  }
};

struct NullProvider : public ResourceProvider {
  std::shared_ptr<void> borrow(const std::string&) const override {
    return nullptr;
  }
};

TEST_CASE("engine: EngineOptions::resource_provider applies unless the context sets one") {
  static bool registered = false;
  if (!registered) {
    register_operator_typed<RecordProvider>(
        {"test217_record_provider", OpType::Transform, "test-only: records ctx.resource_provider()", {}});
    registered = true;
  }
  static const char* kRec = R"({
    "pipeline_config": {
      "operators": {
        "r": {"type_name": "test217_record_provider",
              "$metadata": {"common_input": [], "common_output": [], "item_input": [], "item_output": []}}
      },
      "pipeline_map": {"s": {"pipeline": ["r"]}}
    },
    "pipeline_group": {"main": {"pipeline": ["s"]}},
    "flow_contract": {"common_input": [], "common_output": [], "item_input": [], "item_output": []}
  })";
  NullProvider engine_level;
  NullProvider request_level;
  EngineOptions opts;
  opts.resource_provider = &engine_level;
  Engine engine(load_config_from_json(kRec), opts);

  engine.execute(Request{});
  CHECK(RecordProvider::seen.load() == &engine_level);

  engine.execute(Request{},
                 RequestContext::with_resource_provider(RequestContext::background(), &request_level));
  CHECK(RecordProvider::seen.load() == &request_level);
}

// ---- operators -------------------------------------------------------------

// Holds a frame, spec and input together: OperatorInput points into both.
struct DirectInput {
  ColumnFrame frame;
  std::unique_ptr<InputFieldSpec> spec;
  OperatorInput input;

  DirectInput(Variant::object_t common, std::vector<Variant::object_t> items, const OperatorConfig& cfg)
      : frame(std::move(common), std::move(items)),
        spec(std::make_unique<InputFieldSpec>(compute_input_field_spec(cfg))),
        input(build_operator_input(frame, cfg.name, *spec)) {
  }
};

std::unique_ptr<Operator> make_op(const OperatorConfig& cfg) {
  const auto* entry = registry_entry(cfg.type_name);
  REQUIRE(entry != nullptr);
  auto op = entry->factory();
  op->init(cfg);
  return op;
}

RequestContext cancelled_ctx() {
  auto c = RequestContext::with_cancel(RequestContext::background());
  c.cancel();
  return c;
}

TEST_CASE("lua: a cancelled context stops item mode between items") {
  OperatorConfig cfg;
  cfg.name = "lua";
  cfg.type_name = "transform_by_lua";
  cfg.params = Variant(Variant::object_t{{"lua_script", Variant("function f() return 1 end")},
                                         {"function_for_item", Variant("f")}});
  cfg.metadata.item_output = {"y"};
  auto op = make_op(cfg);
  DirectInput in({}, {Variant::object_t{}, Variant::object_t{}}, cfg);
  OperatorOutput out;
  std::string err;
  try {
    op->execute(cancelled_ctx(), in.input, out);
  } catch (const ExecutionError& e) {
    err = e.what();
    CHECK(error_is<ContextError>(e));
  }
  CHECK(err == "lua: item[0]: context canceled");
  CHECK(out.item_writes().empty());
}

TEST_CASE("lua: a cancelled context stops common mode before the call") {
  OperatorConfig cfg;
  cfg.name = "lua";
  cfg.type_name = "transform_by_lua";
  cfg.params = Variant(Variant::object_t{{"lua_script", Variant("function f() return 1 end")},
                                         {"function_for_common", Variant("f")}});
  cfg.metadata.common_output = {"y"};
  auto op = make_op(cfg);
  DirectInput in({}, {}, cfg);
  OperatorOutput out;
  CHECK_THROWS_WITH_AS(op->execute(cancelled_ctx(), in.input, out), "lua: context canceled", ExecutionError);
}

// Hands out one RedisConnResource pointing at a port nothing listens on:
// a done context must fail the command before any dial.
struct RedisProvider : public ResourceProvider {
  std::shared_ptr<redis::RedisConnResource> conn;
  std::shared_ptr<void> borrow(const std::string& name) const override {
    return name == "redis_conn" ? conn : nullptr;
  }
};

OperatorConfig redis_cfg(const std::string& type, bool fail_on_error) {
  OperatorConfig cfg;
  cfg.name = "r";
  cfg.type_name = type;
  cfg.params = Variant(Variant::object_t{{"resource_name", Variant("redis_conn")},
                                         {"key_prefix", Variant("k:")},
                                         {"fail_on_error", Variant(fail_on_error)}});
  if (type == "transform_redis_get") {
    cfg.metadata.common_input = {"user_id"};
    cfg.metadata.common_output = {"value", "cache_hit"};
  } else {
    cfg.metadata.common_input = {"user_id", "value"};
  }
  return cfg;
}

TEST_CASE("redis_get: a done context fails the command before it is sent") {
  metrics::Collector collector;
  RedisProvider provider;
  provider.conn = std::make_shared<redis::RedisConnResource>("127.0.0.1", 1, "", 0, "cache", &collector);
  auto op = make_op(redis_cfg("transform_redis_get", /*fail_on_error=*/false));
  DirectInput in({{"user_id", Variant("42")}}, {}, redis_cfg("transform_redis_get", false));

  OperatorOutput out;
  op->execute(RequestContext::with_resource_provider(cancelled_ctx(), &provider), in.input, out);
  CHECK(out.warning() == "transform_redis_get: Get(k:42): context canceled");
  REQUIRE(out.common_writes().count("cache_hit") == 1);
  CHECK_FALSE(out.common_writes().at("cache_hit").as_bool());

  auto expired = RequestContext::with_timeout(RequestContext::background(), 0ms);
  auto strict = make_op(redis_cfg("transform_redis_get", /*fail_on_error=*/true));
  OperatorOutput out2;
  CHECK_THROWS_WITH_AS(
      strict->execute(RequestContext::with_resource_provider(expired, &provider), in.input, out2),
      "transform_redis_get: Get(k:42): context deadline exceeded", ExecutionError);

  // pine-go's status taxonomy: cancel → "error", deadline → "timeout".
  std::string m = collector.to_json();
  CHECK(m.find("\"cache GET error\":1") != std::string::npos);
  CHECK(m.find("\"cache GET timeout\":1") != std::string::npos);
}

TEST_CASE("redis_set: a done context is a write failure") {
  RedisProvider provider;
  provider.conn = std::make_shared<redis::RedisConnResource>("127.0.0.1", 1, "", 0);
  auto cfg = redis_cfg("transform_redis_set", /*fail_on_error=*/false);
  auto op = make_op(cfg);
  DirectInput in({{"user_id", Variant("42")}, {"value", Variant("v")}}, {}, cfg);
  OperatorOutput out;
  op->execute(RequestContext::with_resource_provider(cancelled_ctx(), &provider), in.input, out);
  CHECK(out.warning() == "transform_redis_set: write key k:42: context canceled");
}

}  // namespace
