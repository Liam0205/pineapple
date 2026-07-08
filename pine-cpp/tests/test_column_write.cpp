// Batch column write (set_item_column_double) semantics: adopt on column
// store, scatter on row store, length check, NaN validation parity,
// ordering vs per-element writes. Mirrors pine-go column_write_test.go
// and pine-java ColumnWriteTest.
#include "pine/column.hpp"
#include "pine/column_frame.hpp"
#include "pine/frame.hpp"
#include "pine/row_frame.hpp"

#include <doctest/doctest.h>

#include <limits>
#include <memory>
#include <string>
#include <vector>

using namespace pine;

namespace {

std::vector<Variant::object_t> id_items(std::initializer_list<const char*> ids) {
  std::vector<Variant::object_t> items;
  for (const char* id : ids) {
    Variant::object_t row;
    row["id"] = Variant(std::string(id));
    items.push_back(row);
  }
  return items;
}

std::vector<std::unique_ptr<Frame>> both_frames(std::initializer_list<const char*> ids) {
  std::vector<std::unique_ptr<Frame>> frames;
  frames.push_back(std::make_unique<RowFrame>(Variant::object_t{}, id_items(ids)));
  frames.push_back(std::make_unique<ColumnFrame>(Variant::object_t{}, id_items(ids)));
  return frames;
}

}  // namespace

TEST_CASE("set_item_column_double writes whole column on both frames") {
  for (auto& frame : both_frames({"a", "b", "c"})) {
    OperatorOutput out;
    out.set_item_column_double("score", {1.5, 2.5, 3.5});
    frame->apply_output(out, "op", false);

    const double want[] = {1.5, 2.5, 3.5};
    for (std::size_t i = 0; i < 3; ++i) {
      CHECK(frame->item(i, "score").as_number() == want[i]);
      CHECK(frame->item_has(i, "score"));
    }
    // Result projection includes the new field on every row.
    Result r = frame->to_result({}, {"id", "score"});
    for (const auto& row : r.items) {
      CHECK(row.count("score") == 1);
    }
  }
}

TEST_CASE("set_item_column_double length mismatch fails") {
  for (auto& frame : both_frames({"a", "b"})) {
    OperatorOutput out;
    out.set_item_column_double("score", {1.0});  // wrong length
    CHECK_THROWS_WITH_AS(frame->apply_output(out, "op", false),
                         doctest::Contains("does not match item count 2"), ExecutionError);
  }
}

TEST_CASE("set_item_column_double NaN validation message parity") {
  for (auto& frame : both_frames({"a", "b"})) {
    OperatorOutput out;
    out.set_item_column_double("score", {1.0, std::numeric_limits<double>::quiet_NaN()});
    // Same first-error message shape as the per-element path.
    CHECK_THROWS_WITH_AS(
        frame->apply_output(out, "op", false),
        doctest::Contains("item[1] write: field \"score\": NaN/Inf is not a valid JSON value"),
        ExecutionError);
  }
}

TEST_CASE("set_item_column_double overrides per-element writes") {
  for (auto& frame : both_frames({"a", "b"})) {
    OperatorOutput out;
    out.set_item(0, "score", Variant(99.0));  // per-element first
    out.set_item_column_double("score", {1.0, 2.0});
    frame->apply_output(out, "op", false);
    // Column write applies after per-element → wins on collision.
    CHECK(frame->item(0, "score").as_number() == 1.0);
    CHECK(frame->item(1, "score").as_number() == 2.0);
  }
}

TEST_CASE("set_item_column_double adopts vector zero-copy on column store") {
  ColumnFrame f(Variant::object_t{}, id_items({"a", "b"}));
  std::vector<double> vals{1.0, 2.0};
  const double* raw = vals.data();
  OperatorOutput out;
  out.set_item_column_double("score", std::move(vals));
  f.apply_output(out, "op", false);

  // The adopted DoubleColumn's backing store must alias the vector the
  // operator handed over (moved through, never copied element-wise).
  std::vector<Variant> col = f.item_column("score");
  REQUIRE(col.size() == 2);
  CHECK(col[0].as_number() == 1.0);
  CHECK(col[1].as_number() == 2.0);
  // Pin zero-copy: mutate through the original allocation and observe
  // the frame seeing the change. (raw stays valid — ownership moved to
  // the column, the allocation did not.)
  const_cast<double*>(raw)[0] = 7.5;
  CHECK(f.item(0, "score").as_number() == 7.5);
}

TEST_CASE("set_item_column_double row/column result parity") {
  auto frames = both_frames({"a", "b"});
  for (auto& frame : frames) {
    OperatorOutput out;
    out.set_item_column_double("norm", {0.25, 0.75});
    frame->apply_output(out, "op", false);
  }
  Result r1 = frames[0]->to_result({}, {"id", "norm"});
  Result r2 = frames[1]->to_result({}, {"id", "norm"});
  REQUIRE(r1.items.size() == r2.items.size());
  for (std::size_t i = 0; i < r1.items.size(); ++i) {
    CHECK(dump_json(Variant(r1.items[i]), 0) == dump_json(Variant(r2.items[i]), 0));
  }
}
