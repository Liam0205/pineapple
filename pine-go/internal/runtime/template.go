package runtime

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

// templateMarker matches {{field_name}} markers in templated param values.
// Mirrors the Apple-side pattern in apple/template.py (issue #74).
var templateMarker = regexp.MustCompile(`\{\{(\w+)\}\}`)

// bareTemplateMarker enforces the L0 contract: a templated value must
// consist of exactly one {{field}} marker, with no literal text around
// or between markers. Mixed forms like "prefix_{{x}}" are rejected at
// engine build time so the runtime path stays a pure named-param
// binding (no string concatenation, no coercion ambiguity).
var bareTemplateMarker = regexp.MustCompile(`^\{\{(\w+)\}\}$`)

// TemplatedParam is the pre-compiled plan for one templated operator param,
// computed at engine build time so per-request work stays small.
type TemplatedParam struct {
	// Name is the param name as it appears in the operator's schema.
	Name string
	// ScalarType is the param's declared scalar type — one of
	// "string", "int64", "float64", "bool". Used for coercion of the
	// resolved field value. The Apple-side validator already restricts
	// this set, but we re-check at runtime init too.
	ScalarType string
	// Field is the single common-field name this param binds to. The
	// L0 contract guarantees exactly one field per templated param.
	Field string
}

// IsTemplatedString reports whether v is a string carrying a {{field}} marker.
func IsTemplatedString(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	return templateMarker.MatchString(s)
}

