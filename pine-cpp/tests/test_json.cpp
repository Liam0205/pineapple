#include "pine/pine.hpp"

#include <doctest/doctest.h>

using namespace pine;

TEST_CASE("parse_json: scalar values") {
  CHECK(parse_json("null").is_null());
  CHECK(parse_json("true").as_bool() == true);
  CHECK(parse_json("false").as_bool() == false);
  CHECK(parse_json("42").as_number() == doctest::Approx(42.0));
  CHECK(parse_json("-3.14").as_number() == doctest::Approx(-3.14));
  CHECK(parse_json("\"hello\"").as_string() == "hello");
}

TEST_CASE("parse_json: arrays and objects") {
  auto arr = parse_json("[1, 2, 3]");
  REQUIRE(arr.is_array());
  REQUIRE(arr.as_array().size() == 3);
  CHECK(arr.as_array()[0].as_number() == doctest::Approx(1.0));

  auto obj = parse_json("{\"a\": 1, \"b\": \"x\"}");
  REQUIRE(obj.is_object());
  CHECK(obj.find("a")->as_number() == doctest::Approx(1.0));
  CHECK(obj.find("b")->as_string() == "x");
  CHECK(obj.find("missing") == nullptr);
}

TEST_CASE("parse_json: escaped strings") {
  auto v = parse_json("\"line1\\nline2\\ttab\\\"quote\"");
  CHECK(v.as_string() == "line1\nline2\ttab\"quote");
}

TEST_CASE("parse_json: invalid input throws") {
  CHECK_THROWS(parse_json("{not json}"));
  CHECK_THROWS(parse_json("[1, 2,"));
  CHECK_THROWS(parse_json(""));
}

TEST_CASE("dump_json: roundtrip preserves structure") {
  auto original = parse_json(R"({"name":"pine","items":[1,2,3],"flag":true,"empty":null})");
  auto dumped = dump_json(original, 0);
  auto reparsed = parse_json(dumped);
  REQUIRE(reparsed.is_object());
  CHECK(reparsed.find("name")->as_string() == "pine");
  CHECK(reparsed.find("items")->as_array().size() == 3);
  CHECK(reparsed.find("flag")->as_bool() == true);
  CHECK(reparsed.find("empty")->is_null());
}

TEST_CASE("JsonValue truthy semantics") {
  // null and bool follow value; everything else is truthy.
  CHECK(JsonValue(nullptr).truthy() == false);
  CHECK(JsonValue(false).truthy() == false);
  CHECK(JsonValue(true).truthy() == true);
  CHECK(JsonValue(0.0).truthy() == true);
  CHECK(JsonValue(1.0).truthy() == true);
  CHECK(JsonValue(std::string("")).truthy() == true);
  CHECK(JsonValue(std::string("x")).truthy() == true);
}
