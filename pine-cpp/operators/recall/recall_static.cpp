#include "pine/operator.hpp"

#include <unordered_map>

#include "operators/_helpers.hpp"

namespace pine {

class RecallStaticOp : public Operator, public AdditiveWritesRowSet {
 public:
  void init(const OperatorConfig& cfg) override {
    op_name_ = cfg.name;
    for (const auto& item : cfg.params.as_object().at("items").as_array()) {
      JsonValue::object_t row;
      for (const auto& [key, value] : item.as_object()) {
        row[key] = value;
      }
      items_.push_back(std::move(row));
    }
  }
  void execute(const OperatorInput& /*input*/, OperatorOutput& out) override {
    for (const auto& row : items_) {
      out.add_item(row);
    }
  }

 private:
  std::string op_name_;
  std::vector<JsonValue::object_t> items_;
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
              .default_value = JsonValue(nullptr),
              .description = "JSON array of item maps to emit as candidates."}},
        },
};
PINE_REGISTER_OPERATOR_T(RecallStaticOp, k_recall_static_schema)

}  // namespace pine
