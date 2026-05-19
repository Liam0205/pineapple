package registry

import (
	"context"
	"fmt"
	"testing"

	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

// --- test helpers ---

type noopOp struct{}

func (n *noopOp) Init(params map[string]any) error { return nil }
func (n *noopOp) Execute(ctx context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	return nil
}

type captureOp struct {
	params map[string]any
}

func (c *captureOp) Init(params map[string]any) error {
	c.params = params
	return nil
}
func (c *captureOp) Execute(ctx context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	return nil
}

type failInitOp struct{}

func (f *failInitOp) Init(params map[string]any) error { return fmt.Errorf("init failed") }
func (f *failInitOp) Execute(ctx context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	return nil
}

// --- tests ---

func TestRegisterAndLookup(t *testing.T) {
	Reset()
	schema := types.OperatorSchema{Name: "noop", Type: types.OpTypeTransform, Description: "No-op."}
	Register(schema, func() types.Operator { return &noopOp{} })

	s, factory, ok := Lookup("noop")
	if !ok {
		t.Fatal("Lookup failed")
	}
	if s.Name != "noop" {
		t.Errorf("schema.Name = %q", s.Name)
	}
	op := factory()
	if op == nil {
		t.Fatal("factory returned nil")
	}
}

func TestLookupNotFound(t *testing.T) {
	Reset()
	_, _, ok := Lookup("nonexistent")
	if ok {
		t.Error("Lookup should fail for unregistered operator")
	}
}

func TestDuplicateRegistrationPanics(t *testing.T) {
	Reset()
	schema := types.OperatorSchema{Name: "dup", Type: types.OpTypeTransform, Description: "Dup test."}
	Register(schema, func() types.Operator { return &noopOp{} })

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	Register(schema, func() types.Operator { return &noopOp{} })
}

func TestValidateAndExtractParamsRequired(t *testing.T) {
	Reset()
	schema := types.OperatorSchema{
		Name:     "test",
		Type: types.OpTypeTransform,
		Description: "Test op.",
		Params: map[string]types.ParamSpec{
			"required_param": {Type: "string", Required: true, Description: "A required param."},
		},
	}

	_, err := ValidateAndExtractParams(schema, map[string]any{})
	if err == nil {
		t.Error("expected error for missing required param")
	}
}

func TestValidateAndExtractParamsDefault(t *testing.T) {
	Reset()
	schema := types.OperatorSchema{
		Name:     "test",
		Type: types.OpTypeTransform,
		Description: "Test op.",
		Params: map[string]types.ParamSpec{
			"optional": {Type: "float64", Required: false, Default: 0.5, Description: "Optional param."},
		},
	}

	params, err := ValidateAndExtractParams(schema, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if params["optional"] != 0.5 {
		t.Errorf("optional = %v, want 0.5", params["optional"])
	}
}

func TestValidateAndExtractParamsFiltersReserved(t *testing.T) {
	Reset()
	schema := types.OperatorSchema{
		Name: "test", Type: types.OpTypeTransform, Description: "Test op.",
		Params: map[string]types.ParamSpec{
			"business_key": {Type: "string", Required: false, Description: "A business param."},
		},
	}

	raw := map[string]any{
		"type_name":    "test",
		"$metadata":    map[string]any{},
		"$code_info":   "file:1",
		"skip":         "_if_1",
		"recall":       true,
		"sources":      []string{"a"},
		"debug":        true,
		"business_key": "value",
	}

	params, err := ValidateAndExtractParams(schema, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := params["type_name"]; ok {
		t.Error("reserved key type_name should be filtered")
	}
	if _, ok := params["$metadata"]; ok {
		t.Error("reserved key $metadata should be filtered")
	}
	if params["business_key"] != "value" {
		t.Errorf("business_key = %v", params["business_key"])
	}
}

func TestValidateAndExtractParamsRejectsUndeclared(t *testing.T) {
	Reset()
	schema := types.OperatorSchema{
		Name: "test", Type: types.OpTypeTransform, Description: "Test op.",
		Params: map[string]types.ParamSpec{
			"known": {Type: "string", Required: false, Description: "Known param."},
		},
	}

	raw := map[string]any{
		"type_name": "test",
		"known":     "ok",
		"typo_key":  "oops",
	}

	_, err := ValidateAndExtractParams(schema, raw)
	if err == nil {
		t.Fatal("expected error for undeclared parameter")
	}
}

func TestBuildOperatorSuccess(t *testing.T) {
	Reset()
	schema := types.OperatorSchema{
		Name:        "capture",
		Type:        types.OpTypeTransform,
		Description: "Capture op.",
		Params: map[string]types.ParamSpec{
			"threshold": {Type: "float64", Required: false, Default: 1.0, Description: "Threshold value."},
		},
	}
	Register(schema, func() types.Operator { return &captureOp{} })

	op, s, err := BuildOperator("capture", map[string]any{"type_name": "capture"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "capture" {
		t.Errorf("schema name = %q", s.Name)
	}
	c := op.(*captureOp)
	if c.params["threshold"] != 1.0 {
		t.Errorf("threshold = %v, want 1.0", c.params["threshold"])
	}
}

func TestBuildOperatorUnknownType(t *testing.T) {
	Reset()
	_, _, err := BuildOperator("unknown", nil)
	if err == nil {
		t.Error("expected error for unknown operator type")
	}
}

func TestBuildOperatorInitFails(t *testing.T) {
	Reset()
	schema := types.OperatorSchema{Name: "fail_init", Type: types.OpTypeTransform, Description: "Fails init."}
	Register(schema, func() types.Operator { return &failInitOp{} })

	_, _, err := BuildOperator("fail_init", map[string]any{})
	if err == nil {
		t.Error("expected error for failed Init")
	}
}

func TestIsReservedKey(t *testing.T) {
	reserved := []string{"type_name", "$metadata", "$code_info", "skip", "recall",
		"sources", "debug", "consumes_row_set", "mutates_row_set", "additive_writes_row_set",
		"common_defaults", "item_defaults", "for_branch_control"}
	for _, k := range reserved {
		if !IsReservedKey(k) {
			t.Errorf("%q should be reserved", k)
		}
	}
	if IsReservedKey("business_param") {
		t.Error("business_param should not be reserved")
	}
}

func TestRegisterPanicsOnMissingType(t *testing.T) {
	Reset()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for missing Type")
		}
	}()
	Register(types.OperatorSchema{Name: "bad", Description: "Has desc."}, func() types.Operator { return &noopOp{} })
}

func TestRegisterPanicsOnMissingDescription(t *testing.T) {
	Reset()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for missing Description")
		}
	}()
	Register(types.OperatorSchema{Name: "bad", Type: types.OpTypeTransform}, func() types.Operator { return &noopOp{} })
}

func TestRegisterPanicsOnMissingParamDescription(t *testing.T) {
	Reset()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for missing param Description")
		}
	}()
	Register(types.OperatorSchema{
		Name:        "bad",
		Type:        types.OpTypeTransform,
		Description: "Has desc.",
		Params: map[string]types.ParamSpec{
			"field": {Type: "string", Required: true},
		},
	}, func() types.Operator { return &noopOp{} })
}
