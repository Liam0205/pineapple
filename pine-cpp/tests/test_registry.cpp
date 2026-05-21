#include <doctest/doctest.h>

#include "pine/pine.hpp"

using namespace pine;

TEST_CASE("registry_lookup: returns traits for known operators") {
    const auto* traits = registry_lookup("filter_truncate");
    REQUIRE(traits != nullptr);
    CHECK(traits->operator_type == "filter");

    traits = registry_lookup("transform_copy");
    REQUIRE(traits != nullptr);
    CHECK(traits->operator_type == "transform");

    traits = registry_lookup("recall_static");
    REQUIRE(traits != nullptr);
    CHECK(traits->operator_type == "recall");
}

TEST_CASE("registry_lookup: returns null for unknown type") {
    CHECK(registry_lookup("definitely_not_a_real_operator") == nullptr);
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
