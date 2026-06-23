#include "pine/operator.hpp"

#include "operators/_helpers.hpp"

namespace pine {

class FilterPaginateOp : public Operator, public ConsumesRowSet, public MutatesRowSet {
 public:
  void init(const OperatorConfig& cfg) override {
    op_name_ = cfg.name;
    page_field_ = cfg.metadata.common_input.at(0);
    size_field_ = cfg.metadata.common_input.at(1);
  }
  void execute(const OperatorInput& input, OperatorOutput& out) override {
    int n = static_cast<int>(input.item_count());
    if (n == 0) {
      return;
    }
    auto to_int = [](const Variant& v) -> int {
      if (v.is_number()) {
        return static_cast<int>(v.as_number());
      }
      return 0;
    };
    int page = to_int(input.common(page_field_));
    int size = to_int(input.common(size_field_));
    if (size <= 0) {
      size = 10;
    }
    if (page < 0) {
      page = 0;
    }
    int start = page * size;
    int end = start + size;
    if (end > n) {
      end = n;
    }
    for (int i = 0; i < n; ++i) {
      if (i < start || i >= end) {
        out.remove_item(i);
      }
    }
  }

 private:
  std::string op_name_;
  std::string page_field_;
  std::string size_field_;
};

static const OperatorSchema k_filter_paginate_schema{
    .name = "filter_paginate",
    .type = OpType::Filter,
    .description = "Keeps only items in the [page*size, page*size+size) range, removes the rest.",
    .params = {},
    .metadata = {.common_input = "[<page_field>, <size_field>]",
                 .common_output = "[]",
                 .item_input = "[]",
                 .item_output = "[]"},
};
PINE_REGISTER_OPERATOR_T(FilterPaginateOp, k_filter_paginate_schema)

}  // namespace pine
