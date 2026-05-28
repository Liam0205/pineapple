#include "pine/operator.hpp"

#include <cstdint>

#include "operators/_helpers.hpp"

namespace pine {

namespace {

int64_t fib(int n) {
  if (n <= 1) {
    return n;
  }
  int64_t a = 0, b = 1;
  for (int i = 2; i <= n; ++i) {
    int64_t tmp = a + b;
    a = b;
    b = tmp;
  }
  return b;
}

double cpu_work(int iterations) {
  double acc = 0;
  for (int i = 0; i < iterations; ++i) {
    acc += static_cast<double>(fib(32));
    acc /= 1.000001;
  }
  return acc;
}

}  // namespace

class TransformBenchCpuOp : public Operator, public ConcurrentSafe {
 public:
  void init(const OperatorConfig& cfg) override {
    auto it = cfg.params.as_object().find("iterations");
    if (it != cfg.params.as_object().end() && it->second.is_number()) {
      iterations_ = static_cast<int>(it->second.as_number());
    }
  }

  void execute(const OperatorInput& input, OperatorOutput& out) override {
    for (std::size_t i = 0; i < input.item_count(); ++i) {
      double result = cpu_work(iterations_);
      out.set_item(static_cast<int>(i), "_bench_result", JsonValue(result));
    }
  }

 private:
  int iterations_ = 100;
};

static const OperatorSchema k_transform_bench_cpu_schema{
    .name = "transform_bench_cpu",
    .type = OpType::Transform,
    .description =
        "Benchmark-only CPU-bound operator. Computes iterative fib per item. Not available in pine-python.",
    .params =
        {
            {"iterations",
             {.type = "int",
              .required = false,
              .default_value = JsonValue(100.0),
              .description = "Number of fib(32) computations per item."}},
        },
};
PINE_REGISTER_OPERATOR_T(TransformBenchCpuOp, k_transform_bench_cpu_schema)

}  // namespace pine
