#include "pine/column.hpp"
#include "pine/column_store.hpp"

#include <doctest/doctest.h>

using namespace pine;

TEST_CASE("make_column: int values -> Int64Column") {
  std::vector<Variant> vs{Variant(1.0), Variant(2.0), Variant(3.0)};
  auto col = make_column(vs);
  CHECK(col->type() == ColumnType::Int64);
  CHECK(col->size() == 3);
  CHECK(col->get(0).as_number() == 1.0);
  CHECK(col->get(2).as_number() == 3.0);
}

TEST_CASE("make_column: mixed int/double widens to DoubleColumn") {
  std::vector<Variant> vs{Variant(1.0), Variant(2.5)};
  auto col = make_column(vs);
  CHECK(col->type() == ColumnType::Double);
  CHECK(col->size() == 2);
  CHECK(col->get(1).as_number() == 2.5);
}

TEST_CASE("make_column: string values -> StringColumn") {
  std::vector<Variant> vs{Variant(std::string("a")), Variant(std::string("b"))};
  auto col = make_column(vs);
  CHECK(col->type() == ColumnType::String);
  CHECK(col->get(0).as_string() == "a");
}

TEST_CASE("make_column: bool values -> BoolColumn") {
  std::vector<Variant> vs{Variant(true), Variant(false)};
  auto col = make_column(vs);
  CHECK(col->type() == ColumnType::Bool);
  CHECK(col->get(0).as_bool() == true);
  CHECK(col->get(1).as_bool() == false);
}

TEST_CASE("make_column: heterogeneous types -> JsonColumn") {
  std::vector<Variant> vs{Variant(1.0), Variant(std::string("s"))};
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
  std::vector<Variant> vs{Variant(1.0), Variant(), Variant(3.0)};
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
  REQUIRE(col->append(Variant(1.0)));
  col->append_null();
  REQUIRE(col->append(Variant(3.0)));
  CHECK(col->is_present(0));
  CHECK_FALSE(col->is_present(1));
  CHECK(col->is_present(2));
  CHECK(col->is_null(1));
}

TEST_CASE("TypedColumn::set type-mismatch returns false (caller promotes)") {
  auto col = std::make_unique<Int64Column>(3);
  CHECK(col->set(0, Variant(42.0)));
  CHECK_FALSE(col->set(0, Variant(std::string("nope"))));
}

TEST_CASE("Column::append + remove + reorder") {
  std::vector<Variant> vs{Variant(10.0), Variant(20.0), Variant(30.0)};
  auto col = make_column(vs);
  REQUIRE(col->append(Variant(40.0)));
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
  std::vector<Variant> vs{Variant(1.0), Variant(2.0)};
  auto col = make_column(vs);
  auto copy = col->clone();
  REQUIRE(copy->set(0, Variant(99.0)));
  CHECK(col->get(0).as_number() == 1.0);
  CHECK(copy->get(0).as_number() == 99.0);
}

TEST_CASE("Column::to_json_column promotes typed -> Json preserving values + nulls") {
  // Build an Int64Column with [1, ABSENT, 3] (via append + append_null)
  // and verify promotion to JsonColumn preserves presence semantics.
  auto typed = std::make_unique<Int64Column>();
  REQUIRE(typed->append(Variant(1.0)));
  typed->append_null();
  REQUIRE(typed->append(Variant(3.0)));
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
  REQUIRE(col.append(Variant(1.0)));
  REQUIRE(col.append(Variant(std::string("s"))));
  REQUIRE(col.append(Variant(true)));
  REQUIRE(col.append(Variant()));
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
  // self-defend.
  pine::TypedColumnStore store(3);
  std::vector<pine::Variant> vs{pine::Variant(1.0), pine::Variant(2.0), pine::Variant(3.0)};
  store.set_column("x", pine::make_column(vs));

  CHECK_THROWS_AS(store.remove_rows({3}), std::invalid_argument);
  CHECK_THROWS_AS(store.remove_rows({-1}), std::invalid_argument);
  CHECK_THROWS_AS(store.remove_rows({0, 5}), std::invalid_argument);

  // Valid path still works and keeps row_count_ + column sizes in sync.
  store.remove_rows({1});
  CHECK(store.row_count() == 2);
  CHECK(store.column("x")->size() == 2);
}

