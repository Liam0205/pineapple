#include "operators/_helpers.hpp"
#include "pine/operator.hpp"

#include <map>

namespace pine {

class RecallResourceOp : public Operator, public AdditiveWritesRowSet {
public:
    void init(const OperatorConfig& cfg) override {
        op_name_ = cfg.name;
        const auto& params = cfg.params.as_object();
        auto rn_it = params.find("resource_name");
        if (rn_it == params.end() || !rn_it->second.is_string())
            throw ExecutionError("recall_resource: missing resource_name");
        resource_name_ = rn_it->second.as_string();
    }
    void execute(const OperatorInput& input, OperatorOutput& out) override {
        if (!input.resources())
            throw ExecutionError("recall_resource: no resource provider in context");
        auto res_it = input.resources()->find(resource_name_);
        if (res_it == input.resources()->end())
            throw ExecutionError("recall_resource: resource \"" + resource_name_ + "\" not found");
        const auto& resource = res_it->second;
        if (!resource.is_array())
            throw ExecutionError("recall_resource: resource \"" + resource_name_ + "\" is " + pine::operators::json_type_name(resource) + ", want []map[string]any");
        for (std::size_t i = 0; i < resource.as_array().size(); ++i) {
            const auto& elem = resource.as_array()[i];
            if (!elem.is_object())
                throw ExecutionError("recall_resource: items[" + std::to_string(i) + "] is " + pine::operators::json_type_name(elem) + ", want map[string]any");
            std::map<std::string, JsonValue> row;
            for (const auto& [key, value] : elem.as_object()) row[key] = value;
            out.add_item(std::move(row));
        }
    }
private:
    std::string op_name_;
    std::string resource_name_;
};

static const OperatorSchema k_recall_resource_schema{
    .name = "recall_resource",
    .type = OpType::Recall,
    .description = "Recalls items from a named resource.",
    .params = {
        {"resource_name", {.type = "string", .required = true, .default_value = JsonValue(nullptr),
                           .description = "Name of the resource to read."}},
    },
};
PINE_REGISTER_OPERATOR_T(RecallResourceOp, k_recall_resource_schema)

}  // namespace pine
