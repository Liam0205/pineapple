#include "operators/_helpers.hpp"
#include "pine/operator.hpp"

namespace pine {

class FilterTruncateOp : public Operator, public ConsumesRowSet, public MutatesRowSet {
public:
    void init(const OperatorConfig& cfg) override {
        op_name_ = cfg.name;
        top_n_ = static_cast<int>(cfg.params.as_object().at("top_n").as_number());
        if (top_n_ < 0)
            throw ExecutionError(cfg.name, "filter_truncate: top_n must be non-negative");
    }
    void execute(const Frame& frame, OperatorOutput& out) override {
        int n = static_cast<int>(frame.item_count());
        for (int i = top_n_; i < n; ++i) out.remove_item(i);
    }
private:
    std::string op_name_;
    int top_n_ = 0;
};

static const OperatorSchema k_filter_truncate_schema{
    .name = "filter_truncate",
    .type = OpType::Filter,
    .description = "Keeps only the first N items, removing the rest.",
    .params = {
        {"top_n", {.type = "int64", .required = true, .default_value = JsonValue(nullptr),
                   .description = "Number of items to keep."}},
    },
};
PINE_REGISTER_OPERATOR(k_filter_truncate_schema,
    ([] { return std::make_unique<FilterTruncateOp>(); }))

}  // namespace pine
