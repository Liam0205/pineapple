#include "pine/pine.hpp"
#include "pine/operator.hpp"
#include "pine/operator_input.hpp"

#include <doctest/doctest.h>

#include <memory>
#include <map>
#include <string>

using namespace pine;

namespace {
using pine::Engine; // Fix ambiguity with pine::Engine/Engine

// Provide a global accessor to inject stats
class StatsMockOp;
StatsMockOp* g_mock_op = nullptr;

class StatsMockOp : public Operator, public StatsProvider {
public:
    std::map<std::string, int64_t> stats;
    StatsMockOp() { g_mock_op = this; }
    ~StatsMockOp() { if (g_mock_op == this) g_mock_op = nullptr; }
    void execute(const OperatorInput&, OperatorOutput&) override {}
    std::map<std::string, int64_t> operator_stats() const override {
        return stats;
    }
};

class EmptyStatsMockOp : public Operator, public StatsProvider {
public:
    void execute(const OperatorInput&, OperatorOutput&) override {}
    std::map<std::string, int64_t> operator_stats() const override {
        return {}; // Exposes StatsProvider but returns empty map
    }
};

void run_test() {
    OperatorSchema schema1{
        .name = "test_stats_mock",
        .type = OpType::Recall,
        .description = "mock",
        .params = {},
    };
    register_operator(std::move(schema1), [] { return std::make_unique<StatsMockOp>(); });

    OperatorSchema schema2{
        .name = "test_empty_stats_mock",
        .type = OpType::Recall,
        .description = "mock",
        .params = {},
    };
    register_operator(std::move(schema2), [] { return std::make_unique<EmptyStatsMockOp>(); });
}
const bool _reg = (run_test(), true);

const char* kConfig = R"({
  "pipeline_config": {
    "operators": {
      "mock1": {
        "type_name": "test_stats_mock",
        "$metadata": {"common_input": [], "common_output": [], "item_input": [], "item_output": []}
      },
      "empty1": {
        "type_name": "test_empty_stats_mock",
        "$metadata": {"common_input": [], "common_output": [], "item_input": [], "item_output": []}
      }
    }
  },
  "pipeline_group": {"main": {"pipeline": ["mock1", "empty1"]}},
  "flow_contract": {"common_input": [], "item_input": [], "common_output": [], "item_output": []}
})";

}  // namespace

TEST_CASE("stats_provider: engine collects custom stats") {
    g_mock_op = nullptr;
    auto cfg = load_config_from_json(kConfig);
    pine::Engine engine(std::move(cfg));

    auto custom = engine.operator_custom_stats();
    CHECK(custom.empty()); // initially empty because StatsMockOp has empty stats

    REQUIRE(g_mock_op != nullptr);
    g_mock_op->stats["cache_hits"] = 42;
    g_mock_op->stats["cache_misses"] = 7;

    custom = engine.operator_custom_stats();
    REQUIRE(custom.size() == 1);
    CHECK(custom.count("mock1") == 1);
    CHECK(custom["mock1"]["cache_hits"] == 42);
    CHECK(custom["mock1"]["cache_misses"] == 7);
}
