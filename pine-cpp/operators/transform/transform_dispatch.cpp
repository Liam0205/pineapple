#include "operators/_helpers.hpp"
#include "pine/operator.hpp"

namespace pine {

class TransformDispatchOp : public Operator, public ConcurrentSafe {
public:
    void init(const OperatorConfig& cfg) override {
        op_name_ = cfg.name;
        src_ = cfg.metadata.common_input.at(0);
        dst_ = cfg.metadata.item_output.at(0);
    }
    void execute(const OperatorInput& input, OperatorOutput& out) override {
        JsonValue v = input.common(src_);
        for (std::size_t j = 0; j < input.item_count(); ++j) {
            out.set_item(static_cast<int>(j), dst_, v);
        }
    }
private:
    std::string op_name_;
    std::string src_;
    std::string dst_;
};

static const OperatorSchema k_transform_dispatch_schema{
    .name = "transform_dispatch",
    .type = OpType::Transform,
    .description = "Copies a common-side field value to every item as an item-side field.",
    .params = {},
};
PINE_REGISTER_OPERATOR_T(TransformDispatchOp, k_transform_dispatch_schema)

}  // namespace pine
