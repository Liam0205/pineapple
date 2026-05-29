#include "pine/frame.hpp"
#include "pine/row_frame.hpp"

#include <doctest/doctest.h>

#include <cmath>
#include <limits>
#include <memory>

using namespace pine;

namespace {

std::unique_ptr<RowFrame> make_row_frame() {
  std::vector<JsonValue::object_t> items;
  items.push_back({{"id", JsonValue(1.0)}, {"score", JsonValue(10.0)}});
  items.push_back({{"id", JsonValue(2.0)}, {"score", JsonValue(20.0)}});
  items.push_back({{"id", JsonValue(3.0)}, {"score", JsonValue(30.0)}});
  return std::make_unique<RowFrame>(
      JsonValue::object_t{{"region", JsonValue(std::string("us"))}}, std::move(items));
}

}  // namespace

TEST_CASE("RowFrame: construction + reads") {
  auto frame = make_row_frame();
  CHECK(frame->item_count() == 3);
  CHECK(frame->common("region").as_string() == "us");
  CHECK(frame->item(0, "id").as_number() == 1.0);
  CHECK(frame->item(2, "score").as_number() == 30.0);
  CHECK(frame->item_has(0, "score"));
  CHECK_FALSE(frame->item_has(0, "missing"));
}

TEST_CASE("RowFrame: apply_output runs 5 stages") {
  auto frame = make_row_frame();
  OperatorOutput out;
  out.set_common("region", JsonValue(std::string("eu")));
  out.set_item(0, "score", JsonValue(11.0));
  out.add_item({{"id", JsonValue(4.0)}});
  frame->apply_output(out, "op", false);
  CHECK(frame->common("region").as_string() == "eu");
  CHECK(frame->item(0, "score").as_number() == 11.0);
  CHECK(frame->item_count() == 4);
  CHECK(frame->item(3, "id").as_number() == 4.0);
}

TEST_CASE("RowFrame: apply_output rejects NaN/Inf") {
  auto frame = make_row_frame();
  OperatorOutput out;
  out.set_common("ratio", JsonValue(std::numeric_limits<double>::quiet_NaN()));
  CHECK_THROWS_AS(frame->apply_output(out, "op", false), ExecutionError);
}

TEST_CASE("RowFrame: window view + bounds check") {
  auto frame = make_row_frame();
  CHECK_THROWS_AS(frame->make_window_view(0, 4), Error);
  auto view = frame->make_window_view(1, 2);
  CHECK(view->item_count() == 2);
  CHECK(view->item(0, "id").as_number() == 2.0);
  CHECK(view->item(1, "score").as_number() == 30.0);
  CHECK(view->common("region").as_string() == "us");
  // write paths must throw on view
  CHECK_THROWS_AS(view->set_common("k", JsonValue(1.0)), Error);
  OperatorOutput empty;
  CHECK_THROWS_AS(view->apply_output(empty, "op", false), Error);
  CHECK_THROWS_AS(view->to_result({"region"}, {"id"}), Error);
}

TEST_CASE("make_frame factory selects implementation by storage_mode") {
  JsonValue::object_t common{{"r", JsonValue(std::string("v"))}};
  std::vector<JsonValue::object_t> items{{{"id", JsonValue(1.0)}}};
  auto col = make_frame("column", common, items);
  auto row = make_frame("row", common, items);
  auto fallback = make_frame("", common, items);

  // Behavioral parity — both impls expose the same logical view.
  CHECK(col->item_count() == 1);
  CHECK(row->item_count() == 1);
  CHECK(fallback->item_count() == 1);
  CHECK(col->common("r").as_string() == "v");
  CHECK(row->common("r").as_string() == "v");

  // Implementation discriminated by dynamic_cast.
  CHECK(dynamic_cast<RowFrame*>(row.get()) != nullptr);
  CHECK(dynamic_cast<RowFrame*>(col.get()) == nullptr);
  CHECK(dynamic_cast<RowFrame*>(fallback.get()) == nullptr);  // defaults to column
}
