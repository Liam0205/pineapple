package dataframe

import "testing"

// TestNewFrameDispatchIsExactMatch pins the reference behaviour that pine-java
// and pine-cpp were aligned to in issue #179.
//
// NewFrame switches on a string-typed StorageMode with `default: newRowFrame`,
// so exactly one value — the literal "column" — reaches the column store, and
// every other value including misspellings and different casings reaches the
// row store. That rule is only implicit in the switch, and it had no test, so
// nothing here would have noticed if the default branch changed direction.
//
// It matters because the other two runtimes disagreed with it: pine-java matched
// with equalsIgnoreCase so "Column" selected the column store, and pine-cpp's
// make_frame fell back to column so "colunm" did too. Row and column stores are
// output-equivalent by design, so the divergence never changed a response byte —
// only the memory and performance profile — which is why no end-to-end channel
// could see it and why this belongs in a unit test.
func TestNewFrameDispatchIsExactMatch(t *testing.T) {
	common := map[string]any{"r": "v"}
	items := []map[string]any{{"id": "a"}}

	t.Run("exact column selects the column store", func(t *testing.T) {
		f := NewFrame(StorageModeColumn, common, items)
		if _, ok := f.(*RowFrame); ok {
			t.Fatalf("storage_mode %q must not select the row store", StorageModeColumn)
		}
	})

	// Every other value, including the two that diverged in #179.
	rowModes := []StorageMode{
		StorageModeRow,
		"",
		"colunm",
		"Column",
		"COLUMN",
		"cOlUmN",
		"column ",
		" column",
		"columns",
		"col",
		"rows",
		"unknown",
	}
	for _, mode := range rowModes {
		t.Run("row store for "+string(mode), func(t *testing.T) {
			f := NewFrame(mode, common, items)
			if _, ok := f.(*RowFrame); !ok {
				t.Fatalf("storage_mode %q must select the row store, got %T", mode, f)
			}
			if got := f.ItemCount(); got != 1 {
				t.Fatalf("item count = %d, want 1", got)
			}
		})
	}
}
