#include "operators/_helpers.hpp"
#include "pine/operator.hpp"

namespace pine {

class MergeDedupOp : public Operator, public ConsumesRowSet, public MutatesRowSet {
public:
    void init(const OperatorConfig& cfg) override {
        op_name_ = cfg.name;
        field_ = cfg.metadata.item_input.at(0);
    }
    void execute(const OperatorInput& input, OperatorOutput& out) override {
        std::vector<std::string> seen;
        for (std::size_t i = 0; i < input.item_count(); ++i) {
            JsonValue fv = input.item(i, field_);
            std::string key = operators::dedup_key(fv);
            bool dup = false;
            for (const auto& s : seen) { if (s == key) { dup = true; break; } }
            if (dup) {
                out.remove_item(static_cast<int>(i));
            } else {
                seen.push_back(key);
            }
        }
    }
private:
    std::string op_name_;
    std::string field_;
};

static const OperatorSchema k_merge_dedup_schema{
    .name = "merge_dedup",
    .type = OpType::Merge,
    .description = "Deduplicates items by a key field, keeping the first occurrence.",
    .params = {
        {"strategy", {.type = "string", .required = false, .default_value = JsonValue("first"),
                      .description = "Dedup strategy \xe2\x80\x94 \"first\" keeps first occurrence."}},
    },
};
PINE_REGISTER_OPERATOR_T(MergeDedupOp, k_merge_dedup_schema)

}  // namespace pine
