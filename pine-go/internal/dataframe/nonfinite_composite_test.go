package dataframe

import (
	"math"
	"testing"
	"time"

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
// hang or overflow the stack. Two self-keys make a naive scan exponential;
// the ancestor check skips a composite that contains itself, while the
// NaN in a sibling is still found.
func TestValidateValueSelfReferencingMapTerminates(t *testing.T) {
	m := map[string]any{}
	m["a"] = m
	m["b"] = m
	if err := validateValue("f", m); err != nil {
		t.Fatalf("self-referencing map: unexpected error %v", err)
	}
	m["bad"] = math.NaN()
	if err := validateValue("f", m); err == nil {
		t.Fatal("self-referencing map with a NaN sibling: expected rejection")
	}

	// A longer view of the same backing array is a different value (the
	// encoder serialises it), so its extra element must still be scanned.
	parent := make([]any, 1, 2)
	full := parent[:2]
	parent[0] = full
	full[1] = math.Inf(1)
	if err := validateValue("f", parent); err == nil {
		t.Fatal("longer view of the same backing array: expected rejection")
	}
}

func nestArrays(leaf any, levels int) any {
	v := leaf
	for i := 0; i < levels; i++ {
		v = []any{v}
	}
	return v
}

// The depth bound skips composites at depth >= maxCompositeScanDepth but
// keeps scanning their siblings, so the verdict depends only on the value,
// never on Go's random map iteration order. Run many times: an
// order-dependent scan fails this within a few iterations.
func TestValidateValueDepthBoundIsOrderIndependent(t *testing.T) {
	deep := nestArrays(0.0, maxCompositeScanDepth+1)
	for i := 0; i < 200; i++ {
		v := map[string]any{"deep": deep, "bad": math.Inf(1), "z": 1.0}
		if err := validateValue("f", v); err == nil {
			t.Fatalf("iteration %d: NaN/Inf next to a too-deep subtree was not rejected", i)
		}
	}
}

// Boundary: a non-finite scalar whose parent composite is at depth
// maxCompositeScanDepth-1 is found; one level deeper it is not inspected.
// pine-java and pine-cpp pin the same two cases.
func TestValidateValueDepthBoundary(t *testing.T) {
	if err := validateValue("f", nestArrays(math.NaN(), maxCompositeScanDepth)); err == nil {
		t.Error("NaN inside the deepest scanned composite: expected rejection")
	}
	if err := validateValue("f", nestArrays(math.NaN(), maxCompositeScanDepth+1)); err != nil {
		t.Errorf("NaN below the depth bound: expected it to be left to the encoder, got %v", err)
	}
}

type namedFloat float64

// Custom Go operators can hand over slice/map/element types beyond
// []any / map[string]any; json.Marshal rejects a non-finite value in any of
// them, so the write check must too.
func TestValidateValueReflectElementTypes(t *testing.T) {
	inf := math.Inf(1)
	cases := map[string]any{
		"named_float_slice":  []namedFloat{1, namedFloat(inf)},
		"named_float_map":    map[string]namedFloat{"a": namedFloat(inf)},
		"named_float_in_any": []any{namedFloat(inf)},
		"pointer_elem":       []*float64{&inf},
		"nested_array":       []any{[2]float64{1, inf}},
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			assertErr(t, validateValue("f", v), `field "f": NaN/Inf is not a valid JSON value`)
		})
	}
	finite := 1.0
	if err := validateValue("f", []*float64{&finite, nil}); err != nil {
		t.Errorf("finite pointers: unexpected error %v", err)
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

// A composite reachable along many paths — shared sub-values, or a cycle
// through them — must be scanned in time linear in its distinct composites.
// A per-path walk over this ring of k maps, each pointing twice at the next,
// visits 2^k paths (k=40 never finishes); the depth-aware seen set visits
// each map once. Only a custom operator can build such a value.
func TestValidateValueSharedCyclicGraphIsLinear(t *testing.T) {
	const k = 40
	ms := make([]map[string]any, k)
	for i := range ms {
		ms[i] = map[string]any{}
	}
	for i := range ms {
		next := ms[(i+1)%k]
		ms[i]["a"] = next
		ms[i]["b"] = next
	}
	done := make(chan error, 1)
	go func() { done <- validateValue("f", ms[0]) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("finite cyclic graph: unexpected error %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("scan of a shared cyclic graph did not finish: path enumeration is exponential")
	}

	ms[k-1]["bad"] = math.NaN()
	assertErr(t, validateValue("f", ms[0]), `field "f": NaN/Inf is not a valid JSON value`)
}

// A shared sub-value first reached deep (near the bound) and later shallow
// must be rescanned from the shallower depth: the NaN below it is only
// within the bound on the shallow path.
func TestValidateValueSharedSubvalueRescannedFromShallowerDepth(t *testing.T) {
	shared := []any{nestArrays(math.NaN(), 3)}
	deepPath := nestArrays(shared, maxCompositeScanDepth-2)
	v := []any{deepPath, shared}
	assertErr(t, validateValue("f", v), `field "f": NaN/Inf is not a valid JSON value`)
}

// A pointer that refers to itself (through an interface) must terminate.
func TestValidateValueSelfReferencingPointerTerminates(t *testing.T) {
	var x any
	x = &x
	done := make(chan error, 1)
	go func() { done <- validateValue("f", []any{&x}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("self-referencing pointer: unexpected error %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("scan of a self-referencing pointer did not finish")
	}
}
