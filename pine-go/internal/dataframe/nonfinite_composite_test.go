package dataframe

import (
	"encoding/json"
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
// the depth-aware seen set skips a composite already scanned at the same or
// a shallower depth, while the NaN in a sibling is still found.
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

// Two references that share an address and length but cover different data
// (a pointer to an array and to its first element; a slice of arrays and a
// one-element slice of its first array) must not be treated as the same
// composite: the wider one still holds the Inf. In a map the two are visited
// in random order, so repeat to catch an order-dependent verdict.
func TestValidateValueSameAddressDifferentTypeNotConflated(t *testing.T) {
	arr := [2]float64{1, math.Inf(1)}
	s := [][2]float64{{1, math.Inf(1)}}
	const want = `field "f": NaN/Inf is not a valid JSON value`
	assertErr(t, validateValue("f", []any{&arr[0], &arr}), want)
	assertErr(t, validateValue("f", []any{s[0][:1], s}), want)
	for i := 0; i < 200; i++ {
		assertErr(t, validateValue("f", map[string]any{"a": &arr[0], "b": &arr}), want)
		assertErr(t, validateValue("f", map[string]any{"a": s[0][:1], "b": s}), want)
	}
}

type nfEmbedded struct{ E float64 }
type nfEmbeddedHidden struct{ e float64 }
type nfInner struct{ V float64 }

type nfStruct struct {
	nfEmbedded
	X      float64
	S      float64 `json:",string"`
	D      float64 `json:"-"`
	M      float64 `json:"-,"` //nolint:staticcheck // SA5008: the field named "-" is the case under test
	Ptr    *nfInner
	Named  namedFloat
	hidden float64
}

type nfStructHiddenEmbed struct {
	nfEmbeddedHidden
}

// nfHiddenFloat is an unexported non-struct type; embedded, encoding/json
// ignores it (it has no fields to promote).
type nfHiddenFloat float64

type nfStructHiddenFloatEmbed struct {
	nfHiddenFloat
	Y float64
}

// nfEmbeddedPtr embeds a pointer to an unexported struct; its promoted
// exported field is encoded.
type nfEmbeddedPtr struct {
	*nfEmbedded
}

// nfMarshalScore encodes a non-finite value as a string, so json.Marshal
// accepts it.
type nfMarshalScore float64

func (v nfMarshalScore) MarshalJSON() ([]byte, error) {
	f := float64(v)
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return []byte(`"non-finite"`), nil
	}
	return json.Marshal(f)
}

type nfTextScore float64

func (v nfTextScore) MarshalText() ([]byte, error) { return []byte("score"), nil }

type nfMarshalStruct struct{ V float64 }

func (p *nfMarshalStruct) MarshalJSON() ([]byte, error) { return []byte(`"custom"`), nil }

// Custom Go operators can nest structs in a composite; encoding/json encodes
// their exported fields (including ones promoted from an embedded struct and
// fields tagged "-,"), so a non-finite value there must be rejected at write
// time. What encoding/json skips or delegates — unexported fields, fields
// tagged exactly "-", an embedded unexported non-struct, and any type with its
// own MarshalJSON / MarshalText — must not trigger a rejection, or the write
// check would refuse values the encoder accepts. (Shadowed promoted fields
// are the documented exception; see scanStruct.)
func TestValidateValueStructFields(t *testing.T) {
	inf := math.Inf(1)
	const want = `field "f": NaN/Inf is not a valid JSON value`
	rejected := map[string]nfStruct{
		"exported":        {X: inf},
		"string_option":   {S: inf},
		"dash_comma_name": {M: inf},
		"embedded":        {nfEmbedded: nfEmbedded{E: inf}},
		"pointer_field":   {Ptr: &nfInner{V: inf}},
		"named_float":     {Named: namedFloat(inf)},
	}
	for name, v := range rejected {
		t.Run(name, func(t *testing.T) {
			assertErr(t, validateValue("f", []any{v}), want)
			assertErr(t, validateValue("f", []any{&v}), want)
		})
	}
	t.Run("embedded_pointer", func(t *testing.T) {
		assertErr(t, validateValue("f", []any{nfEmbeddedPtr{&nfEmbedded{E: inf}}}), want)
	})
	for name, v := range map[string]any{
		"dash":                  nfStruct{D: inf},
		"unexported":            nfStruct{hidden: inf},
		"embedded_hidden":       nfStructHiddenEmbed{nfEmbeddedHidden{e: inf}},
		"embedded_hidden_float": nfStructHiddenFloatEmbed{nfHiddenFloat: nfHiddenFloat(inf)},
		"embedded_nil_pointer":  nfEmbeddedPtr{},
		"json_marshaler":        nfMarshalScore(inf),
		"text_marshaler":        map[string]nfTextScore{"a": nfTextScore(inf)},
		"marshaler_slice":       []nfMarshalScore{1, nfMarshalScore(inf)},
		"pointer_marshaler":     &nfMarshalStruct{V: inf},
	} {
		t.Run("accepted/"+name, func(t *testing.T) {
			if err := validateValue("f", []any{v}); err != nil {
				t.Fatalf("value encoding/json accepts was rejected: %v", err)
			}
			if _, err := json.Marshal([]any{v}); err != nil {
				t.Fatalf("precondition: json.Marshal should accept this value: %v", err)
			}
		})
	}
}
