#include <doctest/doctest.h>

#include "operators/_helpers.hpp"

using pine::operators::go_format_g;

TEST_CASE("go_format_g matches Go strconv.FormatFloat('g', -1, 64) at long-integer boundaries") {
  // Each line below was captured from `strconv.FormatFloat(v, 'g', -1, 64)`
  // in Go 1.22 — Go's exact source of truth.
  CHECK(go_format_g(100.0) == "100");
  CHECK(go_format_g(100000.0) == "100000");
  CHECK(go_format_g(999999.0) == "999999");
  CHECK(go_format_g(1000000.0) == "1e+06");
  CHECK(go_format_g(10000000.0) == "1e+07");
  CHECK(go_format_g(100000000.0) == "1e+08");
  CHECK(go_format_g(123456789.0) == "1.23456789e+08");
  CHECK(go_format_g(12345678.0) == "1.2345678e+07");
  CHECK(go_format_g(1234567.0) == "1.234567e+06");
  CHECK(go_format_g(0.0001) == "0.0001");
  CHECK(go_format_g(0.00001) == "1e-05");
  CHECK(go_format_g(1e21) == "1e+21");
  CHECK(go_format_g(0.0) == "0");
  CHECK(go_format_g(-0.0) == "-0");
  CHECK(go_format_g(-100.0) == "-100");
  CHECK(go_format_g(0.1) == "0.1");
  CHECK(go_format_g(1.5) == "1.5");
}

TEST_CASE("go_format_lookup_key matches Go FormatInt / FormatFloat('f', -1)") {
  using pine::operators::go_format_lookup_key;
  // Integer-valued floats: FormatInt — no decimal point.
  CHECK(go_format_lookup_key(0.0) == "0");
  CHECK(go_format_lookup_key(1.0) == "1");
  CHECK(go_format_lookup_key(-1.0) == "-1");
  CHECK(go_format_lookup_key(100000.0) == "100000");
  CHECK(go_format_lookup_key(1e7) == "10000000");
  CHECK(go_format_lookup_key(1e15) == "1000000000000000");

  // Non-integer floats: FormatFloat('f', -1) — never scientific.
  // The exact byte-level comparison against Go FormatFloat is constrained:
  // both produce the shortest round-tripping representation, with the
  // 'f' format guaranteeing no exponent. The parity-critical case
  // is 1e-5 — Go renders "0.00001"; C++ go_format_g would render "1e-05"
  // and miss the lookup.
  CHECK(go_format_lookup_key(0.5) == "0.5");
  CHECK(go_format_lookup_key(0.1) == "0.1");
  CHECK(go_format_lookup_key(-0.5) == "-0.5");
  CHECK(go_format_lookup_key(1.5) == "1.5");
  CHECK(go_format_lookup_key(1e-5).find('e') == std::string::npos);
  CHECK(go_format_lookup_key(1e-5).find('E') == std::string::npos);
  CHECK(go_format_lookup_key(1e-5) == "0.00001");
}
