// Dual-impl equivalence — RowFrame vs ColumnFrame must produce
// byte-identical Result for the same (common, items, OperatorOutput) input.
//
// Mirrors pine-go FuzzApplyOutputStorageEquivalence and pine-python
// test_frame_equivalence.py. Without it, a divergence in any of the 5
// apply_output stages (common write / item write / remove / reorder /
// add) can leak through to /execute byte-exact while still passing the
// per-impl doctests.
#include "pine/column_frame.hpp"
#include "pine/frame.hpp"
#include "pine/row_frame.hpp"

#include <doctest/doctest.h>

#include <algorithm>
#include <cmath>
#include <limits>
#include <random>
#include <string>
#include <vector>

using namespace pine;

namespace {

// Drive both impls with identical state. Returns the projected Result
// pair so the test body can compare with a single equality check.
struct Pair {
  std::unique_ptr<RowFrame> row;
  std::unique_ptr<ColumnFrame> col;
};

Pair make_pair(Variant::object_t common, std::vector<Variant::object_t> items) {
  return {std::make_unique<RowFrame>(common, items), std::make_unique<ColumnFrame>(common, items)};
}

void apply_both(Pair& p, const OperatorOutput& out, const std::string& op_name, bool recall) {
  // apply_output now takes a non-const ref and may move-extract from
  // out (RowFrame moves added_items rows). Hand each frame its own
  // mutable copy so the equivalence assertion stays meaningful.
  OperatorOutput out_row = out;
  OperatorOutput out_col = out;
  p.row->apply_output(out_row, op_name, recall);
  p.col->apply_output(out_col, op_name, recall);
}

bool result_equal(const Result& a, const Result& b) {
  // Variant lacks operator== for object/array; compare via dump_json
  // which is byte-deterministic across both impls.
  Variant::object_t ao, bo;
  for (const auto& [k, v] : a.common) {
    ao[k] = v;
  }
  for (const auto& [k, v] : b.common) {
    bo[k] = v;
  }
  if (dump_json(Variant(ao)) != dump_json(Variant(bo))) {
    return false;
  }
  Variant::array_t ai, bi;
  for (const auto& r : a.items) {
    Variant::object_t row;
    for (const auto& [k, v] : r) {
      row[k] = v;
    }
    ai.push_back(Variant(row));
  }
  for (const auto& r : b.items) {
    Variant::object_t row;
    for (const auto& [k, v] : r) {
      row[k] = v;
    }
    bi.push_back(Variant(row));
  }
  return dump_json(Variant(ai)) == dump_json(Variant(bi));
}

Variant rand_value(std::mt19937& rng) {
  int k = rng() % 8;
  switch (k) {
    case 0:
      return Variant(static_cast<double>(rng() % 100));
    case 1:
      return Variant(static_cast<double>(rng()) / 1000.0);
    case 2:
      return Variant(std::string("s") + std::to_string(rng() % 10));
    case 3:
      return Variant(true);
    case 4:
      return Variant(false);
    case 5:
      return Variant(nullptr);  // PRESENT-NULL
    case 6: {
      Variant::array_t a;
      a.push_back(Variant(1.0));
      a.push_back(Variant(2.0));
      return Variant(a);
    }
    default: {
      Variant::object_t o;
      o["k"] = Variant(std::string("v"));
      return Variant(o);
    }
  }
}

OperatorOutput rand_output(std::mt19937& rng, std::size_t n_items) {
  OperatorOutput out;
  // common writes
  int n_cw = rng() % 4;
  for (int i = 0; i < n_cw; ++i) {
    out.set_common("k" + std::to_string(rng() % 5), rand_value(rng));
  }
  // item writes
  if (n_items > 0) {
    int n_iw = rng() % (n_items * 2 + 1);
    for (int i = 0; i < n_iw; ++i) {
      int idx = static_cast<int>(rng() % n_items);
      out.set_item(idx, "f" + std::to_string(rng() % 5), rand_value(rng));
    }
    // removals
    int n_rm = rng() % (n_items / 2 + 1);
    for (int i = 0; i < n_rm; ++i) {
      out.remove_item(static_cast<int>(rng() % n_items));
    }
  }
  // additions (no recall stamp in this generator — recall=true path
  // tested separately below)
  int n_ad = rng() % 3;
  for (int i = 0; i < n_ad; ++i) {
    Variant::object_t row;
    int n_f = 1 + (rng() % 3);
    for (int j = 0; j < n_f; ++j) {
      row["f" + std::to_string(rng() % 5)] = rand_value(rng);
    }
    out.add_item(row);
  }
  return out;
}

}  // namespace

