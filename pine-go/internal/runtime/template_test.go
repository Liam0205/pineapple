package runtime

import (
	"strings"
	"testing"

	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

// schema helper — builds a minimal OperatorSchema with one templatable param.
func tpSchema(name, typ string, templatable bool) types.OperatorSchema {
	return types.OperatorSchema{
		Name:        "op_a",
		Description: "test",
		Params: map[string]types.ParamSpec{
			name: {
				Type:        typ,
				Description: "x",
				Templatable: templatable,
			},
		},
	}
}

func TestIsTemplatedString(t *testing.T) {
	cases := []struct {
		v    any
		want bool
	}{
		{"plain", false},
		{"{{x}}", true},
		{"prefix-{{x}}-suffix", true},
		{"{{}}", false},
		{nil, false},
		{42, false},
		{true, false},
		{[]string{"{{x}}"}, false},
	}
	for _, c := range cases {
		if got := IsTemplatedString(c.v); got != c.want {
			t.Errorf("IsTemplatedString(%#v) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestExtractBareField(t *testing.T) {
	cases := []struct {
		in     string
		field  string
		isBare bool
	}{
		{"{{user_id}}", "user_id", true},
		{"prefix-{{x}}", "", false},
		{"{{x}}-suffix", "", false},
		{"tenant:{{tenant_id}}:", "", false},
		{"{{a}}{{b}}", "", false},
		{"{{}}", "", false},
		{"plain", "", false},
	}
	for _, c := range cases {
		gotField, gotIsBare := extractBareField(c.in)
		if gotField != c.field || gotIsBare != c.isBare {
			t.Errorf("extractBareField(%q) = (%q, %v), want (%q, %v)",
				c.in, gotField, gotIsBare, c.field, c.isBare)
		}
	}
}

func TestBuildTemplatedParamPlan_NoTemplated(t *testing.T) {
	plan, err := BuildTemplatedParamPlan("name", tpSchema("k", "string", true),
		map[string]any{"k": "no markers"})
	if err != nil {
		t.Fatal(err)
	}
	if plan != nil {
		t.Errorf("expected nil plan, got %v", plan)
	}
}

func TestBuildTemplatedParamPlan_Happy(t *testing.T) {
	plan, err := BuildTemplatedParamPlan("name",
		tpSchema("k", "int64", true),
		map[string]any{"k": "{{user_id}}"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].Name != "k" || plan[0].ScalarType != "int64" ||
		plan[0].Field != "user_id" {
		t.Errorf("unexpected plan: %+v", plan)
	}
}

func TestBuildTemplatedParamPlan_RejectsNonTemplatable(t *testing.T) {
	_, err := BuildTemplatedParamPlan("name",
		tpSchema("k", "string", false),
		map[string]any{"k": "{{x}}"})
	if err == nil || !strings.Contains(err.Error(), `param "k" is not declared templatable`) {
		t.Errorf("want non-templatable error, got %v", err)
	}
}

func TestBuildTemplatedParamPlan_RejectsUnknownParam(t *testing.T) {
	_, err := BuildTemplatedParamPlan("name",
		tpSchema("k", "string", true),
		map[string]any{"missing": "{{x}}"})
	if err == nil || !strings.Contains(err.Error(), `param "missing" is not declared in schema`) {
		t.Errorf("want unknown-param error, got %v", err)
	}
}

func TestBuildTemplatedParamPlan_RejectsNonScalarType(t *testing.T) {
	_, err := BuildTemplatedParamPlan("name",
		tpSchema("k", "string_list", true),
		map[string]any{"k": "{{x}}"})
	if err == nil || !strings.Contains(err.Error(), `does not support templating`) {
		t.Errorf("want non-scalar error, got %v", err)
	}
}

func TestBuildTemplatedParamPlan_RejectsNonBareMarker(t *testing.T) {
	// L0 contract: literal text around the marker is rejected at engine
	// build time. Apple validator catches this earlier, but the runtime
	// re-checks in case of hand-edited JSON.
	for _, bad := range []string{
		"prefix-{{x}}",
		"{{x}}-suffix",
		"tenant:{{tenant_id}}:",
		"{{a}}{{b}}",
	} {
		_, err := BuildTemplatedParamPlan("name",
			tpSchema("k", "string", true),
			map[string]any{"k": bad})
		if err == nil || !strings.Contains(err.Error(), "must be a bare {{field}} marker") {
			t.Errorf("value %q: want bare-marker error, got %v", bad, err)
		}
	}
}

func TestBuildTemplatedParamPlan_AllScalarTypes(t *testing.T) {
	for _, typ := range []string{"string", "int", "int64", "float", "float64", "bool"} {
		_, err := BuildTemplatedParamPlan("name",
			tpSchema("k", typ, true),
			map[string]any{"k": "{{x}}"})
		if err != nil {
			t.Errorf("type %s: %v", typ, err)
		}
	}
}

// resolveInput builds a minimal *types.OperatorInput whose Common(field)
// returns from the given map.
func resolveInput(common map[string]any) *types.OperatorInput {
	return types.NewOperatorInput(common, nil)
}

func TestResolveTemplatedParams_String(t *testing.T) {
	plan := []TemplatedParam{
		{Name: "k", ScalarType: "string", Field: "id"},
	}
	got, err := ResolveTemplatedParams("op", plan,
		resolveInput(map[string]any{"id": "42"}))
	if err != nil {
		t.Fatal(err)
	}
	if got["k"] != "42" {
		t.Errorf("got %v", got)
	}
}

// Pins the cross-runtime stringify contract: a float64-valued source
// field bound to a string-typed templatable param must serialize via
// fmt.Sprint (5.0 → "5"), not via Java's String.valueOf (5.0 → "5.0")
// or Python's str (5.0 → "5.0"). Without this pin the Redis key would
// diverge across runtimes whenever the template source is float-typed.
func TestResolveTemplatedParams_FloatSourceStringTargetMatchesFmtSprint(t *testing.T) {
	plan := []TemplatedParam{
		{Name: "k", ScalarType: "string", Field: "x"},
	}
	got, err := ResolveTemplatedParams("op", plan,
		resolveInput(map[string]any{"x": 5.0}))
	if err != nil {
		t.Fatal(err)
	}
	if got["k"] != "5" {
		t.Errorf("got %q, want %q", got["k"], "5")
	}
}

func TestResolveTemplatedParams_Int(t *testing.T) {
	plan := []TemplatedParam{
		{Name: "k", ScalarType: "int64", Field: "n"},
	}
	got, err := ResolveTemplatedParams("op", plan,
		resolveInput(map[string]any{"n": int64(7)}))
	if err != nil {
		t.Fatal(err)
	}
	if got["k"] != int64(7) {
		t.Errorf("got %v (%T)", got["k"], got["k"])
	}
}

func TestResolveTemplatedParams_Bool(t *testing.T) {
	plan := []TemplatedParam{
		{Name: "k", ScalarType: "bool", Field: "b"},
	}
	got, err := ResolveTemplatedParams("op", plan,
		resolveInput(map[string]any{"b": true}))
	if err != nil {
		t.Fatal(err)
	}
	if got["k"] != true {
		t.Errorf("got %v", got)
	}
}

func TestResolveTemplatedParams_MissingField(t *testing.T) {
	plan := []TemplatedParam{
		{Name: "k", ScalarType: "string", Field: "absent"},
	}
	_, err := ResolveTemplatedParams("op", plan, resolveInput(map[string]any{}))
	want := `templated param "k" references common field "absent" which is missing`
	if err == nil || err.Error() != want {
		t.Errorf("got %v\nwant %s", err, want)
	}
}

func TestResolveTemplatedParams_CoerceFailure(t *testing.T) {
	plan := []TemplatedParam{
		{Name: "k", ScalarType: "int64", Field: "x"},
	}
	_, err := ResolveTemplatedParams("op", plan,
		resolveInput(map[string]any{"x": "not-a-number"}))
	want := `templated param "k" cannot coerce "not-a-number" to int64`
	if err == nil || err.Error() != want {
		t.Errorf("got %v\nwant %s", err, want)
	}
}

func TestResolveTemplatedParams_EmptyPlanShortCircuit(t *testing.T) {
	got, err := ResolveTemplatedParams("op", nil, resolveInput(nil))
	if err != nil || got != nil {
		t.Errorf("got %v, %v", got, err)
	}
}
