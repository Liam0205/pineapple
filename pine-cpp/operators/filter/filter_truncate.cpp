#include "pine/operator.hpp"
#include "pine/template.hpp"

#include "operators/_helpers.hpp"

namespace pine {

class FilterTruncateOp : public Operator, public ConsumesRowSet, public MutatesRowSet {
 public:
  void init(const OperatorConfig& cfg) override {
    op_name_ = cfg.name;
    const auto& tp = cfg.params.as_object().at("top_n");
    if (tp.is_number()) {
      top_n_ = static_cast<int>(tp.as_number());
    } else if (tp.is_string()) {
      // Only a bare {{field}} marker is accepted here; engine resolves
      // it per-request at execute time. A non-marker string is hand-
      // edited garbage and must error out rather than silently
      // truncating to zero.
      if (!is_bare_marker(tp.as_string())) {
        throw ExecutionError("filter_truncate: top_n must be numeric");
      }
      top_n_ = 0;
    } else {
      throw ExecutionError("filter_truncate: top_n must be numeric");
    }
    if (top_n_ < 0) {
      throw ExecutionError("filter_truncate: top_n must be non-negative, got " + std::to_string(top_n_));
    }
  }
  void execute(const OperatorInput& input, OperatorOutput& out) override {
    // top_n is templatable (#74). When the DSL configured a {{field}}
    // marker the engine resolved it against this request's common frame
    // before execute; otherwise the init-time value is used. The
    // is_number() check is unreachable: top_n is declared int64 and
    // resolve_templated_params normalizes via parse_int. Kept as
    // defense in depth.
    int top_n = top_n_;
    Variant resolved = input.templated_param("top_n");
    if (resolved.is_number()) {
      top_n = static_cast<int>(resolved.as_number());
    }
    // Mirror Init's invariant at execute time: a templated negative
    // value would otherwise have undefined remove_item(i<0) semantics.
    if (top_n < 0) {
      throw ExecutionError("filter_truncate: top_n must be non-negative, got " + std::to_string(top_n));
    }
    int n = static_cast<int>(input.item_count());
    for (int i = top_n; i < n; ++i) {
      out.remove_item(i);
    }
  }

 private:
  std::string op_name_;
  int top_n_ = 0;
};

static const OperatorSchema k_filter_truncate_schema{
    .name = "filter_truncate",
    .type = OpType::Filter,
    .description = "Keeps only the first N items, removing the rest.",
    .params =
        {
            {"top_n",
             {.type = "int64",
              .required = true,
              .default_value = Variant(nullptr),
              .description = "Number of items to keep. Supports {{field}} interpolation.",
              .templatable = true}},
        },
    .metadata = {.common_input = "[]", .common_output = "[]", .item_input = "[]", .item_output = "[]"},
};
PINE_REGISTER_OPERATOR_T(FilterTruncateOp, k_filter_truncate_schema)

}  // namespace pine
