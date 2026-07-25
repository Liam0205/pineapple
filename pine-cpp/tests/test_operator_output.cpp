#include "pine/pine.hpp"

#include <doctest/doctest.h>

using namespace pine;

TEST_CASE("OperatorOutput: set_common collects field writes") {
  OperatorOutput out;
  out.set_common("a", Variant(std::string("v1")));
  out.set_common("b", Variant(2.0));
  CHECK(out.common_writes().size() == 2);
  CHECK(out.common_writes().at("a").as_string() == "v1");
  CHECK(out.common_writes().at("b").as_number() == 2.0);
}

TEST_CASE("OperatorOutput: set_item collects ordered (idx, field, value) log") {
  OperatorOutput out;
  out.set_item(0, "x", Variant(std::string("hello")));
  out.set_item(0, "y", Variant(true));
  out.set_item(2, "x", Variant(std::string("world")));
  REQUIRE(out.item_writes().size() == 3);
  CHECK(out.item_writes()[0].index == 0);
  CHECK(out.item_writes()[0].field == "x");
  CHECK(out.item_writes()[0].value.as_string() == "hello");
  CHECK(out.item_writes()[1].index == 0);
  CHECK(out.item_writes()[1].field == "y");
  CHECK(out.item_writes()[1].value.as_bool() == true);
  CHECK(out.item_writes()[2].index == 2);
  CHECK(out.item_writes()[2].field == "x");
  CHECK(out.item_writes()[2].value.as_string() == "world");
}

TEST_CASE("OperatorOutput: add_item appends rows") {
  OperatorOutput out;
  Variant::object_t r1;
  r1["id"] = Variant(std::string("a"));
  out.add_item(r1);
  out.add_item({{"id", Variant(std::string("b"))}});
  REQUIRE(out.added_items().size() == 2);
  CHECK(out.added_items()[0].at("id").as_string() == "a");
  CHECK(out.added_items()[1].at("id").as_string() == "b");
}

TEST_CASE("OperatorOutput: remove_item dedupes by set") {
  OperatorOutput out;
  out.remove_item(0);
  out.remove_item(2);
  out.remove_item(0);
  CHECK(out.removed_items().size() == 2);
  CHECK(out.removed_items().count(0) == 1);
  CHECK(out.removed_items().count(2) == 1);
}

TEST_CASE("OperatorOutput: set_item_order is opt-in via has_item_order") {
  OperatorOutput out;
  CHECK_FALSE(out.has_item_order());
  out.set_item_order({2, 0, 1});
  CHECK(out.has_item_order());
  REQUIRE(out.item_order().size() == 3);
  CHECK(out.item_order()[0] == 2);
}

TEST_CASE("OperatorOutput: set_warning is first-wins") {
  OperatorOutput out;
  CHECK_FALSE(out.has_warning());
  out.set_warning("first");
  out.set_warning("second");
  CHECK(out.has_warning());
  CHECK(out.warning() == "first");
}

// --- reset() lifetime contract (issue #122) ---
// Engine-level reuse / leakage coverage lives in test_output_pool.cpp;
// these two cases pin the value semantics of reset() itself.

TEST_CASE("OperatorOutput::reset retains container capacity") {
  // Capacity retention is the entire benefit of reusing the buffer — if
  // reset() ever regressed to move-assignment (`item_writes_ =
  // std::vector<ItemWrite>{}`) the leak tests in test_output_pool.cpp would
  // still pass while the optimization silently evaporated. Assert the
  // mechanism directly.
  //
  // Note `item_writes_ = {}` would NOT be such a regression: that binds
  // to operator=(initializer_list) and forwards to assign(), which never
  // shrinks the buffer. Only a genuine move-assign from a fresh
  // container swaps the heap block away.
  OperatorOutput out;
  for (int i = 0; i < 256; ++i) {
    out.set_item(i, "f", Variant(static_cast<double>(i)));
    Variant::object_t row;
    row["k"] = Variant(static_cast<double>(i));
    out.add_item(std::move(row));
  }
  out.set_common("c", Variant(true));
  out.remove_item(3);
  out.set_item_order({1, 0});
  out.set_warning("w");

  const std::size_t writes_cap = out.item_writes().capacity();
  const std::size_t added_cap = out.added_items().capacity();
  REQUIRE(writes_cap >= 256);
  REQUIRE(added_cap >= 256);

  out.reset();

  // Emptied...
  CHECK(out.item_writes().empty());
  CHECK(out.added_items().empty());
  CHECK(out.common_writes().empty());
  CHECK(out.removed_items().empty());
  CHECK(out.item_order().empty());
  CHECK(out.has_item_order() == false);
  CHECK(out.has_warning() == false);
  CHECK(out.warning().empty());
  // ...but the heap blocks stay, so the next Execute's appends are
  // amortized-free.
  CHECK(out.item_writes().capacity() == writes_cap);
  CHECK(out.added_items().capacity() == added_cap);
}

