#include "pine/template.hpp"

#include <cerrno>
#include <cstdint>
#include <cstdlib>
#include <optional>
#include <regex>
#include <unordered_set>

#include "operators/_helpers.hpp"  // for go_format_g — shared Go fmt.Sprint(float) parity

namespace pine {

namespace {

// Mirrors pine-go internal/runtime/template.go templateMarker.
// PCRE-style alternative used because std::regex_constants::ECMAScript
// treats `\w` identically to Go's [_A-Za-z0-9].
const std::regex& template_marker() {
  static const std::regex re(R"(\{\{(\w+)\}\})");
  return re;
}

// bare_template_marker enforces the L0 contract: a templated value
// must consist of exactly one {{field}} marker, with no literal text
// around or between markers.
const std::regex& bare_template_marker() {
  static const std::regex re(R"(^\{\{(\w+)\}\}$)");
  return re;
}

const std::unordered_set<std::string>& templatable_scalar_types() {
  static const std::unordered_set<std::string> s = {"string", "int", "int64", "float", "float64", "bool"};
  return s;
}

std::string normalize_scalar_type(const std::string& t) {
  if (t == "int" || t == "int64") {
    return "int64";
  }
  if (t == "float" || t == "float64") {
    return "float64";
  }
  return t;
}

// Returns the single field name from a bare "{{field}}" value, or an
// empty optional when the value contains literal text or multiple
// markers. Enforces the L0 contract at engine build time.
std::optional<std::string> extract_bare_field(const std::string& s) {
  std::smatch m;
  if (!std::regex_match(s, m, bare_template_marker())) {
    return std::nullopt;
  }
  return m[1].str();
}

// stringify_common mirrors Go's fmt.Sprint(any) for the scalar Variants
// the common frame can plausibly carry: bool, number, string. Composite
// types are not a supported template source; we fall back to JSON to
// keep the operator's coerce-failure error message stable rather than
// crashing.
std::string stringify_common(const Variant& v) {
  if (v.is_string()) {
    return v.as_string();
  }
  if (v.is_bool()) {
    return v.as_bool() ? "true" : "false";
  }
  if (v.is_number()) {
    return operators::go_format_g(v.as_number());
  }
  return dump_json(v, 0);
}

// parse_int64_strict mirrors Go's strconv.ParseInt(s, 10, 64): rejects
// leading/trailing whitespace, empty input, and any non-decimal trailing
// character. Sign optional.
bool parse_int64_strict(const std::string& s, int64_t& out) {
  if (s.empty()) {
    return false;
  }
  const char* first = s.c_str();
  char* end = nullptr;
  errno = 0;
  long long v = std::strtoll(first, &end, 10);
  if (errno != 0) {
    return false;
  }
  if (end != first + s.size()) {
    return false;
  }
  out = static_cast<int64_t>(v);
  return true;
}

// parse_float64_strict mirrors Go's strconv.ParseFloat(s, 64). Accepts
// scientific notation, "Inf"/"NaN" (case-insensitive — strtod handles
// "INF"/"NaN" the same way Go does).
bool parse_float64_strict(const std::string& s, double& out) {
  if (s.empty()) {
    return false;
  }
  const char* first = s.c_str();
  char* end = nullptr;
  errno = 0;
  double v = std::strtod(first, &end);
  if (end != first + s.size()) {
    return false;
  }
  // Go's strconv.ParseFloat returns ErrRange on overflow but still
  // produces ±Inf; we accept that here (matches "1e500" → +Inf coerce).
  out = v;
  return true;
}

// parse_strict_bool matches Go's strconv.ParseBool: accepts
// 1/0/t/f/T/F/true/false/TRUE/FALSE/True/False. Anything else fails.
bool parse_strict_bool(const std::string& s, bool& out) {
  if (s == "1" || s == "t" || s == "T" || s == "true" || s == "TRUE" || s == "True") {
    out = true;
    return true;
  }
  if (s == "0" || s == "f" || s == "F" || s == "false" || s == "FALSE" || s == "False") {
    out = false;
    return true;
  }
  return false;
}

// coerce_error_message returns the canonical runtime coerce error string.
// Format is byte-exact across runtimes (issue #74): no `operator "X":`
// prefix — the scheduler's ExecutionError wrap adds it once.
std::string coerce_error_message(const std::string& param_name, const std::string& scalar_type,
                                 const std::string& s) {
  return "templated param \"" + param_name + "\" cannot coerce \"" + s + "\" to " + scalar_type;
}

Variant coerce_scalar(const std::string& param_name, const std::string& scalar_type, const std::string& s) {
  if (scalar_type == "string") {
    return Variant(s);
  }
  if (scalar_type == "int64") {
    int64_t v = 0;
    if (!parse_int64_strict(s, v)) {
      throw ExecutionError(coerce_error_message(param_name, scalar_type, s));
    }
    return Variant(static_cast<double>(v));
  }
  if (scalar_type == "float64") {
    double v = 0.0;
    if (!parse_float64_strict(s, v)) {
      throw ExecutionError(coerce_error_message(param_name, scalar_type, s));
    }
    return Variant(v);
  }
  if (scalar_type == "bool") {
    bool v = false;
    if (!parse_strict_bool(s, v)) {
      throw ExecutionError(coerce_error_message(param_name, scalar_type, s));
    }
    return Variant(v);
  }
  // Unreachable: build_templated_param_plan rejects non-scalar types up front.
  throw ExecutionError("templated param \"" + param_name + "\" has unsupported scalar type \"" + scalar_type +
                       "\"");
}

}  // namespace

bool is_templated_string(const Variant& v) {
  if (!v.is_string()) {
    return false;
  }
  return std::regex_search(v.as_string(), template_marker());
}

bool is_bare_marker(const std::string& s) {
  return std::regex_match(s, bare_template_marker());
}

std::vector<TemplatedParam> build_templated_param_plan(const std::string& op_name,
                                                       const OperatorSchema& schema,
                                                       const Variant& raw_params) {
  std::vector<TemplatedParam> plan;
  if (!raw_params.is_object()) {
    return plan;
  }
  const auto& obj = raw_params.as_object();
  for (const auto& [param_name, raw] : obj) {
    if (!is_templated_string(raw)) {
      continue;
    }
    auto sit = schema.params.find(param_name);
    if (sit == schema.params.end()) {
      throw ConfigError("operator \"" + op_name + "\": param \"" + param_name +
                        "\" is not declared in schema");
    }
    const ParamSchema& spec = sit->second;
    if (!spec.templatable) {
      throw ConfigError("operator \"" + op_name + "\": param \"" + param_name +
                        "\" is not declared templatable in schema");
    }
    if (templatable_scalar_types().find(spec.type) == templatable_scalar_types().end()) {
      throw ConfigError("operator \"" + op_name + "\": param \"" + param_name + "\" has declared type \"" +
                        spec.type + "\" which does not support templating");
    }
    const std::string& tmpl = raw.as_string();
    auto field = extract_bare_field(tmpl);
    if (!field.has_value()) {
      // L0 contract violation. Apple validator rejects this at compile
      // time; we re-check at engine init for hand-edited JSON.
      throw ConfigError("operator \"" + op_name + "\": param \"" + param_name + "\" value \"" + tmpl +
                        "\" must be a bare {{field}} marker");
    }
    TemplatedParam p;
    p.name = param_name;
    p.scalar_type = normalize_scalar_type(spec.type);
    p.field = *field;
    plan.push_back(std::move(p));
  }
  return plan;
}

std::unordered_map<std::string, Variant> resolve_templated_params(const std::string& op_name,
                                                                  const std::vector<TemplatedParam>& plan,
                                                                  const Frame& frame) {
  (void)op_name;  // op name is added by ExecutionError wrap layer (scheduler).
  std::unordered_map<std::string, Variant> out;
  if (plan.empty()) {
    return out;
  }
  out.reserve(plan.size());
  for (const auto& p : plan) {
    Variant val = frame.common(p.field);
    if (val.is_null()) {
      throw ExecutionError("templated param \"" + p.name + "\" references common field \"" + p.field +
                           "\" which is missing");
    }
    out.emplace(p.name, coerce_scalar(p.name, p.scalar_type, stringify_common(val)));
  }
  return out;
}

}  // namespace pine