TEST_CASE("Row/Column initial-state projection equivalence") {
  std::vector<Variant::object_t> items;
  items.push_back({{"id", Variant(1.0)}, {"score", Variant(10.0)}});
  items.push_back({{"id", Variant(2.0)}, {"score", Variant(20.0)}});
  auto p = make_pair({{"region", Variant(std::string("us"))}}, items);
  auto r = p.row->to_result({"region"}, {"id", "score"});
  auto c = p.col->to_result({"region"}, {"id", "score"});
  CHECK(result_equal(r, c));
}

TEST_CASE("Row/Column common writes equivalence") {
  auto p =
      make_pair({{"region", Variant(std::string("us"))}}, {{{"id", Variant(1.0)}}, {{"id", Variant(2.0)}}});
  OperatorOutput out;
  out.set_common("region", Variant(std::string("eu")));
  out.set_common("ts", Variant(1234.0));
  apply_both(p, out, "op", false);
  CHECK(result_equal(p.row->to_result({"region", "ts"}, {"id"}), p.col->to_result({"region", "ts"}, {"id"})));
}

TEST_CASE("Row/Column item writes equivalence") {
  auto p = make_pair({}, {{{"id", Variant(1.0)}, {"score", Variant(10.0)}},
                          {{"id", Variant(2.0)}, {"score", Variant(20.0)}}});
  OperatorOutput out;
  out.set_item(0, "score", Variant(99.0));
  out.set_item(1, "bonus", Variant(true));
  apply_both(p, out, "op", false);
  CHECK(result_equal(p.row->to_result({}, {"id", "score", "bonus"}),
                     p.col->to_result({}, {"id", "score", "bonus"})));
}

TEST_CASE("Row/Column remove equivalence") {
  std::vector<Variant::object_t> items;
  for (int i = 0; i < 5; ++i) {
    items.push_back({{"id", Variant(static_cast<double>(i))}});
  }
  auto p = make_pair({}, items);
  OperatorOutput out;
  out.remove_item(1);
  out.remove_item(3);
  apply_both(p, out, "op", false);
  CHECK(p.row->item_count() == p.col->item_count());
  CHECK(p.row->item_count() == 3);
  CHECK(result_equal(p.row->to_result({}, {"id"}), p.col->to_result({}, {"id"})));
}

TEST_CASE("Row/Column reorder equivalence") {
  auto p = make_pair({}, {{{"id", Variant(0.0)}}, {{"id", Variant(1.0)}}, {{"id", Variant(2.0)}}});
  OperatorOutput out;
  out.set_item_order({2, 0, 1});
  apply_both(p, out, "op", false);
  CHECK(result_equal(p.row->to_result({}, {"id"}), p.col->to_result({}, {"id"})));
}

TEST_CASE("Row/Column additions + recall _source stamp equivalence") {
  auto p = make_pair({}, {{{"id", Variant(0.0)}}});
  OperatorOutput out;
  out.add_item({{"id", Variant(100.0)}, {"name", Variant(std::string("added"))}});
  out.add_item({{"id", Variant(200.0)}});
  apply_both(p, out, "op_recall", /*recall=*/true);
  CHECK(p.row->item_count() == 3);
  CHECK(p.col->item_count() == 3);
  CHECK(result_equal(p.row->to_result({}, {"id", "name", "_source"}),
                     p.col->to_result({}, {"id", "name", "_source"})));
}

TEST_CASE("Row/Column five-stage ordering equivalence") {
  std::vector<Variant::object_t> items;
  for (int i = 0; i < 4; ++i) {
    items.push_back(
        {{"id", Variant(static_cast<double>(i))}, {"score", Variant(static_cast<double>(i * 10))}});
  }
  auto p = make_pair({{"src", Variant(std::string("v"))}}, items);
  OperatorOutput out;
  out.set_common("src", Variant(std::string("w")));
  out.set_item(0, "score", Variant(-1.0));
  out.remove_item(2);
  // after remove → 3 items; reorder needs len-3 permutation
  out.set_item_order({2, 0, 1});
  out.add_item({{"id", Variant(99.0)}});
  apply_both(p, out, "op", false);
  CHECK(result_equal(p.row->to_result({"src"}, {"id", "score"}), p.col->to_result({"src"}, {"id", "score"})));
}