TEST_CASE("OperatorOutput::reset releases capacity above the retain limit") {
  // The buffer is thread_local and the DAG pool keeps its workers for the
  // engine's lifetime, so unbounded retention would let one outsized request
  // pin its peak on every worker forever — pine-go avoids that because the
  // GC empties its sync.Pool. reset() therefore drops buffers grown past
  // kRetainLimit (65536) instead of keeping them. This asserts the release
  // half; the case above asserts that ordinary sizes are still retained.
  //
  // EVERY capacity-bearing container is driven here, not just item_writes_.
  // An earlier version of this test exercised set_item alone, which left the
  // added_items_ and column_writes_ release branches unguarded: deleting them
  // kept the suite fully green while a recall-heavy load still pinned ~478 MB
  // permanently, since recall operators grow added_items_, not item_writes_.
  constexpr std::size_t kLimit = 65536;
  constexpr int kHuge = 70000;

  SUBCASE("item_writes (set_item)") {
    OperatorOutput out;
    for (int i = 0; i < kHuge; ++i) {
      out.set_item(i, "f", Variant(static_cast<double>(i)));
    }
    REQUIRE(out.item_writes().capacity() > kLimit);
    out.reset();
    CHECK(out.item_writes().empty());
    CHECK(out.item_writes().capacity() <= kLimit);
    // Still fully usable after the release.
    out.set_item(0, "f", Variant(1.0));
    REQUIRE(out.item_writes().size() == 1);
  }

  SUBCASE("added_items (add_item) — the recall path") {
    OperatorOutput out;
    for (int i = 0; i < kHuge; ++i) {
      Variant::object_t row;
      row["k"] = Variant(static_cast<double>(i));
      out.add_item(std::move(row));
    }
    REQUIRE(out.added_items().capacity() > kLimit);
    out.reset();
    CHECK(out.added_items().empty());
    CHECK(out.added_items().capacity() <= kLimit);
    Variant::object_t row;
    row["k"] = Variant(1.0);
    out.add_item(std::move(row));
    REQUIRE(out.added_items().size() == 1);
  }

  SUBCASE("column_writes (set_item_column_double)") {
    OperatorOutput out;
    for (int i = 0; i < kHuge; ++i) {
      out.set_item_column_double("f", std::vector<double>{static_cast<double>(i)});
    }
    REQUIRE(out.column_writes().capacity() > kLimit);
    out.reset();
    CHECK(out.column_writes().empty());
    CHECK(out.column_writes().capacity() <= kLimit);
    out.set_item_column_double("f", std::vector<double>{1.0});
    REQUIRE(out.column_writes().size() == 1);
  }

  SUBCASE("item_order (set_item_order) — the reorder path") {
    // reorder_sort / reorder_shuffle_by_salt fill this to the item count.
    // Retaining it was never a win in the first place: set_item_order
    // move-assigns, so the next call throws away whatever reset() kept.
    OperatorOutput out;
    std::vector<int> order(kHuge);
    for (int i = 0; i < kHuge; ++i) {
      order[static_cast<std::size_t>(i)] = i;
    }
    out.set_item_order(std::move(order));
    REQUIRE(out.item_order().capacity() > kLimit);
    out.reset();
    CHECK(out.item_order().empty());
    CHECK(out.item_order().capacity() <= kLimit);
    CHECK(out.has_item_order() == false);
    out.set_item_order(std::vector<int>{1, 0});
    REQUIRE(out.item_order().size() == 2);
  }
}

TEST_CASE("OperatorOutput::reset is idempotent and safe on a fresh object") {
  OperatorOutput out;
  out.reset();
  out.reset();
  CHECK(out.item_writes().empty());
  CHECK(out.has_warning() == false);
  // A reset object is fully usable again.
  out.set_common("after", Variant(std::string("ok")));
  REQUIRE(out.common_writes().count("after") == 1);
  CHECK(out.common_writes().at("after").as_string() == "ok");
}
