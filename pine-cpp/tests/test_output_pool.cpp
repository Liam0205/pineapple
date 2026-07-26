// Locks the thread_local OperatorOutput reuse contract (issue #122,
// mirroring pine-go's sync.Pool + Reset from #119).
//
// The scheduler hands each node body a thread_local OperatorOutput that is
// reset on acquire rather than freshly constructed. These tests assert the
// two properties that make that safe:
//   1. every Execute sees a clean output — no writes leak in from the
//      previous operator on the same thread, or the previous request;
//   2. capacity is actually retained across Executes (the point of the
//      optimization) — otherwise the change is pure risk with no win.
#include "pine/operator.hpp"
#include "pine/operator_input.hpp"
#include "pine/pine.hpp"

#include <doctest/doctest.h>

#include <string>
#include <vector>

using namespace pine;

namespace {

// InspectOutputOp records what its OperatorOutput already contained on
// entry. Under a correct reset-on-acquire contract every observation is
// empty; a leak shows up as a non-empty saw_* counter.
struct InspectState {
  std::vector<std::string> saw_common_fields;
  std::size_t saw_added = 0;
  std::size_t saw_item_writes = 0;
  int executes = 0;
};

InspectState& inspect_state() {
  static InspectState state;
  return state;
}

struct InspectOutputOp : public Operator {
  void init(const OperatorConfig&) override {
  }
  void execute(const OperatorInput&, OperatorOutput& out) override {
    InspectState& st = inspect_state();
    ++st.executes;
    for (const auto& [field, value] : out.common_writes()) {
      (void)value;
      st.saw_common_fields.push_back(field);
    }
    st.saw_added += out.added_items().size();
    st.saw_item_writes += out.item_writes().size();
    out.set_common("inspect_ok", Variant(true));
  }
};

// AddingRecallOp emits a fixed number of items every request. Exercises
// the added_items_ reset path (a distinct container from common_writes_)
// and would surface ghost items in the frame if reset missed it.
struct AddingRecallOp : public Operator {
  void init(const OperatorConfig&) override {
  }
  void execute(const OperatorInput&, OperatorOutput& out) override {
    for (int i = 0; i < 3; ++i) {
      Variant::object_t row;
      row["id"] = Variant(std::string(1, static_cast<char>('a' + i)));
      out.add_item(std::move(row));
    }
  }
};

// ItemWritingOp writes one field on every item, so item_writes_ grows to
// the item count. Used both for leak detection and as the workload whose
// vector capacity should be retained across Executes.
struct ItemWritingOp : public Operator {
  void init(const OperatorConfig&) override {
  }
  void execute(const OperatorInput& input, OperatorOutput& out) override {
    for (std::size_t i = 0; i < input.item_count(); ++i) {
      out.set_item(static_cast<int>(i), "marked", Variant(true));
    }
  }
};

// ThrowingRecallOp adds items then throws. apply_output never runs, so what it
// added is still fully live when the node body unwinds — the largest payload
// any single node can be holding.
struct ThrowingRecallOp : public Operator {
  void init(const OperatorConfig&) override {
  }
  void execute(const OperatorInput&, OperatorOutput& out) override {
    for (int i = 0; i < 5; ++i) {
      Variant::object_t row;
      row["id"] = Variant(std::string("leak") + std::to_string(i));
      out.add_item(std::move(row));
    }
    throw std::runtime_error("deliberate failure after adding items");
  }
};

void register_pool_test_ops() {
  static bool registered = false;
  if (registered) {
    return;
  }
  static const OperatorSchema inspect_schema{
      "pool_test_inspect", OpType::Transform, "records leaked output state on entry", {}};
  static const OperatorSchema recall_schema{
      "pool_test_recall", OpType::Recall, "adds three fixed items", {}};
  static const OperatorSchema mark_schema{
      "pool_test_mark", OpType::Transform, "writes one field on every item", {}};
  register_operator_typed<InspectOutputOp>(inspect_schema);
  register_operator_typed<AddingRecallOp>(recall_schema);
  register_operator_typed<ItemWritingOp>(mark_schema);
  static const OperatorSchema throw_schema{
      "pool_test_throwing_recall", OpType::Recall, "adds items then throws", {}};
  register_operator_typed<ThrowingRecallOp>(throw_schema);
  registered = true;
}

constexpr const char* kInspectOnlyConfig = R"({
  "_PINEAPPLE_VERSION": "0.10.16",
  "pipeline_config": {
    "operators": {
      "inspect": {
        "type_name": "pool_test_inspect",
        "$metadata": {"common_output": ["inspect_ok"]}
      }
    },
    "pipeline_map": {"stage": {"pipeline": ["inspect"]}}
  },
  "pipeline_group": {"main": {"pipeline": ["stage"]}},
  "flow_contract": {
    "common_input": [],
    "item_input": [],
    "common_output": ["inspect_ok"],
    "item_output": []
  }
})";

