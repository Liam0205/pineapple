#include "pine/operator.hpp"
#include "pine/pine.hpp"

#include <doctest/doctest.h>

using namespace pine;

TEST_CASE("registry: returns entry for known operators") {
  const auto* entry = registry_entry("filter_truncate");
  REQUIRE(entry != nullptr);
  CHECK(op_type_to_string(entry->schema.type) == std::string("filter"));

  entry = registry_entry("transform_copy");
  REQUIRE(entry != nullptr);
  CHECK(op_type_to_string(entry->schema.type) == std::string("transform"));

  entry = registry_entry("recall_static");
  REQUIRE(entry != nullptr);
  CHECK(op_type_to_string(entry->schema.type) == std::string("recall"));
}

TEST_CASE("registry: returns null for unknown type") {
  CHECK(registry_entry("definitely_not_a_real_operator") == nullptr);
}

TEST_CASE("export_schema_json: produces JSON parseable as an array") {
  std::string schema = export_schema_json();
  auto v = parse_json(schema);
  REQUIRE(v.is_array());
  CHECK(v.as_array().size() > 0);

  // First entry should be an object with at least "Name" and "Params".
  const auto& first = v.as_array().front();
  REQUIRE(first.is_object());
  CHECK(first.find("Name") != nullptr);
  CHECK(first.find("Params") != nullptr);
}

TEST_CASE("export_schema_json: every entry has stable shape") {
  auto v = parse_json(export_schema_json());
  REQUIRE(v.is_array());
  for (const auto& entry : v.as_array()) {
    REQUIRE(entry.is_object());
    REQUIRE(entry.find("Name") != nullptr);
    REQUIRE(entry.find("Name")->is_string());
    REQUIRE(entry.find("Params") != nullptr);
  }
}
