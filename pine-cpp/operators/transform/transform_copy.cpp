#include "pine/operator.hpp"

#include <set>

#include "operators/_helpers.hpp"

namespace pine {

class TransformCopyOp : public Operator, public ConcurrentSafe {
 public:
  void init(const OperatorConfig& cfg) override {
    op_name_ = cfg.name;
    direction_ = cfg.params.as_object().at("direction").as_string();
    skip_ = cfg.skip;
    common_input_ = cfg.metadata.common_input;
    common_output_ = cfg.metadata.common_output;
    item_input_ = cfg.metadata.item_input;
    item_output_ = cfg.metadata.item_output;
  }
  void execute(const OperatorInput& input, OperatorOutput& out) override {
    std::set<std::string> skip_set(skip_.begin(), skip_.end());
    const auto active_inputs = [&](const std::vector<std::string>& src_list) {
      std::vector<std::string> out_list;
      out_list.reserve(src_list.size());
      for (const auto& f : src_list) {
        if (!skip_set.count(f)) {
          out_list.push_back(f);
        }
      }
      return out_list;
    };

    if (direction_ == "common_to_item") {
      const auto inputs = active_inputs(common_input_);
      for (std::size_t i = 0; i < inputs.size(); ++i) {
        Variant value = input.common(inputs[i]);
        const auto& dst = item_output_.at(i);
        for (std::size_t j = 0; j < input.item_count(); ++j) {
          out.set_item(static_cast<int>(j), dst, value);
        }
      }
    } else if (direction_ == "common_to_common") {
      const auto inputs = active_inputs(common_input_);
      for (std::size_t i = 0; i < inputs.size(); ++i) {
        Variant value = input.common(inputs[i]);
        out.set_common(common_output_.at(i), value);
      }
    } else if (direction_ == "item_to_item") {
      const auto inputs = active_inputs(item_input_);
      for (std::size_t i = 0; i < inputs.size(); ++i) {
        const auto& src = inputs[i];
        const auto& dst = item_output_.at(i);
        for (std::size_t j = 0; j < input.item_count(); ++j) {
          out.set_item(static_cast<int>(j), dst, input.item(j, src));
        }
      }
    } else if (direction_ == "item_to_common") {
      const auto inputs = active_inputs(item_input_);
      for (std::size_t i = 0; i < inputs.size(); ++i) {
        const auto& src = inputs[i];
        Variant::array_t vals;
        for (std::size_t j = 0; j < input.item_count(); ++j) {
          vals.push_back(input.item(j, src));
        }
        out.set_common(common_output_.at(i), Variant(vals));
      }
    } else {
      throw ExecutionError("transform_copy: unsupported direction \"" + direction_ + "\"");
    }
  }

 private:
  std::string op_name_;
  std::string direction_;
  std::vector<std::string> skip_;
  std::vector<std::string> common_input_;
  std::vector<std::string> common_output_;
  std::vector<std::string> item_input_;
  std::vector<std::string> item_output_;
};

static const OperatorSchema k_transform_copy_schema{
    .name = "transform_copy",
    .type = OpType::Transform,
    .description = "Copies field values between common and item dimensions.",
    .params =
        {
            {"direction",
             {.type = "string",
              .required = true,
              .default_value = Variant(nullptr),
              .description = "Copy direction: \"common_to_item\", \"item_to_common\", \"common_to_common\", "
                             "or \"item_to_item\"."}},
        },
    .metadata = {.common_input = "[<source_fields...>]",
                 .common_output = "[<target_field>]   (collects all item values into a list)",
                 .item_input = "[<source_field>]",
                 .item_output = "[<target_fields...>]"},
};
PINE_REGISTER_OPERATOR_T(TransformCopyOp, k_transform_copy_schema)

}  // namespace pine