TEST_CASE("TypedColumnStore::reorder_rows shares visited bitmap across K>2 columns") {
  // The store hoists the cycle-following visited bitmap out of the per-column
  // call so K columns share one buffer (column_store.cpp:91-94). The frame
  // tests in test_column_frame.cpp exercise ≤2 columns; with that few, a
  // bug in reset-between-columns hides easily because the cycle structure
  // tends to be trivial. This case uses 5 typed columns of mixed dtypes
  // and a permutation with multiple non-trivial cycles, so any failure to
  // reset visited_scratch between columns would leave the second-onwards
  // column in a partially-permuted state.
  const std::size_t n = 6;
  pine::TypedColumnStore store(n);

  // 5 columns of three dtypes — exercises Int64/Double/String/Bool reorder
  // paths plus a JsonColumn (heterogeneous values).
  std::vector<pine::Variant> ints{
      pine::Variant(10.0), pine::Variant(11.0), pine::Variant(12.0),
      pine::Variant(13.0), pine::Variant(14.0), pine::Variant(15.0),
  };
  std::vector<pine::Variant> doubles{
      pine::Variant(0.5), pine::Variant(1.5), pine::Variant(2.5),
      pine::Variant(3.5), pine::Variant(4.5), pine::Variant(5.5),
  };
  std::vector<pine::Variant> strings{
      pine::Variant(std::string("a")), pine::Variant(std::string("b")), pine::Variant(std::string("c")),
      pine::Variant(std::string("d")), pine::Variant(std::string("e")), pine::Variant(std::string("f")),
  };
  std::vector<pine::Variant> bools{
      pine::Variant(true),  pine::Variant(false), pine::Variant(true),
      pine::Variant(false), pine::Variant(true),  pine::Variant(false),
  };
  // Heterogeneous → JsonColumn.
  std::vector<pine::Variant> jsons{
      pine::Variant(1.0), pine::Variant(std::string("two")), pine::Variant(true), pine::Variant(),
      pine::Variant(4.0), pine::Variant(std::string("six")),
  };

  store.set_column("i", pine::make_column(ints));
  store.set_column("d", pine::make_column(doubles));
  store.set_column("s", pine::make_column(strings));
  store.set_column("b", pine::make_column(bools));
  store.set_column("j", pine::make_column(jsons));

  // Permutation with two non-trivial cycles plus one fixed point:
  //   cycle A: 0 → 2 → 4 → 0
  //   cycle B: 1 → 3 → 1
  //   fixed:   5 → 5
  // After the reorder, new[i] == old[order[i]].
  std::vector<int> order = {2, 3, 4, 1, 0, 5};
  store.reorder_rows(order);

  // Expected per-column values after permutation.
  const std::vector<double> exp_i = {12, 13, 14, 11, 10, 15};
  const std::vector<double> exp_d = {2.5, 3.5, 4.5, 1.5, 0.5, 5.5};
  const std::vector<std::string> exp_s = {"c", "d", "e", "b", "a", "f"};
  const std::vector<bool> exp_b = {true, false, true, false, true, false};

  for (std::size_t i = 0; i < n; ++i) {
    CHECK(store.column("i")->get(i).as_number() == exp_i[i]);
    CHECK(store.column("d")->get(i).as_number() == exp_d[i]);
    CHECK(store.column("s")->get(i).as_string() == exp_s[i]);
    CHECK(store.column("b")->get(i).as_bool() == exp_b[i]);
  }
  // JsonColumn carries the heterogeneous mix; spot-check that each
  // permuted position holds jsons[order[i]] under the same permutation.
  // exp_j[0] = jsons[2] = true
  CHECK(store.column("j")->get(0).as_bool() == true);
  // exp_j[1] = jsons[3] = null
  CHECK(store.column("j")->is_null(1));
  // exp_j[2] = jsons[4] = 4.0
  CHECK(store.column("j")->get(2).as_number() == 4.0);
  // exp_j[3] = jsons[1] = "two"
  CHECK(store.column("j")->get(3).as_string() == "two");
  // exp_j[4] = jsons[0] = 1.0
  CHECK(store.column("j")->get(4).as_number() == 1.0);
  // exp_j[5] = jsons[5] = "six"
  CHECK(store.column("j")->get(5).as_string() == "six");
}

TEST_CASE("Derived-class static type can call 1-arg reorder(order) (name-hiding fix)") {
  // Regression for 0dec52a: TypedColumn/JsonColumn override the 2-arg
  // reorder(order, visited_scratch); by C++ name-hiding rules, that
  // suppresses the base-class 1-arg convenience overload reorder(order)
  // unless the derived class re-imports it via `using Column::reorder`.
  // All callers in the runtime go through Column* / unique_ptr<Column>,
  // so the typed-static-type call path is exercised only here.
  // Without `using Column::reorder` in each derived class, this test
  // would fail to compile.
  {
    Int64Column c(3);
    REQUIRE(c.set(0, Variant(10.0)));
    REQUIRE(c.set(1, Variant(20.0)));
    REQUIRE(c.set(2, Variant(30.0)));
    c.reorder({2, 0, 1});
    CHECK(c.get(0).as_number() == 30.0);
    CHECK(c.get(1).as_number() == 10.0);
    CHECK(c.get(2).as_number() == 20.0);
  }
  {
    StringColumn c(3);
    REQUIRE(c.set(0, Variant(std::string("a"))));
    REQUIRE(c.set(1, Variant(std::string("b"))));
    REQUIRE(c.set(2, Variant(std::string("c"))));
    c.reorder({2, 0, 1});
    CHECK(c.get(0).as_string() == "c");
    CHECK(c.get(1).as_string() == "a");
    CHECK(c.get(2).as_string() == "b");
  }
  {
    JsonColumn c;
    REQUIRE(c.append(Variant(1.0)));
    REQUIRE(c.append(Variant(std::string("two"))));
    REQUIRE(c.append(Variant(true)));
    c.reorder({2, 0, 1});
    CHECK(c.get(0).as_bool() == true);
    CHECK(c.get(1).as_number() == 1.0);
    CHECK(c.get(2).as_string() == "two");
  }
}

TEST_CASE("Int64Column precision boundary detection") {
  using pine::int64_lossy_as_double;
  // Within IEEE 754 binary64 precise range: 0..2^53
  CHECK_FALSE(int64_lossy_as_double(0));
  CHECK_FALSE(int64_lossy_as_double(1LL << 53));
  CHECK_FALSE(int64_lossy_as_double(-(1LL << 53)));
  CHECK_FALSE(int64_lossy_as_double(9007199254740992LL));  // 2^53 exactly
  // Beyond: precision loss in double round-trip
  CHECK(int64_lossy_as_double(9007199254740993LL));  // 2^53 + 1
  CHECK(int64_lossy_as_double(-9007199254740993LL));
  CHECK(int64_lossy_as_double(INT64_MAX));
  CHECK(int64_lossy_as_double(INT64_MIN));
}
