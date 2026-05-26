#pragma once

#include "pine/pine.hpp"

#include <map>
#include <string>
#include <vector>

namespace pine {

class Frame;

// OperatorInput is a lazy read-only proxy over Frame + InputFieldSpec.
// item(i, field) reads from Frame on demand, substituting defaults for
// nil values. Avoids the O(N×M) eager reify that the old implementation
// performed (copying every item×field into a vector<map>).
//
// Operators receive `const OperatorInput&` and call item()/common() —
// the lazy proxy is transparent to them.
//
// Strict field validation is done once by build_operator_input before
// constructing the proxy (PERF-1a).
class OperatorInput {
public:
    OperatorInput(const Frame& frame, const InputFieldSpec& spec);

    // common returns the value for the given field, or null if absent.
    // Substitutes defaults for nil values.
    JsonValue common(const std::string& field) const;

    // item_count returns the number of items.
    std::size_t item_count() const;

    // item returns the value for (index, field), or null if absent.
    // Substitutes defaults for nil values.
    JsonValue item(std::size_t index, const std::string& field) const;

    // common_keys returns all common field names present in the spec.
    std::vector<std::string> common_keys() const;

    // item_keys returns all item field names present in the spec.
    std::vector<std::string> item_keys(std::size_t index) const;

    // resources returns the injected resource map (may be nullptr).
    const std::map<std::string, JsonValue>* resources() const;

private:
    const Frame* frame_;
    const InputFieldSpec* spec_;
};

// build_operator_input constructs an OperatorInput from a Frame and the
// operator's InputFieldSpec. Throws ExecutionError for strict field violations.
OperatorInput build_operator_input(const Frame& frame,
                                   const std::string& op_name,
                                   const InputFieldSpec& spec);

}  // namespace pine
