#include "operators/_helpers.hpp"
#include "pine/operator.hpp"

namespace pine {

class FilterConditionOp : public Operator, public ConsumesRowSet, public MutatesRowSet {
public:
    void init(const OperatorConfig& cfg) override {
        op_name_ = cfg.name;
        const auto& params = cfg.params.as_object();
        auto val_it = params.find("value");
        if (val_it == params.end())
            throw ExecutionError("filter_condition: missing required param 'value'");
        target_ = operators::sprint_value(val_it->second);
        field_ = cfg.metadata.item_input.at(0);
    }
    void execute(const OperatorInput& input, OperatorOutput& out) override {
        for (std::size_t i = 0; i < input.item_count(); ++i) {
            JsonValue fv = input.item(i, field_);
            if (operators::sprint_value(fv) == target_) out.remove_item(static_cast<int>(i));
        }
    }
private:
    std::string op_name_;
    std::string target_;
    std::string field_;
};

static const OperatorSchema k_filter_condition_schema{
    .name = "filter_condition",
    .type = OpType::Filter,
    .description = "Removes items where a specified field equals a given value.",
    .params = {
        {"value", {.type = "any", .required = true, .default_value = JsonValue(nullptr),
                   .description = "Items where field == value are removed."}},
    },
};
PINE_REGISTER_OPERATOR_T(FilterConditionOp, k_filter_condition_schema)

}  // namespace pine
