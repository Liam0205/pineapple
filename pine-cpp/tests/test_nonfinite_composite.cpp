// Issue #210: NaN/±Inf nested inside a composite value used to pass the
// write-time check (only scalars were inspected) and surfaced as bare `inf`
// in the response — invalid JSON — while pine-go failed to encode and
// pine-java wrote a quoted "Infinity". Composites are now scanned with the
// scalar message. Mirrors pine-go nonfinite_composite_test.go and pine-java
// NonFiniteCompositeTest.
#include "pine/column_frame.hpp"
#include "pine/frame.hpp"
#include "pine/row_frame.hpp"

#include <doctest/doctest.h>

#include <limits>
#include <memory>
#include <string>
#include <vector>

#include "dataframe/frame_values.hpp"

using namespace pine;

namespace {

std::vector<std::unique_ptr<Frame>> two_item_frames() {
  auto items = [] {
    std::vector<Variant::object_t> v;
    for (const char* id : {"a", "b"}) {
      Variant::object_t row;
      row["id"] = Variant(std::string(id));
      v.push_back(row);
    }
    return v;
  };
  std::vector<std::unique_ptr<Frame>> frames;
  frames.push_back(std::make_unique<RowFrame>(Variant::object_t{}, items()));
  frames.push_back(std::make_unique<ColumnFrame>(Variant::object_t{}, items()));
  return frames;
}

std::vector<Variant> non_finite_composites() {
  const double inf = std::numeric_limits<double>::infinity();
  const double nan = std::numeric_limits<double>::quiet_NaN();
  Variant::object_t m;
  m["a"] = Variant(1.0);
  m["b"] = Variant(-inf);
  Variant::object_t inner;
  inner["x"] = Variant(Variant::array_t{Variant(nan)});
  return {Variant(Variant::array_t{Variant(2.0), Variant(inf)}), Variant(m),
          Variant(Variant::array_t{Variant(inner)})};
}

}  // namespace

TEST_CASE("apply_output rejects NaN/Inf nested inside a composite") {
  const std::string prefix = "pine: execution error in operator \"op\": ";
  for (const auto& value : non_finite_composites()) {
    for (auto& frame : two_item_frames()) {
      OperatorOutput item;
      item.set_item(1, "r", value);
      CHECK_THROWS_WITH_AS(frame->apply_output(item, "op", false),
                           (prefix + "item[1] write: field \"r\": NaN/Inf is not a valid JSON value").c_str(),
                           ExecutionError);

      OperatorOutput common;
      common.set_common("c", value);
      CHECK_THROWS_WITH_AS(frame->apply_output(common, "op", false),
                           (prefix + "common write: field \"c\": NaN/Inf is not a valid JSON value").c_str(),
                           ExecutionError);

      OperatorOutput added;
      Variant::object_t row;
      row["id"] = Variant(std::string("c"));
      row["r"] = value;
      added.add_item(row);
      CHECK_THROWS_WITH_AS(
          frame->apply_output(added, "op", true),
          (prefix + "added item write: field \"r\": NaN/Inf is not a valid JSON value").c_str(),
          ExecutionError);
    }
  }
}

TEST_CASE("apply_output accepts a finite composite") {
  Variant::object_t m;
  m["k"] = Variant(std::numeric_limits<double>::max());
  Variant value(Variant::array_t{Variant(1.0), Variant("x"), Variant(nullptr), Variant(true), Variant(m)});
  for (auto& frame : two_item_frames()) {
    OperatorOutput out;
    out.set_item(0, "r", value);
    CHECK_NOTHROW(frame->apply_output(out, "op", false));
  }
}

TEST_CASE("a too-deep sibling does not hide a shallow non-finite value") {
  Variant deep(0.0);
  for (int i = 0; i <= detail::kMaxCompositeScanDepth; ++i) {
    deep = Variant(Variant::array_t{deep});
  }
  // "a" sorts before "bad", so the too-deep subtree is visited first.
  Variant::object_t m;
  m["a"] = deep;
  m["bad"] = Variant(std::numeric_limits<double>::infinity());
  CHECK_FALSE(detail::validate_frame_value("f", Variant(m)).empty());
}

TEST_CASE("non-finite scan stops at the depth bound") {
  // One level past the bound: the NaN is never reached, matching pine-go and
  // pine-java, which give up at the same depth.
  Variant deep(std::numeric_limits<double>::quiet_NaN());
  for (int i = 0; i <= detail::kMaxCompositeScanDepth; ++i) {
    deep = Variant(Variant::array_t{deep});
  }
  CHECK(detail::validate_frame_value("f", deep).empty());

  Variant shallow(std::numeric_limits<double>::quiet_NaN());
  for (int i = 0; i < detail::kMaxCompositeScanDepth; ++i) {
    shallow = Variant(Variant::array_t{shallow});
  }
  CHECK_FALSE(detail::validate_frame_value("f", shallow).empty());
}