constexpr const char* kRecallThenInspectConfig = R"({
  "_PINEAPPLE_VERSION": "0.10.16",
  "pipeline_config": {
    "operators": {
      "recall": {
        "type_name": "pool_test_recall",
        "recall": true,
        "$metadata": {"item_output": ["id"]}
      },
      "mark": {
        "type_name": "pool_test_mark",
        "$metadata": {"item_input": ["id"], "item_output": ["marked"]}
      },
      "inspect": {
        "type_name": "pool_test_inspect",
        "$metadata": {"common_output": ["inspect_ok"]}
      }
    },
    "pipeline_map": {"stage": {"pipeline": ["recall", "mark", "inspect"]}}
  },
  "pipeline_group": {"main": {"pipeline": ["stage"]}},
  "flow_contract": {
    "common_input": [],
    "item_input": [],
    "common_output": ["inspect_ok"],
    "item_output": ["id", "marked"]
  }
})";

}  // namespace

TEST_CASE("OperatorOutput reuse: no state leakage across repeated runs") {
  register_pool_test_ops();
  inspect_state() = InspectState{};

  // dag_pool_size = 1 is load-bearing, not tuning. The buffer is
  // thread_local, so a leak from run i is only observable on run i+1 if both
  // runs land on the SAME worker. This pipeline submits one task per request
  // and the pool hands it to any idle worker, so at the default size
  // (nproc * 4) 32 requests spread across 32 distinct threads and every run
  // gets a pristine buffer — the test would pass no matter what reset() did.
  // Pinning the pool to a single worker makes same-thread reuse certain
  // instead of probabilistic.
  //
  // Historical note, because the obvious mutation no longer bites: when this
  // was written, deleting the acquire-side reset() failed this case 20/20 at
  // pool size 1 and passed 20/20 at the default size. A release-side reset()
  // was added afterwards, so the buffer is already empty when the next request
  // acquires it, and deleting the acquire-side call alone now leaves this
  // green. What guards the acquire-side reset today is the failed-request case
  // further down, where a throwing node skips the release. Pool size 1 stays
  // regardless: it costs nothing and keeps the case deterministic.
  EngineOptions opts;
  opts.dag_pool_size = 1;
  Engine engine(load_config_from_json(kInspectOnlyConfig), opts);
  constexpr int kRuns = 32;
  for (int i = 0; i < kRuns; ++i) {
    Request req;
    auto resp = engine.execute(req);
    REQUIRE(resp.common.count("inspect_ok") == 1);
    CHECK(resp.common.at("inspect_ok").as_bool() == true);
  }

  const InspectState& st = inspect_state();
  CHECK(st.executes == kRuns);
  // The whole point: the reused buffer must look freshly constructed on
  // every acquire. Any surviving write from run i would be observed on
  // run i+1.
  CHECK(st.saw_common_fields.empty());
  CHECK(st.saw_added == 0);
  CHECK(st.saw_item_writes == 0);
}

