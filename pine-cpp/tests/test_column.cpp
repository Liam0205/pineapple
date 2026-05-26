#include <doctest/doctest.h>

#include "pine/column.hpp"
#include "pine/column_store.hpp"

using namespace pine;

TEST_CASE("make_column: int values -> Int64Column") {
    std::vector<JsonValue> vs{JsonValue(1.0), JsonValue(2.0), JsonValue(3.0)};
    auto col = make_column(vs);
    CHECK(col->type() == ColumnType::Int64);
    CHECK(col->size() == 3);
    CHECK(col->get(0).as_number() == 1.0);
    CHECK(col->get(2).as_number() == 3.0);
}

TEST_CASE("make_column: mixed int/double widens to DoubleColumn") {
    std::vector<JsonValue> vs{JsonValue(1.0), JsonValue(2.5)};
    auto col = make_column(vs);
    CHECK(col->type() == ColumnType::Double);
    CHECK(col->size() == 2);
    CHECK(col->get(1).as_number() == 2.5);
}

TEST_CASE("make_column: string values -> StringColumn") {
    std::vector<JsonValue> vs{JsonValue(std::string("a")), JsonValue(std::string("b"))};
    auto col = make_column(vs);
    CHECK(col->type() == ColumnType::String);
    CHECK(col->get(0).as_string() == "a");
}

TEST_CASE("make_column: bool values -> BoolColumn") {
    std::vector<JsonValue> vs{JsonValue(true), JsonValue(false)};
    auto col = make_column(vs);
    CHECK(col->type() == ColumnType::Bool);
    CHECK(col->get(0).as_bool() == true);
    CHECK(col->get(1).as_bool() == false);
}

TEST_CASE("make_column: heterogeneous types -> JsonColumn") {
    std::vector<JsonValue> vs{JsonValue(1.0), JsonValue(std::string("s"))};
    auto col = make_column(vs);
    CHECK(col->type() == ColumnType::Json);
    CHECK(col->get(0).as_number() == 1.0);
    CHECK(col->get(1).as_string() == "s");
}

TEST_CASE("make_column: empty -> JsonColumn size 0") {
    auto col = make_column({});
    CHECK(col->type() == ColumnType::Json);
    CHECK(col->size() == 0);
}

TEST_CASE("Column: presence bitmap distinguishes absent / present-null / present-value") {
    // {1, null, 3}: null is treated as present-null, so make_column produces
    // a JsonColumn (typed columns cannot represent present-null).
    std::vector<JsonValue> vs{JsonValue(1.0), JsonValue(), JsonValue(3.0)};
    auto col = make_column(vs);
    CHECK(col->type() == ColumnType::Json);
    CHECK(col->is_null(1));
    CHECK_FALSE(col->is_null(0));
    CHECK_FALSE(col->is_null(2));
    CHECK(col->get(1).is_null());
    // All three slots are explicitly present (the null is a present-null).
    CHECK(col->is_present(0));
    CHECK(col->is_present(1));
    CHECK(col->is_present(2));
}

TEST_CASE("Column: append_null marks slot ABSENT (validity=false)") {
    auto col = std::make_unique<Int64Column>();
    REQUIRE(col->append(JsonValue(1.0)));
    col->append_null();
    REQUIRE(col->append(JsonValue(3.0)));
    CHECK(col->is_present(0));
    CHECK_FALSE(col->is_present(1));
    CHECK(col->is_present(2));
    CHECK(col->is_null(1));
}

TEST_CASE("TypedColumn::set type-mismatch returns false (caller promotes)") {
    auto col = std::make_unique<Int64Column>(3);
    CHECK(col->set(0, JsonValue(42.0)));
    CHECK_FALSE(col->set(0, JsonValue(std::string("nope"))));
}

