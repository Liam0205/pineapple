#include "pine/operator.hpp"

#include <algorithm>

#include "operators/_helpers.hpp"

namespace pine {

class ReorderSortOp : public Operator, public ConsumesRowSet, public MutatesRowSet {
 public:
  void init(const OperatorConfig& cfg) override {
    op_name_ = cfg.name;
    if (cfg.metadata.item_input.empty()) {
      throw ExecutionError("reorder_sort requires item_input field");
    }
    field_ = cfg.metadata.item_input.front();
    const auto& obj = cfg.params.as_object();
    auto it = obj.find("order");
    order_ = (it == obj.end()) ? std::string("desc") : it->second.as_string();
  }
  void execute(const OperatorInput& input, OperatorOutput& out) override {
    if (input.item_count() == 0) {
      return;
    }
    struct Keyed {
      double v;
      std::size_t idx;
    };
    std::vector<Keyed> keyed;
    keyed.reserve(input.item_count());
    // Batched column access: one lock + one lookup instead of per-element
    // item() calls.
    std::vector<Variant> raw = input.item_column(field_);
    for (std::size_t i = 0; i < raw.size(); ++i) {
      try {
        const Variant& v = raw[i];
        if (v.is_null()) {
          throw operators::OperatorError("required field \"" + field_ + "\" is nil on item[" +
                                         std::to_string(i) + "]");
        }
        keyed.push_back({operators::to_double(v), i});
      } catch (const operators::OperatorError& err) {
        throw ExecutionError("reorder_sort: item[" + std::to_string(i) + "]." + field_ + ": " + err.what());
      }
    }
    if (order_ == "asc") {
      std::stable_sort(keyed.begin(), keyed.end(), [](const Keyed& a, const Keyed& b) { return a.v < b.v; });
    } else if (order_ == "desc") {
      std::stable_sort(keyed.begin(), keyed.end(), [](const Keyed& a, const Keyed& b) { return a.v > b.v; });
    } else {
      throw ExecutionError("reorder_sort: unsupported order \"" + order_ + "\"");
    }
    std::vector<int> order_vec;
    order_vec.reserve(keyed.size());
    for (const auto& k : keyed) {
      order_vec.push_back(static_cast<int>(k.idx));
    }
    out.set_item_order(std::move(order_vec));
  }

 private:
  std::string op_name_;
  std::string field_;
  std::string order_;
};

static const OperatorSchema k_reorder_sort_schema{
    .name = "reorder_sort",
    .type = OpType::Reorder,
    .description = "Sorts items by a numeric field in ascending or descending order.",
    .params =
        {
            {"order",
             {.type = "string",
              .required = false,
              .default_value = Variant("desc"),
              .description = "Sort direction \xe2\x80\x94 \"asc\" or \"desc\"."}},
        },
    .metadata = {.common_input = "[]", .common_output = "[]", .item_input = "[<field>]", .item_output = "[]"},
};
PINE_REGISTER_OPERATOR_T(ReorderSortOp, k_reorder_sort_schema)

}  // namespace pine
