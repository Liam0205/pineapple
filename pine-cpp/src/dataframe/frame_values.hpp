#pragma once

// Write-time value check shared by RowFrame and ColumnFrame. Mirrors pine-go
// validateValue (internal/dataframe/row_frame.go) and pine-java
// FrameValues.checkValue: the message is part of the cross-runtime error
// contract and must stay byte-identical.

#include "pine/pine.hpp"

#include <cmath>
#include <string>

namespace pine {
namespace detail {

// Bound on the descent into nested composites: a composite at this depth or
// deeper is not inspected (scalars are checked at any depth their parent
// reaches). The rule depends only on the value, not on key order, and matches
// pine-go maxCompositeScanDepth and pine-java MAX_COMPOSITE_SCAN_DEPTH. A
// Variant is a value tree and cannot reference itself, so no cycle check is
// needed here; the bound keeps deep nesting from growing the stack.
inline constexpr int kMaxCompositeScanDepth = 1000;

inline bool contains_non_finite(const Variant& v, int depth) {
  if (v.is_number()) {
    double d = v.as_number();
    return std::isnan(d) || std::isinf(d);
  }
  if ((!v.is_array() && !v.is_object()) || depth >= kMaxCompositeScanDepth) {
    return false;
  }
  if (v.is_array()) {
    for (const auto& e : v.as_array()) {
      if (contains_non_finite(e, depth + 1)) {
        return true;
      }
    }
    return false;
  }
  for (const auto& [k, e] : v.as_object()) {
    if (contains_non_finite(e, depth + 1)) {
      return true;
    }
  }
  return false;
}

// Rejects NaN/Inf in any numeric write — including one nested inside an
// array or object (issue #210): those serialize to invalid JSON and silently
// corrupt downstream consumers. Called from apply_output's write phases
// (common, items, additions). Returns the violation message (without the
// `pine:`/op prefix) or empty when OK. Variant only carries JSON-representable
// types, so Go's "unsupported type" branch cannot fire here.
inline std::string validate_frame_value(const std::string& field, const Variant& value) {
  if (contains_non_finite(value, 0)) {
    return "field \"" + field + "\": NaN/Inf is not a valid JSON value";
  }
  return "";
}

}  // namespace detail
}  // namespace pine