TEST_CASE("Column::append + remove + reorder") {
    std::vector<JsonValue> vs{JsonValue(10.0), JsonValue(20.0), JsonValue(30.0)};
    auto col = make_column(vs);
    REQUIRE(col->append(JsonValue(40.0)));
    CHECK(col->size() == 4);

    col->remove({1});  // drop 20.0
    CHECK(col->size() == 3);
    CHECK(col->get(1).as_number() == 30.0);

    col->reorder({2, 0, 1});  // [40, 10, 30]
    CHECK(col->get(0).as_number() == 40.0);
    CHECK(col->get(1).as_number() == 10.0);
    CHECK(col->get(2).as_number() == 30.0);
}

TEST_CASE("Column::clone produces independent copy") {
    std::vector<JsonValue> vs{JsonValue(1.0), JsonValue(2.0)};
    auto col = make_column(vs);
    auto copy = col->clone();
    REQUIRE(copy->set(0, JsonValue(99.0)));
    CHECK(col->get(0).as_number() == 1.0);
    CHECK(copy->get(0).as_number() == 99.0);
}

TEST_CASE("Column::to_json_column promotes typed -> Json preserving values + nulls") {
    // Build an Int64Column with [1, ABSENT, 3] (via append + append_null)
    // and verify promotion to JsonColumn preserves presence semantics.
    auto typed = std::make_unique<Int64Column>();
    REQUIRE(typed->append(JsonValue(1.0)));
    typed->append_null();
    REQUIRE(typed->append(JsonValue(3.0)));
    REQUIRE(typed->type() == ColumnType::Int64);
    auto json_col = typed->to_json_column();
    CHECK(json_col->type() == ColumnType::Json);
    CHECK(json_col->get(0).as_number() == 1.0);
    CHECK(json_col->is_null(1));
    CHECK_FALSE(json_col->is_present(1));
    CHECK(json_col->get(2).as_number() == 3.0);
}

TEST_CASE("JsonColumn accepts arbitrary types") {
    JsonColumn col;
    REQUIRE(col.append(JsonValue(1.0)));
    REQUIRE(col.append(JsonValue(std::string("s"))));
    REQUIRE(col.append(JsonValue(true)));
    REQUIRE(col.append(JsonValue()));
    CHECK(col.size() == 4);
    CHECK(col.get(0).as_number() == 1.0);
    CHECK(col.get(1).as_string() == "s");
    CHECK(col.get(2).as_bool());
    CHECK(col.is_null(3));
}

TEST_CASE("TypedColumnStore::remove_rows rejects OOB indices") {
    // Direct public-API contract: caller must not feed indices outside
    // [0, row_count_). The ColumnFrame::apply_output path pre-validates,
    // but the store surface is reachable from other callers and must
    // self-defend (tracked as P1-S2).
    pine::TypedColumnStore store(3);
    std::vector<pine::JsonValue> vs{
        pine::JsonValue(1.0), pine::JsonValue(2.0), pine::JsonValue(3.0)};
    store.set_column("x", pine::make_column(vs));

    CHECK_THROWS_AS(store.remove_rows({3}), std::invalid_argument);
    CHECK_THROWS_AS(store.remove_rows({-1}), std::invalid_argument);
    CHECK_THROWS_AS(store.remove_rows({0, 5}), std::invalid_argument);

    // Valid path still works and keeps row_count_ + column sizes in sync.
    store.remove_rows({1});
    CHECK(store.row_count() == 2);
    CHECK(store.column("x")->size() == 2);
}

TEST_CASE("Int64Column precision boundary detection (P1-S5)") {
    using pine::int64_lossy_as_double;
    // Within IEEE 754 binary64 precise range: 0..2^53
    CHECK_FALSE(int64_lossy_as_double(0));
    CHECK_FALSE(int64_lossy_as_double(1LL << 53));
    CHECK_FALSE(int64_lossy_as_double(-(1LL << 53)));
    CHECK_FALSE(int64_lossy_as_double(9007199254740992LL));   // 2^53 exactly
    // Beyond: precision loss in double round-trip
    CHECK(int64_lossy_as_double(9007199254740993LL));         // 2^53 + 1
    CHECK(int64_lossy_as_double(-9007199254740993LL));
    CHECK(int64_lossy_as_double(INT64_MAX));
    CHECK(int64_lossy_as_double(INT64_MIN));
}
