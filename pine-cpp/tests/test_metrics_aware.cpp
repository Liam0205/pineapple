#include "pine/pine.hpp"
#include "pine/operator.hpp"
#include "pine/operator_input.hpp"
#include "pine/metrics.hpp"

#include <doctest/doctest.h>

#include <memory>
#include <string>

using namespace pine;

namespace {

// Tracks whether the Engine injected a non-null provider matching the
// one passed via EngineOptions.
class ProbeOp : public Operator, public MetricsAware {
public:
    static metrics::Provider* injected;
    static int call_count;

    void execute(const OperatorInput&, OperatorOutput&) override {}
    void set_metrics_provider(metrics::Provider* p) override {
        injected = p;
        ++call_count;
    }
};
metrics::Provider* ProbeOp::injected = nullptr;
int ProbeOp::call_count = 0;

// Operator that does NOT implement MetricsAware; engine must NOT call
// set_metrics_provider on it (compile-time safety; we just register it).
class NonAwareOp : public Operator {
public:
    void execute(const OperatorInput&, OperatorOutput&) override {}
};

const bool _reg_probe = [] {
    OperatorSchema schema{
        .name = "test_metrics_probe",
        .type = OpType::Recall,
        .description = "metrics-aware probe",
        .params = {},
    };
    register_operator_typed<ProbeOp>(std::move(schema));
    OperatorSchema schema2{
        .name = "test_metrics_nonaware",
        .type = OpType::Recall,
        .description = "non-aware probe",
        .params = {},
    };
    register_operator_typed<NonAwareOp>(std::move(schema2));
    return true;
}();

const char* kConfig = R"({
  "pipeline_config": {
    "operators": {
      "probe": {
        "type_name": "test_metrics_probe",
        "$metadata": {"common_input": [], "common_output": [], "item_input": [], "item_output": []}
      },
      "nonaware": {
        "type_name": "test_metrics_nonaware",
        "$metadata": {"common_input": [], "common_output": [], "item_input": [], "item_output": []}
      }
    }
  },
  "pipeline_group": {"main": {"pipeline": ["probe", "nonaware"]}},
  "flow_contract": {"common_input": [], "item_input": [], "common_output": [], "item_output": []}
})";

class CountingProvider : public metrics::Provider {
public:
    struct C : metrics::Counter { metrics::Counter* with(const std::vector<std::string>&) override { return this; } void inc() override {} };
    struct G : metrics::Gauge   { metrics::Gauge*   with(const std::vector<std::string>&) override { return this; } void set(double) override {} void add(double) override {} };
    struct H : metrics::Histogram { metrics::Histogram* with(const std::vector<std::string>&) override { return this; } void observe(double) override {} };
    metrics::Counter*   new_counter(const metrics::MetricOpts&) override   { return new_counter_called = true, &c_; }
    metrics::Gauge*     new_gauge(const metrics::MetricOpts&) override     { return &g_; }
    metrics::Histogram* new_histogram(const metrics::HistogramOpts&) override { return &h_; }
    bool new_counter_called = false;
private:
    C c_; G g_; H h_;
};

}  // namespace

TEST_CASE("metrics_aware: engine injects configured provider after init") {
    ProbeOp::injected = nullptr;
    ProbeOp::call_count = 0;

    CountingProvider provider;
    EngineOptions opts;
    opts.metrics_provider = &provider;

    auto cfg = load_config_from_json(kConfig);
    Engine engine(std::move(cfg), opts);

    CHECK(ProbeOp::call_count == 1);
    CHECK(ProbeOp::injected == &provider);
}

TEST_CASE("metrics_aware: nop provider is injected when none configured") {
    ProbeOp::injected = nullptr;
    ProbeOp::call_count = 0;

    auto cfg = load_config_from_json(kConfig);
    Engine engine(std::move(cfg));

    CHECK(ProbeOp::call_count == 1);
    CHECK(ProbeOp::injected == metrics::nop_provider());
}
