// Issue #219: request-level debug. Mirrors pine-go's pkg/debug/debug_test.go
// and internal/runtime/debug_test.go, and pine-java's RequestDebugTest.

#include "pine/debug.hpp"
#include "pine/operator.hpp"
#include "pine/operator_input.hpp"
#include "pine/pine.hpp"

#include <doctest/doctest.h>

#include <atomic>
#include <chrono>
#include <latch>
#include <mutex>
#include <set>
#include <stdexcept>
#include <string>
#include <thread>
#include <vector>

using namespace pine;
using namespace std::chrono_literals;

namespace {

const int kRequestKey = 0;

struct Event {
  debug::NodeMeta meta;
  std::string kind;
  Variant data;
};

class Capture : public debug::Emitter {
 public:
  void emit(const debug::NodeMeta& meta, const std::string& kind, const Variant& data) override {
    std::lock_guard<std::mutex> lk(mu_);
    events_.push_back(Event{meta, kind, data});
  }

  std::vector<Event> of_kind(const std::string& kind) const {
    std::lock_guard<std::mutex> lk(mu_);
    std::vector<Event> out;
    for (const auto& e : events_) {
      if (e.kind == kind) {
        out.push_back(e);
      }
    }
    return out;
  }

  std::size_t size() const {
    std::lock_guard<std::mutex> lk(mu_);
    return events_.size();
  }

 private:
  mutable std::mutex mu_;
  std::vector<Event> events_;
};

class Throwing : public debug::Emitter {
 public:
  void emit(const debug::NodeMeta&, const std::string&, const Variant&) override {
    throw std::runtime_error("emitter broke");
  }
};

class ThrowingNonStd : public debug::Emitter {
 public:
  void emit(const debug::NodeMeta&, const std::string&, const Variant&) override {
    throw 42;
  }
};

// ---- Recorder --------------------------------------------------------------

TEST_CASE("debug: disabled by default") {
  auto bg = RequestContext::background();
  CHECK(debug::from_context(bg) == nullptr);
  CHECK(debug::from_context(debug::with_emitter(bg, nullptr)) == nullptr);
}

TEST_CASE("debug: a null emitter keeps the existing recorder") {
  Capture capture;
  auto ctx = debug::with_emitter(RequestContext::background(), &capture);
  auto same = debug::with_emitter(ctx, nullptr);
  REQUIRE(debug::from_context(same) != nullptr);
  debug::from_context(same)->emit("k", Variant(1));
  CHECK(capture.size() == 1);
}

TEST_CASE("debug: bind_node keeps values and cancellation without mutating the parent") {
  Capture capture;
  auto root = RequestContext::with_cancel(RequestContext::background());
  auto parent = debug::with_emitter(
      RequestContext::with_value(root, &kRequestKey, std::any(std::string("kept"))), &capture);
  debug::NodeMeta meta{"rank", "transform_x", "scoring"};
  auto bound = debug::from_context(parent)->bind_node(parent, meta);

  REQUIRE(bound.value(&kRequestKey) != nullptr);
  CHECK(std::any_cast<std::string>(*bound.value(&kRequestKey)) == "kept");
  debug::from_context(bound)->emit("bound", Variant(1));
  debug::from_context(parent)->emit("parent", Variant(2));
  auto b = capture.of_kind("bound");
  auto p = capture.of_kind("parent");
  REQUIRE(b.size() == 1);
  REQUIRE(p.size() == 1);
  CHECK(b[0].meta == meta);
  CHECK(p[0].meta == debug::NodeMeta{});

  root.cancel();
  CHECK(bound.cancelled());
}

TEST_CASE("debug: emitter failures are isolated") {
  Throwing t;
  ThrowingNonStd n;
  CHECK_NOTHROW(
      debug::from_context(debug::with_emitter(RequestContext::background(), &t))->emit("k", Variant(1)));
  CHECK_NOTHROW(
      debug::from_context(debug::with_emitter(RequestContext::background(), &n))->emit("k", Variant(1)));
}

// ---- engine binding ----------------------------------------------------------

// Per-test hooks (registration is process-wide; doctest runs cases serially).
std::atomic<std::latch*> g_started{nullptr};
std::atomic<std::latch*> g_release{nullptr};
std::atomic<bool> g_block_until_cancelled{false};

struct DebugTestOp : public Operator, public ConcurrentSafe {
  std::string field = "result";

