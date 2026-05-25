#pragma once

#include "pine/pine.hpp"

#include <map>
#include <string>
#include <vector>

namespace pine {

// Forward declaration: Frame is defined in frame.hpp.
class Frame;

// OperatorInput is a read-only snapshot of the Frame fields relevant to an
// operator, with item_defaults / common_defaults already substituted for nil
// values. Mirrors Go's BuildInput → OperatorInput pattern (column_frame.go),
// Java's Frame.buildInput(), and Python's Frame.build_input().
//
// Operators receive OperatorInput instead of raw Frame& so they never see nil
// when a default exists — the engine layer handles default substitution
// uniformly before dispatch.
class OperatorInput {
public:
    OperatorInput(std::map<std::string, JsonValue> common,
                  std::vector<std::map<std::string, JsonValue>> items,
                  const std::map<std::string, JsonValue>* resources);

    // common returns the value for the given field, or null if absent.
    JsonValue common(const std::string& field) const;

    // item_count returns the number of items.
    std::size_t item_count() const;

    // item returns the value for (index, field), or null if absent.
    JsonValue item(std::size_t index, const std::string& field) const;

    // common_keys returns all common field names present.
    std::vector<std::string> common_keys() const;

    // item_keys returns all field names present on a given item.
    std::vector<std::string> item_keys(std::size_t index) const;

    // resources returns the injected resource map (may be nullptr).
    const std::map<std::string, JsonValue>* resources() const { return resources_; }

private:
    std::map<std::string, JsonValue> common_;
    std::vector<std::map<std::string, JsonValue>> items_;
    const std::map<std::string, JsonValue>* resources_ = nullptr;
};

// build_operator_input constructs an OperatorInput from a Frame and the
// operator's InputFieldSpec. Throws ExecutionError for strict field violations.
OperatorInput build_operator_input(const Frame& frame,
                                   const std::string& op_name,
                                   const InputFieldSpec& spec);

}  // namespace pine
