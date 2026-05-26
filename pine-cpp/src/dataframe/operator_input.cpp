#include "pine/operator_input.hpp"
#include "pine/frame.hpp"

namespace pine {

OperatorInput::OperatorInput(const Frame& frame, const InputFieldSpec& spec)
    : frame_(&frame), spec_(&spec) {}

JsonValue OperatorInput::common(const std::string& field) const {
    JsonValue v = frame_->common(field);
    if (!v.is_null()) return v;
    for (const auto& df : spec_->defaulted_common) {
        if (df.name == field) return df.default_value;
    }
    return JsonValue(nullptr);
}

std::size_t OperatorInput::item_count() const {
    return frame_->item_count();
}

JsonValue OperatorInput::item(std::size_t index, const std::string& field) const {
    if (index >= frame_->item_count()) return JsonValue(nullptr);
    JsonValue v = frame_->item(index, field);
    if (!v.is_null()) return v;
    for (const auto& df : spec_->defaulted_item) {
        if (df.name == field) return df.default_value;
    }
    return JsonValue(nullptr);
}

std::vector<std::string> OperatorInput::common_keys() const {
    std::vector<std::string> keys;
    for (const auto& f : spec_->strict_common) keys.push_back(f);
    for (const auto& df : spec_->defaulted_common) keys.push_back(df.name);
    return keys;
}

std::vector<std::string> OperatorInput::item_keys(std::size_t index) const {
    (void)index;
    std::vector<std::string> keys;
    for (const auto& f : spec_->strict_item) keys.push_back(f);
    for (const auto& df : spec_->defaulted_item) keys.push_back(df.name);
    return keys;
}

const std::map<std::string, JsonValue>* OperatorInput::resources() const {
    return frame_->resources();
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
    // Validate strict common fields
    for (const auto& field : spec.strict_common) {
        JsonValue v = frame.common(field);
        if (v.is_null()) {
            throw ExecutionError(op_name, "required field \"" + field + "\" is nil in common");
        }
    }

    // Batch-validate strict item fields (PERF-1a)
    if (!spec.strict_item.empty()) {
        auto [bad_field, bad_row] = frame.validate_strict_items(spec.strict_item);
        if (bad_row >= 0) {
            throw ExecutionError(op_name, "required field \"" + bad_field + "\" is nil on item[" + std::to_string(bad_row) + "]");
        }
    }

    // Return lazy proxy — no eager reify of items (PERF-1b)
    return OperatorInput(frame, spec);
}

}  // namespace pine