  void init(const OperatorConfig& config) override {
    if (const auto* f = config.params.is_object() ? config.params.find("field") : nullptr) {
      field = f->as_string();
    }
  }

  void execute(const RequestContext& ctx, const OperatorInput& input, OperatorOutput& out) override {
    const std::any* kept = ctx.value(&kRequestKey);
    if (kept == nullptr || std::any_cast<std::string>(*kept) != "kept") {
      throw std::runtime_error("node context lost request values");
    }
    auto n = input.item_count();
    if (auto* started = g_started.load()) {
      started->count_down();
      if (g_block_until_cancelled.load()) {
        ctx.wait_for(10s);
        ctx.throw_if_cancelled();
      } else {
        g_release.load()->wait();
      }
    }
    const auto* r = debug::from_context(ctx);
    if (r != nullptr) {
      r->emit("items", Variant(static_cast<int>(n)));
    }
    for (std::size_t i = 0; i < n; ++i) {
      Variant value = input.item(i, "value");
      if (r != nullptr) {
        r->emit("item", value);
      }
      out.set_item(static_cast<int>(i), field, value);
    }
  }
};

void register_debug_op() {
  static bool registered = false;
  if (registered) {
    return;
  }
  OperatorSchema schema;
  schema.name = "transform_debug_test";
  schema.type = OpType::Transform;
  schema.description = "test-only: emits request debug events";
  ParamSchema field;
  field.type = "string";
  field.default_value = Variant("result");
  field.description = "item output field";
  schema.params.emplace("field", std::move(field));
  register_operator_typed<DebugTestOp>(std::move(schema));
  registered = true;
}

std::string op_json(const std::string& name, const std::string& field, int data_parallel) {
  std::string dp = data_parallel > 1 ? ", \"data_parallel\": " + std::to_string(data_parallel) : "";
  return "\"" + name + "\": {\"type_name\": \"transform_debug_test\", \"field\": \"" + field + "\"" + dp +
         ", \"$metadata\": {\"common_input\": [], \"common_output\": [], \"item_input\": [\"value\"], "
         "\"item_output\": [\"" +
         field + "\"]}}";
}

// ops all live in the pipeline_map entry "scoring".
Config debug_config(const std::vector<std::string>& names, const std::vector<std::string>& ops,
                    const std::string& item_out) {
  std::string op_list;
  std::string pipeline;
  for (std::size_t i = 0; i < ops.size(); ++i) {
    op_list += (i ? ", " : "") + ops[i];
    pipeline += (i ? ", \"" : "\"") + names[i] + "\"";
  }
  return load_config_from_json(R"({"pipeline_config": {"operators": {)" + op_list +
                               R"(}, "pipeline_map": {"scoring": {"pipeline": [)" + pipeline +
                               R"(]}}}, "pipeline_group": {"main": {"pipeline": ["scoring"]}},
      "flow_contract": {"common_input": [], "common_output": [], "item_input": ["value"],
                        "item_output": [)" +
                               item_out + "]}}");
}

Request items(int n) {
  Request req;
  for (int i = 0; i < n; ++i) {
    Variant::object_t item;
    item["value"] = Variant(i + 1);
    req.items.push_back(std::move(item));
  }
  return req;
}

TEST_CASE("debug: engine preserves parallel shards and node metadata") {
  register_debug_op();
  g_started = nullptr;
  Engine engine(debug_config({"rank"}, {op_json("rank", "result", 3)}, "\"result\""));
  const debug::NodeMeta want{"rank", "transform_debug_test", "scoring"};

  for (bool enabled : {false, true, false}) {
    CAPTURE(enabled);
    RequestContext ctx =
        RequestContext::with_value(RequestContext::background(), &kRequestKey, std::any(std::string("kept")));
    Capture capture;
    if (enabled) {
      ctx = debug::with_emitter(ctx, &capture);
    }
    TracedResult traced;
    engine.execute_traced_into(items(6), ctx, &traced);
    REQUIRE(traced.result.items.size() == 6);
    for (int i = 0; i < 6; ++i) {
      CHECK(traced.result.items[static_cast<std::size_t>(i)].at("result").as_number() == i + 1);
    }
    REQUIRE(traced.trace.size() == 1);
    CHECK_FALSE(traced.trace[0].has_input_snapshot);
    CHECK_FALSE(traced.trace[0].has_output_snapshot);

    if (!enabled) {
      CHECK(capture.size() == 0);
      continue;
    }
    auto shards = capture.of_kind("items");
    REQUIRE(shards.size() == 3);
    for (const auto& e : shards) {
      CHECK(e.meta == want);
      CHECK(e.data.as_number() == 2);
    }
    auto per_item = capture.of_kind("item");
    REQUIRE(per_item.size() == 6);
    std::set<int> seen;
    for (const auto& e : per_item) {
      CHECK(e.meta == want);
      CHECK(seen.insert(static_cast<int>(e.data.as_number())).second);
    }
    CHECK(seen == std::set<int>{1, 2, 3, 4, 5, 6});
  }
}

TEST_CASE("debug: concurrent nodes and requests stay separate") {
  register_debug_op();
  constexpr int kRequests = 8;
  std::latch started(2 * kRequests);
  std::latch release(1);
  g_block_until_cancelled = false;
  g_started = &started;
  g_release = &release;
  Engine engine(debug_config({"first", "second"},
                             {op_json("first", "first", 1), op_json("second", "second", 1)},
                             "\"first\", \"second\""));

  std::mutex fail_mu;
  std::vector<std::string> failures;
  auto fail = [&](std::string msg) {
    std::lock_guard<std::mutex> lk(fail_mu);
    failures.push_back(std::move(msg));
  };
  std::vector<std::thread> threads;
  for (int n = 1; n <= kRequests; ++n) {
    threads.emplace_back([&, n] {
      Capture capture;
      auto ctx = debug::with_emitter(RequestContext::with_value(RequestContext::background(), &kRequestKey,
                                                                std::any(std::string("kept"))),
                                     &capture);
      try {
        engine.execute(items(n), ctx);
      } catch (const std::exception& e) {
        fail(e.what());
        return;
      }
      std::set<std::string> seen;
      auto events = capture.of_kind("items");
      for (const auto& e : events) {
        if (e.data.as_number() != n || e.meta.type != "transform_debug_test" || e.meta.subflow != "scoring") {
          fail("cross-request data/meta for request with " + std::to_string(n) + " items");
        }
        seen.insert(e.meta.operator_name);
      }
      if (events.size() != 2 || seen != std::set<std::string>{"first", "second"}) {
        fail("node scopes not isolated for request with " + std::to_string(n) + " items");
      }
    });
  }
  // Every node of every request is inside execute before any is released,
  // so emits from independent nodes and requests overlap.
  started.wait();
  release.count_down();
  for (auto& t : threads) {
    t.join();
  }
  g_started = nullptr;
  CHECK(failures.empty());
  for (const auto& f : failures) {
    MESSAGE(f);
  }
}

TEST_CASE("debug: parallel shards keep caller cancellation") {
  register_debug_op();
  std::latch started(3);
  g_block_until_cancelled = true;
  g_started = &started;
  Engine engine(debug_config({"rank"}, {op_json("rank", "result", 3)}, "\"result\""));
  auto parent = RequestContext::with_timeout(RequestContext::background(), 10s);
  Capture capture;
  auto ctx = debug::with_emitter(
      RequestContext::with_value(parent, &kRequestKey, std::any(std::string("kept"))), &capture);

  std::string err;
  std::thread runner([&] {
    try {
      engine.execute(items(3), ctx);
    } catch (const std::exception& e) {
      err = e.what();
    }
  });
  started.wait();
  auto t0 = std::chrono::steady_clock::now();
  parent.cancel();
  runner.join();
  g_started = nullptr;
  g_block_until_cancelled = false;
  CHECK(std::chrono::steady_clock::now() - t0 < 5s);
  CHECK(err.find("context canceled") != std::string::npos);
}

}  // namespace
