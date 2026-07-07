#pragma once

#include "pine/pine.hpp"

#include <map>
#include <string>
#include <unordered_map>
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
  Variant common(const std::string& field) const;

  // item_count returns the number of items.
  std::size_t item_count() const {
    return cached_item_count_;
  }

  // item returns the value for (index, field), or null if absent.
  // Substitutes defaults for nil values.
  Variant item(std::size_t index, const std::string& field) const;

  // item_column returns all values of `field` as a vector indexed by item
  // position — element i is identical to item(i, field), including
  // item-default substitution for nil slots. Collapses the per-element
  // lock + column lookup of an item() loop to once per column (the
  // access shape where column storage's contiguous layout pays off).
  std::vector<Variant> item_column(const std::string& field) const;

  // common_keys returns all common field names present in the spec.
  std::vector<std::string> common_keys() const;

  // item_keys returns all item field names present in the spec.
  std::vector<std::string> item_keys(std::size_t index) const;

  // resources returns the injected resource map (may be nullptr).
  const std::map<std::string, Variant>* resources() const;

  // templated_param returns the resolved + coerced value for a templated
  // param declared on this operator (issue #74). Returns a null Variant
  // when the param was not templated or no templated params were resolved
  // for this request. Read-only: the map is shared across data_parallel
  // shards by const-pointer.
  Variant templated_param(const std::string& name) const;

  // Engine-internal: install the per-request resolved {{field}} map.
  // Storing a non-owning pointer keeps copies out of the hot path and
  // lets parallel shards share the parent's map (which lives for the
  // entire dispatch call frame).
  void set_templated_params(const std::unordered_map<std::string, Variant>* resolved) {
    templated_ = resolved;
  }

 private:
  const Frame* frame_;
  const InputFieldSpec* spec_;
  std::size_t cached_item_count_;
  const std::unordered_map<std::string, Variant>* templated_ = nullptr;
};

// build_operator_input constructs an OperatorInput from a Frame and the
// operator's InputFieldSpec. Throws ExecutionError for strict field violations.
OperatorInput build_operator_input(const Frame& frame, const std::string& op_name,
                                   const InputFieldSpec& spec);

}  // namespace pine
