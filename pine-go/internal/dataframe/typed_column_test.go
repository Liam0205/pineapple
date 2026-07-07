package dataframe

import (
	"fmt"
	"testing"

	"github.com/Liam0205/pineapple/pine-go/internal/config"
	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

// Typed-column behavior tests: construction-time inference, promotion on
// mixed writes / present-nil, and the ItemColumnFloat64 fast path.

func TestTypedColumnInference(t *testing.T) {
	items := []map[string]any{
		{"f": 1.5, "s": "a", "b": true, "mixed": 1.0, "withnil": 1.0, "compo": []any{1.0}},
		{"f": 2.5, "s": "b", "b": false, "mixed": "x", "withnil": nil, "compo": []any{2.0}},
	}
	f := newColumnFrame(nil, items)

	wantKinds := map[string]columnKind{
		"f":       kindFloat64,
		"s":       kindString,
		"b":       kindBool,
		"mixed":   kindJSON, // float64 then string → fallback
		"withnil": kindJSON, // present-nil disqualifies typed storage
		"compo":   kindJSON, // composite values
	}
	for field, want := range wantKinds {
		if got := f.columns[field].kind(); got != want {
			t.Errorf("field %q: kind=%v want %v", field, got, want)
		}
	}
}

func TestTypedColumnItemSemantics(t *testing.T) {
	items := []map[string]any{
		{"f": 1.5},
		{},         // absent
		{"f": nil}, // present-nil → whole column degrades to json
		{"f": 2.5},
	}
	f := newColumnFrame(nil, items)
	if got := f.Item(0, "f"); got != 1.5 {
		t.Errorf("Item(0)=%v want 1.5", got)
	}
	if got := f.Item(1, "f"); got != nil {
		t.Errorf("Item(1)=%v want nil (absent)", got)
	}
	if got := f.Item(2, "f"); got != nil {
		t.Errorf("Item(2)=%v want nil (present-nil)", got)
	}
	// Presence must still distinguish absent from present-nil.
	col := f.columns["f"]
	if col.present(1) {
		t.Error("present(1) should be false (absent)")
	}
	if !col.present(2) {
		t.Error("present(2) should be true (explicit nil)")
	}
}

func TestTypedColumnPromotionOnWrite(t *testing.T) {
	items := []map[string]any{{"f": 1.0}, {"f": 2.0}}
	f := newColumnFrame(nil, items)
	if f.columns["f"].kind() != kindFloat64 {
		t.Fatal("precondition: f should be typed float64")
	}

	// Type-mismatched write promotes to json and preserves other slots.
	out := types.NewOperatorOutput()
	out.SetItem(0, "f", "now-a-string")
	if err := f.ApplyOutput(out, "op", false); err != nil {
		t.Fatal(err)
	}
	if f.columns["f"].kind() != kindJSON {
		t.Error("expected promotion to jsonColumn after mixed-type write")
	}
	if got := f.Item(0, "f"); got != "now-a-string" {
		t.Errorf("Item(0)=%v", got)
	}
	if got := f.Item(1, "f"); got != 2.0 {
		t.Errorf("Item(1)=%v want 2.0 (preserved through promotion)", got)
	}

	// Present-nil write also requires promotion.
	items2 := []map[string]any{{"g": 1.0}}
	f2 := newColumnFrame(nil, items2)
	out2 := types.NewOperatorOutput()
	out2.SetItem(0, "g", nil)
	if err := f2.ApplyOutput(out2, "op", false); err != nil {
		t.Fatal(err)
	}
	if f2.columns["g"].kind() != kindJSON {
		t.Error("expected promotion to jsonColumn after nil write")
	}
	if got := f2.Item(0, "g"); got != nil {
		t.Errorf("Item(0)=%v want nil", got)
	}
	if !f2.columns["g"].present(0) {
		t.Error("slot should be present-nil after explicit nil write")
	}
}

func TestItemColumnFloat64FastPath(t *testing.T) {
	items := []map[string]any{{"f": 1.0, "s": "x"}, {"f": 2.0, "s": "y"}, {"f": 3.0, "s": "z"}}
	f := newColumnFrame(nil, items)
	in, err := f.BuildInput("op", &config.InputFieldSpec{NullableItem: []string{"f"}})
	if err != nil {
		t.Fatal(err)
	}

	raw, ok := in.ItemColumnFloat64("f")
	if !ok {
		t.Fatal("expected typed fast path for fully-present float64 column")
	}
	want := []float64{1.0, 2.0, 3.0}
	for i := range want {
		if raw[i] != want[i] {
			t.Errorf("raw[%d]=%v want %v", i, raw[i], want[i])
		}
	}

	// Non-float64 column → no fast path.
	if _, ok := in.ItemColumnFloat64("s"); ok {
		t.Error("string column must not serve the float64 fast path")
	}
	// Absent column → no fast path.
	if _, ok := in.ItemColumnFloat64("absent"); ok {
		t.Error("absent column must not serve the float64 fast path")
	}

	// Column with a hole → no fast path (defaults could apply).
	items2 := []map[string]any{{"f": 1.0}, {}}
	f2 := newColumnFrame(nil, items2)
	in2, err := f2.BuildInput("op", &config.InputFieldSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := in2.ItemColumnFloat64("f"); ok {
		t.Error("column with absent slot must not serve the float64 fast path")
	}

	// Row frame → no fast path (no typed storage).
	fr := newRowFrame(nil, items)
	inR, err := fr.BuildInput("op", &config.InputFieldSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := inR.ItemColumnFloat64("f"); ok {
		t.Error("row frame must not serve the float64 fast path")
	}
}

// TestTypedColumnResultParity pins that a pipeline of writes / removals /
// reorder / additions produces identical ToResult output on both storage
// modes (the dual-impl equivalence that the typed rewrite must preserve).
func TestTypedColumnResultParity(t *testing.T) {
	mk := func(mode StorageMode) Frame {
		items := []map[string]any{
			{"id": "a", "score": 3.0},
			{"id": "b", "score": 1.0},
			{"id": "c", "score": 2.0},
		}
		return NewFrame(mode, map[string]any{"u": "x"}, items)
	}
	apply := func(f Frame) {
		out := types.NewOperatorOutput()
		out.SetItem(0, "rank", 1.0)
		out.SetItem(1, "rank", 2.0)
		out.SetItem(2, "rank", 3.0)
		if err := f.ApplyOutput(out, "op1", false); err != nil {
			t.Fatal(err)
		}
		out2 := types.NewOperatorOutput()
		out2.RemoveItem(1)
		if err := f.ApplyOutput(out2, "op2", false); err != nil {
			t.Fatal(err)
		}
		out3 := types.NewOperatorOutput()
		out3.SetItemOrder([]int{1, 0})
		if err := f.ApplyOutput(out3, "op3", false); err != nil {
			t.Fatal(err)
		}
		out4 := types.NewOperatorOutput()
		out4.AddItem(map[string]any{"id": "d", "score": 9.0, "extra": true})
		if err := f.ApplyOutput(out4, "op4", true); err != nil {
			t.Fatal(err)
		}
	}

	row := mk(StorageModeRow)
	col := mk(StorageModeColumn)
	apply(row)
	apply(col)

	fields := []string{"id", "score", "rank", "extra", "_source"}
	r1 := row.ToResult([]string{"u"}, fields)
	r2 := col.ToResult([]string{"u"}, fields)
	if fmt.Sprint(r1.Common) != fmt.Sprint(r2.Common) {
		t.Errorf("common mismatch: %v vs %v", r1.Common, r2.Common)
	}
	if len(r1.Items) != len(r2.Items) {
		t.Fatalf("item count mismatch: %d vs %d", len(r1.Items), len(r2.Items))
	}
	for i := range r1.Items {
		if fmt.Sprint(r1.Items[i]) != fmt.Sprint(r2.Items[i]) {
			t.Errorf("item %d mismatch:\n row: %v\n col: %v", i, r1.Items[i], r2.Items[i])
		}
	}
}