TEST_CASE("Row/Column NaN-rejection error message equivalence") {
  auto p = make_pair({}, {{{"id", Variant(1.0)}}});
  OperatorOutput out;
  out.set_common("ratio", Variant(std::numeric_limits<double>::quiet_NaN()));
  std::string row_err, col_err;
  try {
    p.row->apply_output(out, "op", false);
  } catch (const ExecutionError& e) {
    row_err = e.what();
  }
  try {
    p.col->apply_output(out, "op", false);
  } catch (const ExecutionError& e) {
    col_err = e.what();
  }
  CHECK(!row_err.empty());
  CHECK(!col_err.empty());
  CHECK(row_err == col_err);
}

TEST_CASE("Row/Column reorder-permutation error message equivalence") {
  auto p = make_pair({}, {{{"id", Variant(0.0)}}, {{"id", Variant(1.0)}}, {{"id", Variant(2.0)}}});
  OperatorOutput out;
  out.set_item_order({0, 0, 0});  // duplicate index
  std::string row_err, col_err;
  try {
    p.row->apply_output(out, "op", false);
  } catch (const ExecutionError& e) {
    row_err = e.what();
  }
  try {
    p.col->apply_output(out, "op", false);
  } catch (const ExecutionError& e) {
    col_err = e.what();
  }
  CHECK(!row_err.empty());
  CHECK(row_err == col_err);
}

TEST_CASE("Row/Column differential fuzz") {
  // 100 seeded rounds with random (common, items, OperatorOutput).
  // Either both succeed with identical Result, or both throw with
  // identical message. seed range 0..99; bump if regressions slip
  // past this batch in CI.
  for (int seed = 0; seed < 100; ++seed) {
    std::mt19937 rng(static_cast<std::uint32_t>(seed));
    std::size_t n_items = rng() % 7;
    Variant::object_t common;
    for (int i = 0, n = rng() % 4; i < n; ++i) {
      common["c" + std::to_string(i)] = rand_value(rng);
    }
    std::vector<Variant::object_t> items;
    for (std::size_t i = 0; i < n_items; ++i) {
      Variant::object_t row;
      int n_f = 1 + static_cast<int>(rng() % 4);
      for (int j = 0; j < n_f; ++j) {
        row["f" + std::to_string(j)] = rand_value(rng);
      }
      items.push_back(std::move(row));
    }

    auto p = make_pair(common, items);
    auto out = rand_output(rng, n_items);
    // apply_output now takes a non-const ref; hand each frame its own
    // mutable copy so both see the same input regardless of which one
    // moves rows out.
    OperatorOutput out_row = out;
    OperatorOutput out_col = out;

    std::string row_err, col_err;
    try {
      p.row->apply_output(out_row, "op", false);
    } catch (const ExecutionError& e) {
      row_err = e.what();
    } catch (const Error& e) {
      row_err = e.what();
    }
    try {
      p.col->apply_output(out_col, "op", false);
    } catch (const ExecutionError& e) {
      col_err = e.what();
    } catch (const Error& e) {
      col_err = e.what();
    }

    INFO("seed=" << seed);
    REQUIRE(row_err.empty() == col_err.empty());
    if (!row_err.empty()) {
      // Error messages may differ in trailing OOB wording (impls
      // store rows differently); compare the leading segment up
      // to the first colon so the class/segment matches.
      auto row_pre = row_err.substr(0, row_err.find(':'));
      auto col_pre = col_err.substr(0, col_err.find(':'));
      CHECK(row_pre == col_pre);
      continue;
    }
    // Both succeeded — projections must match exactly.
    std::vector<std::string> ck;
    for (int i = 0; i < 5; ++i) {
      ck.push_back("k" + std::to_string(i));
    }
    std::vector<std::string> ik;
    for (int i = 0; i < 5; ++i) {
      ik.push_back("f" + std::to_string(i));
    }
    ik.push_back("_source");
    auto r = p.row->to_result(ck, ik);
    auto c = p.col->to_result(ck, ik);
    CHECK(result_equal(r, c));
  }
}
