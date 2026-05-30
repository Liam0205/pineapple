#pragma once
#include "pine/column_frame.hpp"
#include "pine/operator_input.hpp"
#include "pine/pine.hpp"

#include <cstdint>
#include <string>
#include <vector>

namespace pine {
namespace operators {

using Frame = pine::Frame;

// Exception type for operator-internal errors (converted to ExecutionError by caller).
class OperatorError : public std::runtime_error {
 public:
  using std::runtime_error::runtime_error;
};

double to_double(const Variant& value);

// json_type_name returns the Go-reflect-style type name for a Variant.
// Used in operator error messages to mirror Go's `%T` output (e.g.
// `[]interface {}` and `map[string]interface {}`). Consolidation —
// previously each operator that needed this had a private copy that
// returned the C++-native form (`array` / `object`), creating two
// inconsistent vocabularies in the same codebase.
std::string json_type_name(const Variant& value);

Variant require_common(const Frame& frame, const OperatorConfig& op, const std::string& field);
Variant require_item(const Frame& frame, const OperatorConfig& op, std::size_t index,
                       const std::string& field);

// Variants that take the operator name + defaults map directly. Operators
// that cached `op_name_` / `common_defaults_` / `item_defaults_` on `init`
// (transform_copy, transform_normalize, reorder_sort, ...) used to keep a
// per-class copy of these helpers; consolidated to one place so future
// error-message tweaks land once.
Variant require_common_by_name(const Frame& frame, const std::map<std::string, Variant>& defaults,
                                 const std::string& field);
Variant require_item_by_name(const Frame& frame, std::size_t index,
                               const std::map<std::string, Variant>& defaults, const std::string& field);

std::string go_format_g(double d);
// go_format_lookup_key mirrors pine-go transform/resource_lookup.go:91-96:
// integer-valued floats serialize with FormatInt (no decimal point) and
// non-integer floats with FormatFloat(v, 'f', -1, 64) — never scientific.
// transform_resource_lookup uses this for table keys so `1e-5` (lookup
// value) matches the `0.00001` key form pine-go produces.
std::string go_format_lookup_key(double d);
std::string sprint_value(const Variant& v);
std::string any_to_string(const Variant& v);
std::string dedup_key(const Variant& v);
std::string build_key_suffix(const Frame& frame, const std::vector<std::string>& fields);
std::string build_key_suffix(const OperatorInput& input, const std::vector<std::string>& fields);
std::vector<std::string> json_to_string_slice(const Variant& v);

struct RedisParams {
  std::string host;
  int port = 6379;
  std::string password;
  int db = 0;
  std::string key_prefix;
  std::string data_type = "string";
  int ttl = 0;
  bool fail_on_error = false;
};

RedisParams parse_redis_params(const OperatorConfig& op);

}  // namespace operators
}  // namespace pine
