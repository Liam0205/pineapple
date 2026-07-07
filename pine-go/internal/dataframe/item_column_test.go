package dataframe

import (
	"fmt"
	"testing"

	"github.com/Liam0205/pineapple/pine-go/internal/config"
	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

// itemColumnModes runs a subtest against both storage modes.
func itemColumnModes(t *testing.T, fn func(t *testing.T, mode StorageMode)) {
	t.Helper()
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) { fn(t, tm.mode) })
	}
}

func TestItemColumnMatchesItem(t *testing.T) {
	itemColumnModes(t, func(t *testing.T, mode StorageMode) {
		items := []map[string]any{
			{"a": float64(1), "b": "x"},
			{"a": float64(2)}, // b missing
			{"a": nil, "b": "z"},
		}
		f := NewFrame(mode, map[string]any{"u": "c"}, items)
		in, err := f.BuildInput("op", &config.InputFieldSpec{
			NullableItem: []string{"a"},
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"a", "b", "absent"} {
			col := in.ItemColumn(field)
			if len(col) != in.ItemCount() {
				t.Fatalf("field %q: len=%d want %d", field, len(col), in.ItemCount())
			}
			for i := range col {
				want := in.Item(i, field)
				if fmt.Sprint(col[i]) != fmt.Sprint(want) {
					t.Errorf("field %q item %d: got %v want %v", field, i, col[i], want)
				}
			}
		}
	})
}

func TestItemColumnAppliesDefaults(t *testing.T) {
	itemColumnModes(t, func(t *testing.T, mode StorageMode) {
		items := []map[string]any{
			{"score": float64(1)},
			{"score": nil},
			{}, // missing entirely
		}
		f := NewFrame(mode, nil, items)
		in, err := f.BuildInput("op", &config.InputFieldSpec{
			DefaultedItem: []config.DefaultedField{{Name: "score", Default: float64(-1)}},
		})
		if err != nil {
			t.Fatal(err)
		}
		col := in.ItemColumn("score")
		want := []any{float64(1), float64(-1), float64(-1)}
		for i := range want {
			if col[i] != want[i] {
				t.Errorf("item %d: got %v want %v", i, col[i], want[i])
			}
			if got := in.Item(i, "score"); got != want[i] {
				t.Errorf("Item(%d): got %v want %v", i, got, want[i])
			}
		}
	})
}

func TestItemColumnMaterializedMode(t *testing.T) {
	items := []map[string]any{
		{"a": float64(1)},
		{"a": float64(2)},
	}
	in := types.NewOperatorInput(nil, items)
	col := in.ItemColumn("a")
	if len(col) != 2 || col[0] != float64(1) || col[1] != float64(2) {
		t.Fatalf("materialized ItemColumn = %v", col)
	}
}

func TestItemColumnViewWindow(t *testing.T) {
	items := make([]map[string]any, 10)
	for i := range items {
		items[i] = map[string]any{"v": float64(i)}
	}
	itemColumnModes(t, func(t *testing.T, mode StorageMode) {
		f := NewFrame(mode, nil, items)
		cr, ok := f.(types.ColumnReader)
		if !ok {
			t.Fatalf("%T does not implement ColumnReader", f)
		}
		col, ok := cr.ItemColumnView("v", 3, 4)
		if !ok {
			t.Fatal("ItemColumnView not ok")
		}
		if len(col) != 4 {
			t.Fatalf("len=%d want 4", len(col))
		}
		for i := 0; i < 4; i++ {
			if col[i] != float64(3+i) {
				t.Errorf("slot %d: got %v want %v", i, col[i], float64(3+i))
			}
		}
		// Out-of-range windows must be rejected.
		if _, ok := cr.ItemColumnView("v", 8, 4); ok {
			t.Error("out-of-range window unexpectedly ok")
		}
		if _, ok := cr.ItemColumnView("v", -1, 2); ok {
			t.Error("negative offset unexpectedly ok")
		}
	})
}

func TestItemColumnAbsentField(t *testing.T) {
	itemColumnModes(t, func(t *testing.T, mode StorageMode) {
		f := NewFrame(mode, nil, []map[string]any{{"a": 1.0}, {"a": 2.0}})
		in, err := f.BuildInput("op", &config.InputFieldSpec{})
		if err != nil {
			t.Fatal(err)
		}
		col := in.ItemColumn("nope")
		if len(col) != 2 {
			t.Fatalf("len=%d want 2", len(col))
		}
		for i, v := range col {
			if v != nil {
				t.Errorf("slot %d: got %v want nil", i, v)
			}
		}
	})
}
