#pragma once
#include "pine/frame.hpp"
#include "pine/operator.hpp"
#include "pine/pine.hpp"

#include <string>
#include <unordered_map>
#include <vector>

namespace pine {

// TemplatedParam captures the compiled per-param plan for {{field}}
// interpolation (issue #74). Built once at Engine construction from
// the operator's raw params + schema; resolved each request against
// OperatorInput::common.
//
// The L0 contract limits each templated value to a single bare
// {{field}} marker (validated at build_templated_param_plan time), so
// `field` carries exactly one common-frame field name -- no literal
// text, no concatenation. String composition is delegated to upstream
// operators (e.g. transform_by_lua).
struct TemplatedParam {
  std::string name;         // param key, e.g. "user_id"
  std::string scalar_type;  // canonical: "string" | "int64" | "float64" | "bool"
  std::string field;        // single common-field name (L0 contract)
};

// Returns true iff `v` is a string carrying at least one {{field}} marker.
bool is_templated_string(const Variant& v);

// Scans an operator's raw params against its schema and returns the
// per-param interpolation plan. Returns an empty vector when no
// templated params are present. Throws ConfigError when a templated
// value targets a non-templatable / non-scalar / unknown param. The
// thrown what() carries the canonical `operator "X": ...` prefix.
//
// `raw_params` may be null/empty (no params); in that case the plan
// is empty and no error is raised even if templating would be illegal
// — there is nothing to interpolate.
std::vector<TemplatedParam> build_templated_param_plan(const std::string& op_name,
                                                       const OperatorSchema& schema,
                                                       const Variant& raw_params);

// Expands a pre-built plan against the current request's common frame
// and returns the {paramName -> coercedValue} map. The scheduler attaches
// it to the per-request OperatorInput via set_templated_params; operators
// read it via input.templated_param(name). Throws ExecutionError
// (single-arg form, no op prefix — `dispatch_with_recovery` adds it)
// on missing common field or coercion failure.
//
// Reads directly from the raw request Frame (not the filtered
// OperatorInput) so that template source fields declared only in the
// `common_input_template` bucket — and therefore hidden from the
// operator-visible input — are still accessible. See issue #74.
std::unordered_map<std::string, Variant> resolve_templated_params(const std::string& op_name,
                                                                  const std::vector<TemplatedParam>& plan,
                                                                  const Frame& frame);

}  // namespace pine
