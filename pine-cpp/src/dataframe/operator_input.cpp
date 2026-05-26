#include "pine/operator_input.hpp"
#include "pine/frame.hpp"

namespace pine {

OperatorInput::OperatorInput(std::map<std::string, JsonValue> common,
                             std::vector<std::map<std::string, JsonValue>> items,
                             const std::map<std::string, JsonValue>* resources)
    : common_(std::move(common)), items_(std::move(items)), resources_(resources) {}

JsonValue OperatorInput::common(const std::string& field) const {
    auto it = common_.find(field);
    if (it != common_.end()) return it->second;
    return JsonValue(nullptr);
}

std::size_t OperatorInput::item_count() const {
    return items_.size();
}

JsonValue OperatorInput::item(std::size_t index, const std::string& field) const {
    if (index >= items_.size()) return JsonValue(nullptr);
    auto it = items_[index].find(field);
    if (it != items_[index].end()) return it->second;
    return JsonValue(nullptr);
}

std::vector<std::string> OperatorInput::common_keys() const {
    std::vector<std::string> keys;
    keys.reserve(common_.size());
    for (const auto& [k, _] : common_) keys.push_back(k);
    return keys;
}

std::vector<std::string> OperatorInput::item_keys(std::size_t index) const {
    if (index >= items_.size()) return {};
    std::vector<std::string> keys;
    keys.reserve(items_[index].size());
    for (const auto& [k, _] : items_[index]) keys.push_back(k);
    return keys;
}

InputFieldSpec compute_input_field_spec(const OperatorConfig& config) {
    InputFieldSpec spec;
    for (const auto& field : config.metadata.common_input) {
        auto def_it = config.common_defaults.find(field);
        if (def_it != config.common_defaults.end()) {
            spec.defaulted_common.push_back({field, def_it->second});
        } else {
            spec.strict_common.push_back(field);
        }
    }
    for (const auto& field : config.metadata.item_input) {
        auto def_it = config.item_defaults.find(field);
        if (def_it != config.item_defaults.end()) {
            spec.defaulted_item.push_back({field, def_it->second});
        } else {
            spec.strict_item.push_back(field);
        }
    }
    return spec;
}

OperatorInput build_operator_input(const Frame& frame,
                                   const std::string& op_name,
                                   const InputFieldSpec& spec) {
    // Build common map
    std::map<std::string, JsonValue> common;
    for (const auto& field : spec.strict_common) {
        JsonValue v = frame.common(field);
        if (v.is_null()) {
            throw ExecutionError(op_name, "required field \"" + field + "\" is nil in common");
        }
        common[field] = std::move(v);
    }
    for (const auto& df : spec.defaulted_common) {
        JsonValue v = frame.common(df.name);
        if (v.is_null()) {
            common[df.name] = df.default_value;
        } else {
            common[df.name] = std::move(v);
        }
    }

    // Batch-validate strict item fields (PERF-1a: ColumnFrame uses bitmap scan)
    if (!spec.strict_item.empty()) {
        auto [bad_field, bad_row] = frame.validate_strict_items(spec.strict_item);
        if (bad_row >= 0) {
            throw ExecutionError(op_name, "required field \"" + bad_field + "\" is nil on item[" + std::to_string(bad_row) + "]");
        }
    }

    // Build items
    std::vector<std::map<std::string, JsonValue>> items;
    items.reserve(frame.item_count());
    for (std::size_t i = 0; i < frame.item_count(); ++i) {
        std::map<std::string, JsonValue> row;
        for (const auto& field : spec.strict_item) {
            row[field] = frame.item(i, field);
        }
        for (const auto& df : spec.defaulted_item) {
            JsonValue v = frame.item(i, df.name);
            if (v.is_null()) {
                row[df.name] = df.default_value;
            } else {
                row[df.name] = std::move(v);
            }
        }
        items.push_back(std::move(row));
    }

    return OperatorInput(std::move(common), std::move(items), frame.resources());
}

}  // namespace pine
