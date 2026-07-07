// item_column batch access — element i must be identical to the
// per-element item(i, field) path across both Frame implementations,
// including item-default substitution at the OperatorInput layer and
// window-view offset translation. Mirrors pine-go item_column_test.go
// and pine-java ItemColumnTest.
#include "pine/column_frame.hpp"
#include "pine/frame.hpp"
#include "pine/operator_input.hpp"
#include "pine/row_frame.hpp"

#include <doctest/doctest.h>

#include <memory>
#include <string>
#include <vector>

using namespace pine;

namespace {

std::vector<Variant::object_t> sample_items() {
  std::vector<Variant::object_t> items;
  Variant::object_t r0;
  r0["a"] = Variant(1.0);
  r0["b"] = Variant(std::string("x"));
  items.push_back(r0);
  Variant::object_t r1;
  r1["a"] = Variant(2.0);  // b missing
  items.push_back(r1);
  Variant::object_t r2;
  r2["a"] = Variant(nullptr);
  r2["b"] = Variant(std::string("z"));
  items.push_back(r2);
  return items;
}

std::vector<std::unique_ptr<Frame>> both_frames() {
  std::vector<std::unique_ptr<Frame>> frames;
  frames.push_back(std::make_unique<RowFrame>(Variant::object_t{}, sample_items()));
  frames.push_back(std::make_unique<ColumnFrame>(Variant::object_t{}, sample_items()));
  return frames;
}

}  // namespace

TEST_CASE("item_column matches per-element item() on both frames") {
  for (auto& frame : both_frames()) {
    InputFieldSpec spec;
    spec.nullable_item.push_back("a");
    OperatorInput in(*frame, spec);
    for (const std::string field : {"a", "b", "absent"}) {
      std::vector<Variant> col = in.item_column(field);
      REQUIRE(col.size() == in.item_count());
      for (std::size_t i = 0; i < col.size(); ++i) {
        CHECK(dump_json(col[i], 0) == dump_json(in.item(i, field), 0));
      }
    }
  }
}

TEST_CASE("item_column applies item defaults to nil slots") {
  std::vector<Variant::object_t> items;
  Variant::object_t r0;
  r0["score"] = Variant(1.0);
  items.push_back(r0);
  Variant::object_t r1;
  r1["score"] = Variant(nullptr);
  items.push_back(r1);
  items.push_back(Variant::object_t{});  // missing entirely

  std::vector<std::unique_ptr<Frame>> frames;
  frames.push_back(std::make_unique<RowFrame>(Variant::object_t{}, items));
  frames.push_back(std::make_unique<ColumnFrame>(Variant::object_t{}, items));
  for (auto& frame : frames) {
    InputFieldSpec spec;
    spec.defaulted_item.push_back({"score", Variant(-1.0)});
    OperatorInput in(*frame, spec);
    std::vector<Variant> col = in.item_column("score");
    REQUIRE(col.size() == 3);
    CHECK(col[0].as_number() == 1.0);
    CHECK(col[1].as_number() == -1.0);
    CHECK(col[2].as_number() == -1.0);
    for (std::size_t i = 0; i < 3; ++i) {
      CHECK(dump_json(col[i], 0) == dump_json(in.item(i, "score"), 0));
    }
  }
}

TEST_CASE("item_column translates window-view offsets") {
  std::vector<Variant::object_t> items;
  for (int i = 0; i < 10; ++i) {
    Variant::object_t row;
    row["v"] = Variant(static_cast<double>(i));
    items.push_back(row);
  }
  std::vector<std::unique_ptr<Frame>> frames;
  frames.push_back(std::make_unique<RowFrame>(Variant::object_t{}, items));
  frames.push_back(std::make_unique<ColumnFrame>(Variant::object_t{}, items));
  for (auto& frame : frames) {
    std::unique_ptr<Frame> view = frame->make_window_view(3, 4);
    std::vector<Variant> col = view->item_column("v");
    REQUIRE(col.size() == 4);
    for (std::size_t i = 0; i < 4; ++i) {
      CHECK(col[i].as_number() == static_cast<double>(3 + i));
    }
  }
}

TEST_CASE("item_column of absent field is all nulls") {
  for (auto& frame : both_frames()) {
    std::vector<Variant> col = frame->item_column("nope");
    REQUIRE(col.size() == frame->item_count());
    for (const auto& v : col) {
      CHECK(v.is_null());
    }
  }
}
