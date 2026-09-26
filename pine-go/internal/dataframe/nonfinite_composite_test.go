package dataframe

import (
	"math"
	"testing"

	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

// Issue #210: NaN/±Inf nested inside a composite value used to pass the
// write-time check (only scalars were inspected) and only failed later at
// response encoding — Go refused to encode, Java wrote "Infinity", C++ wrote
// bare inf. Composites are now scanned with the same message as scalars.

func TestApplyOutputRejectsNonFiniteInComposite(t *testing.T) {
	inf := math.Inf(1)
	nan := math.NaN()
	cases := []struct {
		name  string
		value any
	}{
		{"array", []any{2.0, inf}},
		{"map", map[string]any{"a": 1.0, "b": math.Inf(-1)}},
		{"nested", []any{map[string]any{"x": []any{nan}}}},
		{"typed_slice", []float64{1, inf}},
	}
	for _, tm := range testModes {
		for _, c := range cases {
			t.Run(tm.name+"/"+c.name, func(t *testing.T) {
				f := NewFrame(tm.mode, nil, []map[string]any{{"id": "a"}, {"id": "b"}})

				out := types.NewOperatorOutput()
				out.SetItem(1, "r", c.value)
				assertErr(t, f.ApplyOutput(out, "op", false),
					`item[1] write: field "r": NaN/Inf is not a valid JSON value`)

				out = types.NewOperatorOutput()
				out.SetCommon("c", c.value)
				assertErr(t, f.ApplyOutput(out, "op", false),
					`common write: field "c": NaN/Inf is not a valid JSON value`)

				out = types.NewOperatorOutput()
				out.AddItem(map[string]any{"id": "c", "r": c.value})
				assertErr(t, f.ApplyOutput(out, "op", true),
					`added item write: field "r": NaN/Inf is not a valid JSON value`)
			})
		}
	}
}

func TestApplyOutputAcceptsFiniteComposite(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := NewFrame(tm.mode, nil, []map[string]any{{"id": "a"}})
			out := types.NewOperatorOutput()
			out.SetItem(0, "r", []any{1.0, "x", nil, true, map[string]any{"k": math.MaxFloat64}})
			if err := f.ApplyOutput(out, "op", false); err != nil {
				t.Fatalf("finite composite rejected: %v", err)
			}
		})
	}
}

// A self-referencing map (only a custom operator can build one) must not
// hang or overflow the stack: the scan gives up at maxCompositeScanDepth and
// leaves the value to the encoder. Two self-keys make an unbounded scan
// exponential, so this also pins that hitting the bound aborts the whole
// scan rather than one branch.
func TestValidateValueSelfReferencingMapTerminates(t *testing.T) {
	m := map[string]any{}
	m["a"] = m
	m["b"] = m
	if err := validateValue("f", m); err != nil {
		t.Fatalf("self-referencing map: unexpected error %v", err)
	}
}

func assertErr(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %q, got nil", want)
	}
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}
