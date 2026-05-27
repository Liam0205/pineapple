#include "pine/operator.hpp"
#include "pine/operator_input.hpp"
#include "pine/pine.hpp"

#include <doctest/doctest.h>

using namespace pine;

TEST_CASE("register_operator: builtin operators are registered via static init") {
  auto names = registered_operator_names();
  CHECK(names.size() >= 17);
  CHECK(registry_entry("filter_condition") != nullptr);
  CHECK(registry_entry("transform_redis_get") != nullptr);
  CHECK(registry_entry("recall_static") != nullptr);
  CHECK(registry_entry("reorder_sort") != nullptr);
}

TEST_CASE("register_operator: traits are correct") {
  const auto* e = registry_entry("filter_truncate");
  REQUIRE(e != nullptr);
  CHECK(e->schema.type == OpType::Filter);
  CHECK(e->consumes_row_set == true);
  CHECK(e->mutates_row_set == true);
  CHECK(e->schema.params.count("top_n") == 1);
  CHECK(e->schema.params.at("top_n").required == true);
}

TEST_CASE("register_operator: factory is callable") {
  const auto* e = registry_entry("transform_size");
  REQUIRE(e != nullptr);
  REQUIRE(e->factory);
  auto instance = e->factory();
  CHECK(instance != nullptr);
}

TEST_CASE("register_operator: unknown operator returns nullptr") {
  CHECK(registry_entry("nonexistent_operator_xyz") == nullptr);
}

TEST_CASE("register_operator: duplicate throws RegistryError") {
  static const OperatorSchema dup_schema{
      .name = "filter_condition",
      .type = OpType::Filter,
      .description = "dup",
      .params = {},
  };
  struct DupOp : public Operator, public ConsumesRowSet, public MutatesRowSet {
    void execute(const OperatorInput&, OperatorOutput&) override {
    }
  };
  CHECK_THROWS_AS(register_operator_typed<DupOp>(dup_schema), RegistryError);
}

TEST_CASE("register_operator: empty name throws RegistryError") {
  static const OperatorSchema empty_schema{
      .name = "",
      .type = OpType::Filter,
      .description = "desc",
      .params = {},
  };
  struct EmptyNameOp : public Operator {
    void execute(const OperatorInput&, OperatorOutput&) override {
    }
  };
  CHECK_THROWS_AS(register_operator_typed<EmptyNameOp>(empty_schema), RegistryError);
}

TEST_CASE("register_operator: null factory throws RegistryError") {
  static const OperatorSchema null_schema{
      .name = "test_null_factory",
      .type = OpType::Filter,
      .description = "desc",
      .params = {},
  };
  CHECK_THROWS_AS(register_operator_with_traits(null_schema, nullptr, false, false, false, false),
                  RegistryError);
}
