#include "operators/_helpers.hpp"
#include "pine/operator.hpp"

#include <map>

namespace pine {

namespace {

std::string inline_type_name(const JsonValue& v) {
    if (v.is_null()) return "nil";
    if (v.is_bool()) return "bool";
    if (v.is_number()) return "float64";
    if (v.is_string()) return "string";
    if (v.is_array()) return "array";
    if (v.is_object()) return "object";
    return "unknown";
}

}  // namespace

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
    void execute(const Frame& frame, OperatorOutput& out) override {
        if (!frame.resources())
            throw ExecutionError("recall_resource: no resource provider in context");
        auto res_it = frame.resources()->find(resource_name_);
        if (res_it == frame.resources()->end())
            throw ExecutionError("recall_resource: resource \"" + resource_name_ + "\" not found");
        const auto& resource = res_it->second;
        if (!resource.is_array())
            throw ExecutionError("recall_resource: resource \"" + resource_name_ + "\" is " + inline_type_name(resource) + ", want []map[string]any");
        for (std::size_t i = 0; i < resource.as_array().size(); ++i) {
            const auto& elem = resource.as_array()[i];
            if (!elem.is_object())
                throw ExecutionError("recall_resource: items[" + std::to_string(i) + "] is " + inline_type_name(elem) + ", want map[string]any");
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
PINE_REGISTER_OPERATOR(k_recall_resource_schema,
    ([] { return std::make_unique<RecallResourceOp>(); }))

}  // namespace pine
