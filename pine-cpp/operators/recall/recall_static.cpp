#include "pine/operator.hpp"

#include <unordered_map>

#include "operators/_helpers.hpp"

namespace pine {

class RecallStaticOp : public Operator, public AdditiveWritesRowSet {
 public:
  void init(const OperatorConfig& cfg) override {
    op_name_ = cfg.name;
    for (const auto& item : cfg.params.as_object().at("items").as_array()) {
      Variant::object_t row;
      for (const auto& [key, value] : item.as_object()) {
        row[key] = value;
      }
      items_.push_back(std::move(row));
    }
    const auto& params = cfg.params.as_object();
    auto it = params.find("set_common");
    if (it != params.end() && !it->second.is_null()) {
      for (const auto& [key, value] : it->second.as_object()) {
        set_common_[key] = value;
      }
    }
  }
  void execute(const OperatorInput& /*input*/, OperatorOutput& out) override {
    for (const auto& [key, value] : set_common_) {
      out.set_common(key, value);
    }
    for (const auto& row : items_) {
      out.add_item(row);
    }
  }

 private:
  std::string op_name_;
  std::vector<Variant::object_t> items_;
  Variant::object_t set_common_;
};

static const OperatorSchema k_recall_static_schema{
    .name = "recall_static",
    .type = OpType::Recall,
    .description = "Emits a configurable static set of items for testing and validation.",
    .params =
        {
            {"items",
             {.type = "any",
              .required = true,
              .default_value = Variant(nullptr),
              .description = "JSON array of item maps to emit as candidates."}},
            {"set_common",
             {.type = "any",
              .required = false,
              .default_value = Variant(nullptr),
              .description = "JSON object of common fields the recall writes."}},
        },
    .metadata = {.common_input = "[]",
                 .common_output = "[]",
                 .item_input = "[]",
                 .item_output = "[item_id, ...]"},
};
PINE_REGISTER_OPERATOR_T(RecallStaticOp, k_recall_static_schema)

}  // namespace pine
