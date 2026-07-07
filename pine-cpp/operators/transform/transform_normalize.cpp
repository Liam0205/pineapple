#include "pine/operator.hpp"

#include <algorithm>

#include "operators/_helpers.hpp"

namespace pine {

class TransformNormalizeOp : public Operator {
 public:
  void init(const OperatorConfig& cfg) override {
    op_name_ = cfg.name;
    item_input_ = cfg.metadata.item_input;
    item_output_ = cfg.metadata.item_output;
    std::string method = "min_max";
    const auto& params = cfg.params.as_object();
    auto it = params.find("method");
    if (it != params.end() && !it->second.is_null()) {
      method = it->second.as_string();
    }
    if (method != "min_max") {
      throw RegistryError(op_name_, "unsupported method \"" + method + "\"");
    }
  }
  void execute(const OperatorInput& input, OperatorOutput& out) override {
    if (input.item_count() == 0) {
      return;
    }
    if (item_input_.empty()) {
      throw ExecutionError(op_name_, "transform_normalize: item_input is empty");
    }
    if (item_output_.empty()) {
      throw ExecutionError(op_name_, "transform_normalize: item_output is empty");
    }
    const std::string& field_ = item_input_[0];
    const std::string& out_field_ = item_output_[0];
    // Batched column access: one lock + one lookup instead of per-element
    // item() calls.
    std::vector<Variant> raw = input.item_column(field_);
    std::vector<double> vals;
    vals.reserve(input.item_count());
    for (std::size_t i = 0; i < raw.size(); ++i) {
      try {
        const Variant& v = raw[i];
        if (v.is_null()) {
          throw operators::OperatorError("required field \"" + field_ + "\" is nil on item[" +
                                         std::to_string(i) + "]");
        }
        vals.push_back(operators::to_double(v));
      } catch (const operators::OperatorError& err) {
        throw ExecutionError("transform_normalize: item[" + std::to_string(i) + "]." + field_ + ": " +
                             err.what());
      }
    }
    double minv = *std::min_element(vals.begin(), vals.end());
    double maxv = *std::max_element(vals.begin(), vals.end());
    double rng = maxv - minv;
    for (std::size_t i = 0; i < vals.size(); ++i) {
      double norm = (rng == 0.0) ? 0.0 : (vals[i] - minv) / rng;
      out.set_item(static_cast<int>(i), out_field_, Variant(norm));
    }
  }

 private:
  std::string op_name_;
  std::vector<std::string> item_input_;
  std::vector<std::string> item_output_;
};

static const OperatorSchema k_transform_normalize_schema{
    .name = "transform_normalize",
    .type = OpType::Transform,
    .description = "Normalizes a numeric item field using min-max scaling to [0, 1].",
    .params =
        {
            {"method",
             {.type = "string",
              .required = false,
              .default_value = Variant("min_max"),
              .description = "Normalization method."}},
        },
    .metadata = {.common_input = "[]",
                 .common_output = "[]",
                 .item_input = "[<field>]",
                 .item_output = "[<output_field>]"},
};
PINE_REGISTER_OPERATOR_T(TransformNormalizeOp, k_transform_normalize_schema)

}  // namespace pine
