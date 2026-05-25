#include "operators/_helpers.hpp"
#include "pine/operator.hpp"

#include <set>

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
        common_defaults_ = cfg.common_defaults;
        item_defaults_ = cfg.item_defaults;
    }
    void execute(const Frame& frame, OperatorOutput& out) override {
        std::set<std::string> skip_set(skip_.begin(), skip_.end());

        if (direction_ == "common_to_item") {
            std::vector<std::string> active_inputs;
            for (const auto& field : common_input_) {
                if (!skip_set.count(field)) active_inputs.push_back(field);
            }
            for (std::size_t i = 0; i < active_inputs.size(); ++i) {
                JsonValue value = require_common_local(frame, active_inputs[i]);
                const auto& dst = item_output_.at(i);
                for (std::size_t j = 0; j < frame.item_count(); ++j) {
                    out.set_item(static_cast<int>(j), dst, value);
                }
            }
        } else if (direction_ == "common_to_common") {
            std::vector<std::string> active_inputs;
            for (const auto& field : common_input_) {
                if (!skip_set.count(field)) active_inputs.push_back(field);
            }
            for (std::size_t i = 0; i < active_inputs.size(); ++i) {
                JsonValue value = require_common_local(frame, active_inputs[i]);
                out.set_common(common_output_.at(i), value);
            }
        } else if (direction_ == "item_to_item") {
            std::vector<std::string> active_inputs;
            for (const auto& field : item_input_) {
                if (!skip_set.count(field)) active_inputs.push_back(field);
            }
            for (std::size_t i = 0; i < active_inputs.size(); ++i) {
                const auto& src = active_inputs[i];
                const auto& dst = item_output_.at(i);
                for (std::size_t j = 0; j < frame.item_count(); ++j) {
                    out.set_item(static_cast<int>(j), dst, require_item_local(frame, j, src));
                }
            }
        } else if (direction_ == "item_to_common") {
            std::vector<std::string> active_inputs;
            for (const auto& field : item_input_) {
                if (!skip_set.count(field)) active_inputs.push_back(field);
            }
            for (std::size_t i = 0; i < active_inputs.size(); ++i) {
                const auto& src = active_inputs[i];
                JsonValue::array_t vals;
                for (std::size_t j = 0; j < frame.item_count(); ++j) {
                    vals.push_back(frame.item(j, src));
                }
                out.set_common(common_output_.at(i), JsonValue(vals));
            }
        } else {
            throw ExecutionError("transform_copy: unsupported direction \"" + direction_ + "\"");
        }
    }
private:
    JsonValue require_common_local(const Frame& frame, const std::string& field) {
        JsonValue v = frame.common(field);
        if (!v.is_null()) return v;
        auto def = common_defaults_.find(field);
        if (def != common_defaults_.end()) return def->second;
        throw ExecutionError("required field \"" + field + "\" is nil in common");
    }
    JsonValue require_item_local(const Frame& frame, std::size_t index, const std::string& field) {
        JsonValue v = frame.item(index, field);
        if (!v.is_null()) return v;
        auto def = item_defaults_.find(field);
        if (def != item_defaults_.end()) return def->second;
        throw ExecutionError("required field \"" + field + "\" is nil on item[" + std::to_string(index) + "]");
    }

    std::string op_name_;
    std::string direction_;
    std::vector<std::string> skip_;
    std::vector<std::string> common_input_;
    std::vector<std::string> common_output_;
    std::vector<std::string> item_input_;
    std::vector<std::string> item_output_;
    std::map<std::string, JsonValue> common_defaults_;
    std::map<std::string, JsonValue> item_defaults_;
};

static const OperatorSchema k_transform_copy_schema{
    .name = "transform_copy",
    .type = OpType::Transform,
    .description = "Copies field values between common and item dimensions.",
    .params = {
        {"direction", {.type = "string", .required = true, .default_value = JsonValue(nullptr),
                       .description = "Copy direction: \"common_to_item\", \"item_to_common\", \"common_to_common\", or \"item_to_item\"."}},
    },
};
PINE_REGISTER_OPERATOR(k_transform_copy_schema,
    ([] { return std::make_unique<TransformCopyOp>(); }))

}  // namespace pine
