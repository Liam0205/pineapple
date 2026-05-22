#include "operators/_helpers.hpp"
#include "pine/operator.hpp"

namespace pine {

class ObserveLogOp : public Operator {
public:
    void init(const OperatorConfig& /*cfg*/) override {}
    void execute(const Frame& /*frame*/, OperatorOutput& /*out*/) override {}
};

static const OperatorSchema k_observe_log_schema{
    .name = "observe_log",
    .type = OpType::Observe,
    .description = "Reads declared input fields and writes them to Go standard log. This is a read-only operator: it produces no output fields and does not modify the DataFrame. It is exempt from dead-code detection.",
    .params = {
        {"log_prefix", {.type = "string", .required = false, .default_value = JsonValue(""),
                        .description = "Prefix prepended to each log line."}},
    },
};
PINE_REGISTER_OPERATOR(k_observe_log_schema,
    ([] { return std::make_unique<ObserveLogOp>(); }))

}  // namespace pine
