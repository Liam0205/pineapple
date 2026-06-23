#include "pine/operator.hpp"

#include <chrono>
#include <thread>

#include "operators/_helpers.hpp"

namespace pine {

class TransformBenchSleepOp : public Operator, public ConcurrentSafe {
 public:
  void init(const OperatorConfig& cfg) override {
    auto it = cfg.params.as_object().find("delay_ms");
    if (it != cfg.params.as_object().end() && it->second.is_number()) {
      delay_ms_ = static_cast<int>(it->second.as_number());
    }
  }

  void execute(const OperatorInput& input, OperatorOutput& out) override {
    std::this_thread::sleep_for(std::chrono::milliseconds(delay_ms_));
    for (std::size_t i = 0; i < input.item_count(); ++i) {
      out.set_item(static_cast<int>(i), "_bench_slept", Variant(true));
    }
  }

 private:
  int delay_ms_ = 5;
};

static const OperatorSchema k_transform_bench_sleep_schema{
    .name = "transform_bench_sleep",
    .type = OpType::Transform,
    .description =
        "Benchmark-only I/O-simulating operator. Sleeps for delay_ms per invocation. Not available in "
        "pine-python.",
    .params =
        {
            {"delay_ms",
             {.type = "int",
              .required = false,
              .default_value = Variant(5.0),
              .description = "Milliseconds to sleep per operator invocation."}},
        },
    .metadata = {.common_input = "", .common_output = "", .item_input = "", .item_output = ""},
};
PINE_REGISTER_OPERATOR_T(TransformBenchSleepOp, k_transform_bench_sleep_schema)

}  // namespace pine
