package dataframe

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

// Batch column write (SetItemColumnFloat64) semantics: adopt on column
// store, scatter on row store, length check, NaN validation parity,
// ordering vs per-element writes.

func TestSetItemColumnFloat64BothModes(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			items := []map[string]any{{"id": "a"}, {"id": "b"}, {"id": "c"}}
			f := NewFrame(tm.mode, nil, items)
			out := types.NewOperatorOutput()
			out.SetItemColumnFloat64("score", []float64{1.5, 2.5, 3.5})
			if err := f.ApplyOutput(out, "op", false); err != nil {
				t.Fatal(err)
			}
			for i, want := range []float64{1.5, 2.5, 3.5} {
				if got := f.Item(i, "score"); got != want {
					t.Errorf("Item(%d)=%v want %v", i, got, want)
				}
			}
			// Result projection includes the new field on every row.
			r := f.ToResult(nil, []string{"id", "score"})
			for i := range r.Items {
				if _, ok := r.Items[i]["score"]; !ok {
					t.Errorf("item %d missing score in result", i)
				}
			}
		})
	}
}

func TestSetItemColumnFloat64LengthMismatch(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := NewFrame(tm.mode, nil, []map[string]any{{"id": "a"}, {"id": "b"}})
			out := types.NewOperatorOutput()
			out.SetItemColumnFloat64("score", []float64{1.0}) // wrong length
			err := f.ApplyOutput(out, "op", false)
			if err == nil {
				t.Fatal("expected length-mismatch error")
			}
			if !strings.Contains(err.Error(), "does not match item count 2") {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestSetItemColumnFloat64NaNValidation(t *testing.T) {
	nan := func() float64 { var z float64; return 0 / z }()
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := NewFrame(tm.mode, nil, []map[string]any{{"id": "a"}, {"id": "b"}})
			out := types.NewOperatorOutput()
			out.SetItemColumnFloat64("score", []float64{1.0, nan})
			err := f.ApplyOutput(out, "op", false)
			if err == nil {
				t.Fatal("expected NaN validation error")
			}
			// Same first-error message shape as the per-element path.
			want := `item[1] write: field "score": NaN/Inf is not a valid JSON value`
			if err.Error() != want {
				t.Errorf("error = %q, want %q", err.Error(), want)
			}
		})
	}
}

func TestSetItemColumnFloat64OverridesPerElement(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := NewFrame(tm.mode, nil, []map[string]any{{"id": "a"}, {"id": "b"}})
			out := types.NewOperatorOutput()
			out.SetItem(0, "score", 99.0) // per-element first
			out.SetItemColumnFloat64("score", []float64{1.0, 2.0})
			if err := f.ApplyOutput(out, "op", false); err != nil {
				t.Fatal(err)
			}
			// Column write applies after per-element → wins on collision.
			if got := f.Item(0, "score"); got != 1.0 {
				t.Errorf("Item(0)=%v want 1.0 (column write wins)", got)
			}
		})
	}
}

func TestSetItemColumnFloat64AdoptZeroCopy(t *testing.T) {
	// Column store must adopt the slice (typed fast-path readable,
	// zero-copy) — pinned via the read view aliasing the written slice.
	items := []map[string]any{{"id": "a"}, {"id": "b"}}
	f := newColumnFrame(nil, items)
	vals := []float64{1.0, 2.0}
	out := types.NewOperatorOutput()
	out.SetItemColumnFloat64("score", vals)
	if err := f.ApplyOutput(out, "op", false); err != nil {
		t.Fatal(err)
	}
	view, ok := f.ItemColumnFloat64View("score", 0, 2)
	if !ok {
		t.Fatal("adopted column must serve the typed read view")
	}
	if &view[0] != &vals[0] {
		t.Error("expected zero-copy adoption (view aliases the written slice)")
	}
}

func TestSetItemColumnFloat64RowColumnParity(t *testing.T) {
	mk := func(mode StorageMode) Frame {
		return NewFrame(mode, nil, []map[string]any{
			{"id": "a", "old": 1.0}, {"id": "b", "old": 2.0},
		})
	}
	apply := func(f Frame) {
		out := types.NewOperatorOutput()
		out.SetItemColumnFloat64("norm", []float64{0.25, 0.75})
		if err := f.ApplyOutput(out, "op", false); err != nil {
			t.Fatal(err)
		}
	}
	row, col := mk(StorageModeRow), mk(StorageModeColumn)
	apply(row)
	apply(col)
	fields := []string{"id", "old", "norm"}
	r1, r2 := row.ToResult(nil, fields), col.ToResult(nil, fields)
	for i := range r1.Items {
		if fmt.Sprint(r1.Items[i]) != fmt.Sprint(r2.Items[i]) {
			t.Errorf("item %d mismatch: %v vs %v", i, r1.Items[i], r2.Items[i])
		}
	}
}