// extractBareField returns the single field name from a bare "{{field}}"
// value, or ("", false) if the value contains literal text or multiple
// markers. This enforces the L0 contract at engine build time.
func extractBareField(s string) (string, bool) {
	m := bareTemplateMarker.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// templatableScalarTypes is the set of declared types eligible for
// per-request interpolation. Mirrors apple/validator._TEMPLATABLE_SCALAR_TYPES.
var templatableScalarTypes = map[string]struct{}{
	"string":  {},
	"int":     {},
	"int64":   {},
	"float":   {},
	"float64": {},
	"bool":    {},
}

// normalizeScalarType maps Apple/codegen type spellings to a canonical
// internal form used by ResolveTemplatedParams for coercion dispatch.
func normalizeScalarType(t string) string {
	switch t {
	case "int", "int64":
		return "int64"
	case "float", "float64":
		return "float64"
	}
	return t
}

// BuildTemplatedParamPlan scans an operator's RawParams against its schema
// and returns the per-param interpolation plan, plus an error if any
// templated value targets a non-templatable or non-scalar param. Called
// once at engine build time per operator.
//
// Returns (nil, nil) when the operator has no templated params — callers
// should treat an empty plan as "skip per-request resolution entirely".
func BuildTemplatedParamPlan(
	opName string,
	schema types.OperatorSchema,
	rawParams map[string]any,
) ([]TemplatedParam, error) {
	var plan []TemplatedParam
	for paramName, raw := range rawParams {
		if !IsTemplatedString(raw) {
			continue
		}
		spec, ok := schema.Params[paramName]
		if !ok {
			// Apple validator already rejects unknown params; runtime
			// reaching this branch implies a hand-edited config.
			// Wrap as ConfigError so the CLI surface carries the
			// `pine: config error:` prefix byte-for-byte with pine-cpp
			// and pine-java (cross-runtime error wording is contract).
			return nil, &types.ConfigError{Message: fmt.Sprintf(
				"operator %q: param %q is not declared in schema",
				opName, paramName)}
		}
		if !spec.Templatable {
			return nil, &types.ConfigError{Message: fmt.Sprintf(
				"operator %q: param %q is not declared templatable in schema",
				opName, paramName)}
		}
		if _, ok := templatableScalarTypes[spec.Type]; !ok {
			return nil, &types.ConfigError{Message: fmt.Sprintf(
				"operator %q: param %q has declared type %q which does not support templating",
				opName, paramName, spec.Type)}
		}
		tmpl := raw.(string)
		field, ok := extractBareField(tmpl)
		if !ok {
			// L0 contract violation. Apple validator already rejects
			// this at compile time; we re-check at engine init in case
			// of hand-edited JSON or an older codegen still in flight.
			return nil, &types.ConfigError{Message: fmt.Sprintf(
				"operator %q: param %q value %q must be a bare {{field}} marker",
				opName, paramName, tmpl)}
		}
		plan = append(plan, TemplatedParam{
			Name:       paramName,
			ScalarType: normalizeScalarType(spec.Type),
			Field:      field,
		})
	}
	return plan, nil
}

// ResolveTemplatedParams expands a pre-built plan against the current
// request's common frame and returns the {paramName -> coercedValue}
// map. The scheduler attaches it to the per-request OperatorInput via
// SetTemplatedParams; operators read it via input.TemplatedParam(name).
//
// The lookup intentionally consults the raw request frame rather than
// the operator's filtered OperatorInput: template source fields live in
// meta.common_input_template (#74), which is excluded from the operator
// view so that operators cannot accidentally observe them via
// input.Common. The DAG still tracks them as read dependencies via
// Metadata.CommonReadFields, guaranteeing the producing operator (or
// the request itself) has populated the frame before this call.
//
// Error wording is fixed and shared with pine-java / pine-cpp for
// byte-exact cross-runtime parity (issue #74). The returned error
// intentionally OMITS the `operator "X":` prefix because the scheduler
// wraps every fatal in `types.ExecutionError`, which already prefixes
// `pine: execution error in operator "X": ...`. Duplicating the
// operator name would diverge from the cross-runtime contract.
func ResolveTemplatedParams(
	opName string,
	plan []TemplatedParam,
	frame FrameReader,
) (map[string]any, error) {
	if len(plan) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(plan))
	for _, p := range plan {
		val := frame.Common(p.Field)
		if val == nil {
			// Either truly missing or present-but-nil. Apple side
			// declares the field in meta.common_input_template so
			// missing here means no upstream operator (or the
			// request itself) populated it.
			return nil, fmt.Errorf(
				"templated param %q references common field %q which is missing",
				p.Name, p.Field)
		}
		// Stringify-then-coerce keeps semantics identical regardless of
		// whether the frame holds the field as int64 or as decimal text;
		// the cross-runtime contract is "the coerced result is the same
		// for any source representation of the same scalar value".
		coerced, err := coerceScalar(p.Name, p.ScalarType, fmt.Sprint(val))
		if err != nil {
			return nil, err
		}
		out[p.Name] = coerced
	}
	return out, nil
}

// FrameReader is the minimal contract ResolveTemplatedParams needs from
// the per-request frame. dataframe.Frame satisfies it directly; tests
// can stub it without dragging the full frame surface.
type FrameReader interface {
	Common(field string) any
}

// coerceScalar parses the substituted string into the declared scalar
// type. The error wording is shared with the other runtimes verbatim.
func coerceScalar(paramName, scalarType, s string) (any, error) {
	switch scalarType {
	case "string":
		return s, nil
	case "int64":
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, coerceErr(paramName, scalarType, s)
		}
		return v, nil
	case "float64":
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, coerceErr(paramName, scalarType, s)
		}
		return v, nil
	case "bool":
		v, err := strconv.ParseBool(s)
		if err != nil {
			return nil, coerceErr(paramName, scalarType, s)
		}
		return v, nil
	}
	// Unreachable: BuildTemplatedParamPlan rejects non-scalar types up front.
	return nil, fmt.Errorf(
		"templated param %q has unsupported scalar type %q",
		paramName, scalarType)
}

func coerceErr(paramName, scalarType, s string) error {
	return fmt.Errorf(
		"templated param %q cannot coerce %q to %s",
		paramName, s, scalarType)
}
