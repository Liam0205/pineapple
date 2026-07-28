#include "pine/pine.hpp"

#include <doctest/doctest.h>

#include <limits>

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

TEST_CASE("FlatMap::reserve is a capacity hint that does not perturb semantics (L6)") {
  // FlatMap::reserve (flat_map.hpp:68) and the keys.reserve in
  // write_json_value (json_writer.hpp:166) / json_writer.cpp (29, 50, 166)
  // are pure capacity hints. They MUST NOT introduce phantom entries,
  // change size(), reorder, or alter serialization. Without this guarantee
  // dump_json's keys.reserve would silently bias output and 09-raw-byte.sh
  // / 14-byte-exact-execute.sh would lose byte-equality across runtimes.

  // Scenario A: reserve on empty map — stays empty and serializes to "{}".
  Variant::object_t a;
  a.reserve(64);
  CHECK(a.size() == 0);
  CHECK(a.empty());
  CHECK(dump_json(Variant(std::move(a)), 0) == "{}");

  // Scenario B: reserve before bulk emplace — identical to no-reserve path.
  Variant::object_t with_reserve;
  with_reserve.reserve(8);
  with_reserve.emplace("c", Variant(3.0));
  with_reserve.emplace("a", Variant(1.0));
  with_reserve.emplace("b", Variant(2.0));

  Variant::object_t no_reserve;
  no_reserve.emplace("c", Variant(3.0));
  no_reserve.emplace("a", Variant(1.0));
  no_reserve.emplace("b", Variant(2.0));

  CHECK(with_reserve.size() == 3);
  CHECK(no_reserve.size() == 3);
  CHECK(dump_json(Variant(std::move(with_reserve)), 0) == dump_json(Variant(std::move(no_reserve)), 0));
}

TEST_CASE("FlatMap::reserve after partial insertion preserves entries (L6)") {
  // reserve called *after* some inserts must keep all existing key/value
  // pairs intact and in sorted order across the capacity grow. A bug that
  // re-routed entries through a non-sort-preserving path would corrupt the
  // sorted-vector invariant silently.
  Variant::object_t obj;
  obj.emplace("delta", Variant(4.0));
  obj.emplace("alpha", Variant(1.0));

  obj.reserve(32);  // forces vector capacity grow on a populated FlatMap

  obj.emplace("charlie", Variant(3.0));
  obj.emplace("bravo", Variant(2.0));

  CHECK(obj.size() == 4);

  Variant v(std::move(obj));
  CHECK(dump_json(v, 0) == R"({"alpha":1,"bravo":2,"charlie":3,"delta":4})");
}

TEST_CASE("dump_json: numbers match Go encoding/json byte for byte (#180)") {
  // Every expected string below was produced by running json.Marshal on the
  // same float64 in Go, not derived from the spec by hand. Go's rule is
  // strconv.FormatFloat(d, 'f'|'e', -1, 64) with 'e' chosen when |x| < 1e-6 or
  // |x| >= 1e21, and precision -1 meaning shortest round-trip.
  auto emit = [](double d) { return dump_json(Variant(d), 0); };

  SUBCASE("plain decimal range keeps every digit, no exponent") {
    CHECK(emit(0.0) == "0");
    CHECK(emit(1.0) == "1");
    CHECK(emit(1.5) == "1.5");
    CHECK(emit(1e6) == "1000000");
    CHECK(emit(1e15) == "1000000000000000");
    // Beyond 2^53 but still below 1e21: Go stays in plain decimal. An earlier
    // Java guard stopped at 2^53 and fell back to "1.0E16" here, which is the
    // divergence #180 reported.
    CHECK(emit(1e16) == "10000000000000000");
    CHECK(emit(1e18) == "1000000000000000000");
    CHECK(emit(1e20) == "100000000000000000000");
    CHECK(emit(-1e20) == "-100000000000000000000");
  }

  SUBCASE("shortest round-trip, not the exact binary expansion") {
    // 1.0000000000000002e20 is exactly 100000000000000016384. Go prints the
    // shortest digits that round-trip and zero-fills, so the trailing digits
    // are 20000, not 16384. std::to_chars with chars_format::fixed prints the
    // exact value and got this wrong before the fix.
    CHECK(emit(1.0000000000000002e20) == "100000000000000020000");
  }

  SUBCASE("scientific above 1e21") {
    CHECK(emit(1e21) == "1e+21");
    CHECK(emit(1e22) == "1e+22");
    CHECK(emit(1.5e21) == "1.5e+21");
    CHECK(emit(-1e21) == "-1e+21");
  }

  SUBCASE("small magnitudes: 1e-6 is the boundary, and it is inclusive") {
    CHECK(emit(1e-5) == "0.00001");
    CHECK(emit(1e-6) == "0.000001");
    // Below 1e-6 switches to scientific. Note the exponent: strconv pads to two
    // digits ("1e-07") and encoding/json then strips one leading zero from
    // NEGATIVE exponents only, so this is "1e-7" while 1e+21 above keeps "+21".
    CHECK(emit(1e-7) == "1e-7");
    CHECK(emit(1e-9) == "1e-9");
    CHECK(emit(1e-10) == "1e-10");
  }

  SUBCASE("three-digit exponents keep all digits, no trimming") {
    CHECK(emit(1e100) == "1e+100");
    CHECK(emit(1e-100) == "1e-100");
  }

  SUBCASE("negative zero keeps its sign bit") {
    CHECK(emit(-0.0) == "-0");
  }

  SUBCASE("non-finite is not mangled into a corrupt token") {
    // Go's encoding/json refuses NaN/Inf, so there is no reference byte
    // sequence; these are the strings the pre-#180 implementation produced and
    // callers are expected to reject non-finite before serializing.
    //
    // What matters is that they are not silently corrupted. to_chars SUCCEEDS
    // on non-finite input and writes "inf"/"nan", so the errc fallback never
    // fires; those letters used to reach the decompose helper, which read 'i'
    // as a mantissa digit and 'f' as an exponent digit and emitted "i.nfe+2".
    const double inf = std::numeric_limits<double>::infinity();
    CHECK(emit(inf) == "inf");
    CHECK(emit(-inf) == "-inf");
    CHECK(emit(std::numeric_limits<double>::quiet_NaN()) == "nan");
    // The isnan guard's only observable effect is normalizing the sign: without
    // it, -NaN reaches to_chars and comes back as "-nan".
    CHECK(emit(-std::numeric_limits<double>::quiet_NaN()) == "nan");
  }

  SUBCASE("subnormals render shortest, matching Go") {
    // Verified against json.Marshal over the first 1e6 bit patterns.
    CHECK(emit(std::numeric_limits<double>::denorm_min()) == "5e-324");
    CHECK(emit(-std::numeric_limits<double>::denorm_min()) == "-5e-324");
  }
}

TEST_CASE("dump_json: object KEYS get Go's HTML-safe escaping, like values do") {
  // Values already went through write_go_string; keys went straight to
  // RapidJSON's Key(), which does not apply Go's escapes. So a key containing
  // < > & or U+2028/U+2029 diverged from Go and pine-java while the same
  // character in a VALUE did not. Reachable through any request whose field
  // names contain them.
  Variant::object_t o;
  o.emplace("a<b", Variant(1.0));
  o.emplace("c&d", Variant(2.0));
  o.emplace("e>f", Variant(3.0));
  o.emplace("g\xe2\x80\xa8h", Variant(4.0));
  std::string out = dump_json(Variant(std::move(o)), 0);
  CHECK(out ==
        R"({"a\u003cb":1,"c\u0026d":2,"e\u003ef":3,"g\u2028h":4})");
}
