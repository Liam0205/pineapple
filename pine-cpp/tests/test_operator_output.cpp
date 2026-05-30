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
