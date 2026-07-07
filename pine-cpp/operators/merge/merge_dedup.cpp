#include "pine/operator.hpp"

#include <unordered_set>

#include "operators/_helpers.hpp"

namespace pine {

class MergeDedupOp : public Operator, public ConsumesRowSet, public MutatesRowSet {
 public:
  void init(const OperatorConfig& cfg) override {
    op_name_ = cfg.name;
    field_ = cfg.metadata.item_input.at(0);
  }
  void execute(const OperatorInput& input, OperatorOutput& out) override {
    const std::size_t n = input.item_count();
    std::unordered_set<std::string> seen;
    seen.reserve(n);
    // Batched column access: one lock + one lookup instead of per-element
    // item() calls.
    std::vector<Variant> col = input.item_column(field_);
    for (std::size_t i = 0; i < n; ++i) {
      auto [_it, inserted] = seen.emplace(operators::dedup_key(col[i]));
      if (!inserted) {
        out.remove_item(static_cast<int>(i));
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
    .params =
        {
            {"strategy",
             {.type = "string",
              .required = false,
              .default_value = Variant("first"),
              .description = "Dedup strategy \xe2\x80\x94 \"first\" keeps first occurrence."}},
        },
    .metadata = {.common_input = "[]",
                 .common_output = "[]",
                 .item_input = "[item_id, _source]",
                 .item_output = "[item_id]"},
};
PINE_REGISTER_OPERATOR_T(MergeDedupOp, k_merge_dedup_schema)

}  // namespace pine
