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

TEST_CASE("Variant truthy semantics") {
  // null and bool follow value; everything else is truthy.
  CHECK(Variant(nullptr).truthy() == false);
  CHECK(Variant(false).truthy() == false);
  CHECK(Variant(true).truthy() == true);
  CHECK(Variant(0.0).truthy() == true);
  CHECK(Variant(1.0).truthy() == true);
  CHECK(Variant(std::string("")).truthy() == true);
  CHECK(Variant(std::string("x")).truthy() == true);
}

TEST_CASE("dump_json: object keys serialize in sorted order regardless of insertion order (L5)") {
  // Locks the invariant that pine-cpp's JSON output sorts object keys
  // lexicographically before emit, matching Go encoding/json + Java Jackson
  // with sortKeysOnSerialize. The FieldMap backend (sorted FlatMap by default,
  // unordered_map under PINE_USE_HASH_MAP=ON) is a benchmark A/B knob that
  // must not be observable on output. A regression here would silently
  // break cross-runtime byte-equal parity in 09-raw-byte.sh and
  // 14-byte-exact-execute.sh.
  Variant::object_t obj;
  // Insert keys in deliberately reverse-lexicographic order so the test is
  // sensitive to "writer iterates underlying map in insertion order" bugs.
  obj.emplace("zeta", Variant(1.0));
  obj.emplace("mu", Variant(2.0));
  obj.emplace("beta", Variant(3.0));
  obj.emplace("alpha", Variant(4.0));

  Variant v(std::move(obj));
  std::string out = dump_json(v, 0);
  CHECK(out == R"({"alpha":4,"beta":3,"mu":2,"zeta":1})");
}

TEST_CASE("dump_json: nested objects all sort keys (L5)") {
  // The sort applies recursively — every object_t at every depth must emit
  // sorted. A bug that only sorts the top level would slip past the flat
  // sibling check above.
  Variant::object_t inner;
  inner.emplace("z", Variant(true));
  inner.emplace("a", Variant(false));

  Variant::object_t outer;
  outer.emplace("y", Variant(std::move(inner)));
  outer.emplace("x", Variant(std::string("hi")));

  Variant v(std::move(outer));
  std::string out = dump_json(v, 0);
  CHECK(out == R"({"x":"hi","y":{"a":false,"z":true}})");
}
