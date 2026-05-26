#include "operators/_helpers.hpp"
#include "pine/operator.hpp"

namespace pine {

class TransformSizeOp : public Operator, public ConsumesRowSet, public ConcurrentSafe {
public:
    void init(const OperatorConfig& cfg) override {
        op_name_ = cfg.name;
        out_field_ = cfg.metadata.common_output.at(0);
    }
    void execute(const OperatorInput& input, OperatorOutput& out) override {
        out.set_common(out_field_, JsonValue(static_cast<double>(input.item_count())));
    }
private:
    std::string op_name_;
    std::string out_field_;
};

static const OperatorSchema k_transform_size_schema{
    .name = "transform_size",
    .type = OpType::Transform,
    .description = "Outputs the current item count to a common field.",
    .params = {},
};
PINE_REGISTER_OPERATOR_T(TransformSizeOp, k_transform_size_schema)

}  // namespace pine