TEST_CASE("OperatorOutput reuse: no leakage across operators within one run") {
  // Three operators run back to back on (very likely) the same thread and
  // therefore the same thread_local buffer. recall fills added_items_,
  // mark fills item_writes_, inspect asserts it sees neither. This is the
  // intra-request half of the contract, distinct from the cross-request case
  // above. Either reset satisfies it: the release-side call runs after the
  // try/catch and so covers the throw path too, which means dropping the
  // acquire-side call alone leaves this green. It is a behaviour assertion
  // about what an operator may observe, not a gate on one line.
  register_pool_test_ops();
  inspect_state() = InspectState{};

  Engine engine(load_config_from_json(kRecallThenInspectConfig));
  constexpr int kRuns = 16;
  for (int i = 0; i < kRuns; ++i) {
    Request req;
    auto resp = engine.execute(req);
    // Exactly the three items recall emits — no ghosts carried over from
    // a previous request's added_items_.
    REQUIRE(resp.items.size() == 3);
    for (const auto& item : resp.items) {
      REQUIRE(item.count("id") == 1);
      REQUIRE(item.count("marked") == 1);
      CHECK(item.at("marked").as_bool() == true);
    }
  }

  const InspectState& st = inspect_state();
  CHECK(st.executes == kRuns);
  CHECK(st.saw_common_fields.empty());
  CHECK(st.saw_added == 0);
  CHECK(st.saw_item_writes == 0);
}

namespace {

// One pipeline, two entry stages: a failing branch and a good branch, both in
// the same engine so they share the same single worker and therefore the same
// thread_local buffer. Selected per request via `skip`.
constexpr const char* kThrowThenGoodConfig = R"({
  "_PINEAPPLE_VERSION": "0.10.16",
  "pipeline_config": {
    "operators": {
      "bad_recall": {
        "type_name": "pool_test_throwing_recall",
        "recall": true,
        "skip": ["_run_bad"],
        "$metadata": {"common_input": ["_run_bad"], "item_output": ["id"]}
      },
      "good_recall": {
        "type_name": "pool_test_recall",
        "recall": true,
        "skip": ["_run_good"],
        "$metadata": {"common_input": ["_run_good"], "item_output": ["id"]}
      }
    },
    "pipeline_map": {"stage": {"pipeline": ["bad_recall", "good_recall"]}}
  },
  "pipeline_group": {"main": {"pipeline": ["stage"]}},
  "flow_contract": {"common_input": ["_run_bad", "_run_good"], "item_output": ["id"]}
})";

}  // namespace

TEST_CASE("OperatorOutput reuse: a failed request's items never reach a later response") {
  // A recall that adds items and then throws never reaches apply_output, so
  // its rows are still sitting in the worker's thread_local buffer. Something
  // has to clear them before the next request lands on that worker, or they
  // surface as ghost items in an unrelated successful response — which is
  // exactly what this asserts does not happen.
  //
  // Both resets can satisfy it, so this is a behaviour test rather than a gate
  // on one specific line: with the trailing reset running after the try/catch,
  // deleting the acquire-side call alone leaves this green. Remove both and it
  // fails with 8 items instead of 3, five of them named "leak*".
  //
  // Both requests must run on the SAME engine: a second Engine gets its own
  // dag_pool and therefore its own worker threads, so the buffer would not be
  // shared and the leak would be invisible. pool_size = 1 then pins both
  // requests to one worker.
  register_pool_test_ops();

  EngineOptions opts;
  opts.dag_pool_size = 1;
  Engine engine(load_config_from_json(kThrowThenGoodConfig), opts);

  Request bad;
  bad.common["_run_bad"] = Variant(false);   // false => bad_recall runs
  bad.common["_run_good"] = Variant(true);   // true  => good_recall skipped
  bool threw = false;
  try {
    (void)engine.execute(bad);
  } catch (...) {
    threw = true;
  }
  REQUIRE(threw);

  Request good;
  good.common["_run_bad"] = Variant(true);   // true  => bad_recall skipped
  good.common["_run_good"] = Variant(false); // false => good_recall runs
  auto resp = engine.execute(good);

  // AddingRecallOp emits exactly 3 items; anything more came from the failure.
  CHECK(resp.items.size() == 3);
  for (const auto& item : resp.items) {
    REQUIRE(item.count("id") == 1);
    CHECK(item.at("id").as_string().rfind("leak", 0) != 0);
  }
}
