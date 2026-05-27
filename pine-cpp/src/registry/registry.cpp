#include "pine/pine.hpp"
#include "pine/operator.hpp"

#include <algorithm>
#include <map>
#include <mutex>
#include <shared_mutex>
#include <vector>

namespace pine {

// --- op_type helpers ---

const char* op_type_to_string(OpType t) {
    switch (t) {
        case OpType::Recall:    return "recall";
        case OpType::Transform: return "transform";
        case OpType::Filter:    return "filter";
        case OpType::Merge:     return "merge";
        case OpType::Reorder:   return "reorder";
        case OpType::Observe:   return "observe";
    }
    return "unknown";
}

const char* op_type_to_schema_string(OpType t) {
    switch (t) {
        case OpType::Recall:    return "Recall";
        case OpType::Transform: return "Transform";
        case OpType::Filter:    return "Filter";
        case OpType::Merge:     return "Merge";
        case OpType::Reorder:   return "Reorder";
        case OpType::Observe:   return "Observe";
    }
    return "Unknown";
}

namespace {

// Construct-on-first-use idiom — sidesteps the static initialization order
// fiasco. PINE_REGISTER_OPERATOR_T macros in translation units across the
// library may fire before any other translation unit, so we cannot rely on
// file-scope statics being initialized first.
std::shared_mutex& registry_mu() {
    static std::shared_mutex m;
    return m;
}

std::map<std::string, OperatorEntry>& registry_map() {
    static std::map<std::string, OperatorEntry> m;
    return m;
}

}  // namespace

void register_operator_with_traits(OperatorSchema schema,
                                   OperatorFactory factory,
                                   bool consumes_row_set,
                                   bool mutates_row_set,
                                   bool additive_writes_row_set,
                                   bool concurrent_safe) {
    if (schema.name.empty()) {
        throw RegistryError("pine: register_operator_with_traits called with empty name");
    }
    if (schema.description.empty()) {
        throw RegistryError("pine: operator \"" + schema.name + "\": description is required");
    }
    for (const auto& [pname, pschema] : schema.params) {
        if (pschema.description.empty()) {
            throw RegistryError("pine: operator \"" + schema.name + "\" param \"" + pname +
                                "\": description is required");
        }
    }
    if (!factory) {
        throw RegistryError("pine: operator \"" + schema.name + "\": factory must not be null");
    }

    OperatorEntry entry;
    entry.schema = std::move(schema);
    entry.factory = std::move(factory);
    entry.consumes_row_set        = consumes_row_set;
    entry.mutates_row_set         = mutates_row_set;
    entry.additive_writes_row_set = additive_writes_row_set;
    entry.concurrent_safe         = concurrent_safe;

    const std::string name = entry.schema.name;

    std::unique_lock<std::shared_mutex> lk(registry_mu());
    auto& reg = registry_map();
    if (reg.count(name)) {
        throw RegistryError("pine: duplicate operator registration: \"" + name + "\"");
    }
    reg.emplace(name, std::move(entry));
}

const OperatorEntry* registry_entry(const std::string& type_name) {
    std::shared_lock<std::shared_mutex> lk(registry_mu());
    const auto& reg = registry_map();
    auto it = reg.find(type_name);
    return it != reg.end() ? &it->second : nullptr;
}

std::vector<std::string> registered_operator_names() {
    std::shared_lock<std::shared_mutex> lk(registry_mu());
    const auto& reg = registry_map();
    std::vector<std::string> names;
    names.reserve(reg.size());
    for (const auto& [name, _] : reg) names.push_back(name);
    return names;
}

void apply_registry_traits(Config& config) {
    for (auto& [name, op] : config.operators) {
        const auto* entry = registry_entry(op.type_name);
        if (!entry) {
            throw RegistryError("operator \"" + name + "\": operator type not registered: \"" + op.type_name + "\"");
        }
        op.operator_type = op_type_to_string(entry->schema.type);
        if (entry->consumes_row_set)        op.consumes_row_set = true;
        if (entry->mutates_row_set)         op.mutates_row_set = true;
        if (entry->additive_writes_row_set) op.additive_writes_row_set = true;
        if (entry->concurrent_safe)         op.concurrent_safe = true;
    }
}

std::string export_schema_json() {
    auto names = registered_operator_names();
    std::sort(names.begin(), names.end());

    JsonValue::array_t arr;
    for (const auto& name : names) {
        const auto* entry = registry_entry(name);
        if (!entry) continue;
        const auto& schema = entry->schema;
        JsonValue::object_t obj;
        obj["Name"]        = JsonValue(schema.name);
        obj["Type"]        = JsonValue(op_type_to_schema_string(schema.type));
        obj["Description"] = JsonValue(schema.description);

        JsonValue::object_t params_obj;
        for (const auto& [pname, pschema] : schema.params) {
            JsonValue::object_t param;
            param["Type"]        = JsonValue(pschema.type);
            param["Required"]    = JsonValue(pschema.required);
            param["Default"]     = pschema.default_value;
            param["Description"] = JsonValue(pschema.description);
            params_obj[pname]    = JsonValue(param);
        }
        obj["Params"] = JsonValue(params_obj);

        arr.push_back(JsonValue(obj));
    }

    return dump_json(JsonValue(arr));
}

}  // namespace pine
