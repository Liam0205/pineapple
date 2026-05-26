#include "operators/_helpers.hpp"
#include "pine/operator.hpp"

#include <charconv>

namespace pine {

class TransformResourceLookupOp : public Operator, public ConcurrentSafe {
public:
    void init(const OperatorConfig& cfg) override {
        op_name_ = cfg.name;
        const auto& params = cfg.params.as_object();
        auto rn_it = params.find("resource_name");
        if (rn_it == params.end() || !rn_it->second.is_string())
            throw ExecutionError("transform_resource_lookup: missing resource_name");
        resource_name_ = rn_it->second.as_string();

        auto lk_it = params.find("lookup_key");
        if (lk_it == params.end() || !lk_it->second.is_string())
            throw ExecutionError("transform_resource_lookup: missing lookup_key");
        lookup_key_ = lk_it->second.as_string();

        auto of_it = params.find("output_field");
        if (of_it == params.end() || !of_it->second.is_string())
            throw ExecutionError("transform_resource_lookup: missing output_field");
        output_field_ = of_it->second.as_string();

        auto dv_it = params.find("default_value");
        has_default_ = (dv_it != params.end());
        if (has_default_) default_value_ = dv_it->second;
    }
    void execute(const Frame& frame, OperatorOutput& out) override {
        if (!frame.resources())
            throw ExecutionError("transform_resource_lookup: no resource provider in context");
        auto res_it = frame.resources()->find(resource_name_);
        if (res_it == frame.resources()->end())
            throw ExecutionError("transform_resource_lookup: resource \"" + resource_name_ + "\" not found");
        const auto& resource = res_it->second;
        if (!resource.is_object())
            throw ExecutionError("transform_resource_lookup: resource \"" + resource_name_ + "\" is " + pine::operators::json_type_name(resource) + ", want map[string]any");
        const auto& table = resource.as_object();

        for (std::size_t i = 0; i < frame.item_count(); ++i) {
            JsonValue field_val = frame.item(i, lookup_key_);
            if (field_val.is_null()) {
                if (has_default_) out.set_item(static_cast<int>(i), output_field_, default_value_);
                continue;
            }
            std::string key;
            if (field_val.is_string()) {
                key = field_val.as_string();
            } else if (field_val.is_number()) {
                // R3-H4: lookup keys must use FormatInt / FormatFloat('f')
                // — never scientific — to match pine-go resource_lookup.go.
                key = operators::go_format_lookup_key(field_val.as_number());
            } else {
                key = operators::sprint_value(field_val);
            }
            auto val_it = table.find(key);
            if (val_it != table.end()) {
                out.set_item(static_cast<int>(i), output_field_, val_it->second);
            } else if (has_default_) {
                out.set_item(static_cast<int>(i), output_field_, default_value_);
            }
        }
    }
private:
    std::string op_name_;
    std::string resource_name_;
    std::string lookup_key_;
    std::string output_field_;
    bool has_default_ = false;
    JsonValue default_value_;
};

static const OperatorSchema k_transform_resource_lookup_schema{
    .name = "transform_resource_lookup",
    .type = OpType::Transform,
    .description = "Enriches items by looking up values from a named resource.",
    .params = {
        {"default_value", {.type = "any", .required = false, .default_value = JsonValue(nullptr),
                           .description = "Value to use when the key is not found. Missing keys are skipped if unset."}},
        {"lookup_key", {.type = "string", .required = true, .default_value = JsonValue(nullptr),
                        .description = "Item field whose value is used as the lookup key."}},
        {"output_field", {.type = "string", .required = true, .default_value = JsonValue(nullptr),
                          .description = "Item field to write the looked-up value to."}},
        {"resource_name", {.type = "string", .required = true, .default_value = JsonValue(nullptr),
                           .description = "Name of the resource to read."}},
    },
};
PINE_REGISTER_OPERATOR_T(TransformResourceLookupOp, k_transform_resource_lookup_schema)

}  // namespace pine
