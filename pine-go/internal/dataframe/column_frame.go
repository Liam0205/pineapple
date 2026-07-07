package dataframe

import (
	"fmt"
	"sync"

	"github.com/Liam0205/pineapple/pine-go/internal/config"
	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

// ColumnFrame is a request-local column-store DataFrame backed by typed
// columns (see column.go — float64/string/bool flat arrays + validity
// bitmap, jsonColumn as the heterogeneous fallback), mirroring pine-cpp's
// Column hierarchy.
// It is concurrency-safe via a single RWMutex: read operations take RLock,
// write operations take Lock.
type ColumnFrame struct {
	mu       sync.RWMutex
	common   map[string]any
	columns  map[string]column
	rowCount int
}

func newColumnFrame(common map[string]any, items []map[string]any) *ColumnFrame {
	c := make(map[string]any, len(common))
	for k, v := range common {
		c[k] = v
	}

	// Collect the field union, then build each column with
	// construction-time type inference (one pass per field).
	fieldSet := make(map[string]struct{})
	for _, item := range items {
		for k := range item {
			fieldSet[k] = struct{}{}
		}
	}
	cols := make(map[string]column, len(fieldSet))
	for field := range fieldSet {
		cols[field] = makeColumn(items, field)
	}

	return &ColumnFrame{common: c, columns: cols, rowCount: len(items)}
}

func (f *ColumnFrame) Common(field string) any {
	f.mu.RLock()
	v := f.common[field]
	f.mu.RUnlock()
	return v
}

func (f *ColumnFrame) SetCommon(field string, value any) {
	f.mu.Lock()
	f.common[field] = value
	f.mu.Unlock()
}

func (f *ColumnFrame) ItemCount() int {
	f.mu.RLock()
	n := f.rowCount
	f.mu.RUnlock()
	return n
}

func (f *ColumnFrame) Item(index int, field string) any {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if index < 0 || index >= f.rowCount {
		return nil
	}
	col, ok := f.columns[field]
	if !ok {
		return nil
	}
	return col.get(index)
}

// ItemColumnView implements types.ColumnReader: it returns a boxed
// window of the field's column under a single lock acquisition.
// jsonColumn windows are zero-copy views of the backing array; typed
// columns box a fresh copy (their raw arrays are not []any). Escaping
// the lock is safe because the DAG scheduler hazard-orders writers of
// this field and row-set mutating operators (including the in-place
// reorder in ApplyOutput) relative to the reader — see types.ColumnReader.
func (f *ColumnFrame) ItemColumnView(field string, offset, count int) ([]any, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if offset < 0 || count < 0 || offset+count > f.rowCount {
		return nil, false
	}
	col, ok := f.columns[field]
	if !ok {
		// Absent column: every slot reads as nil, same as Item(). Serve a
		// nil-filled slice so callers keep the batch path.
		return make([]any, count), true
	}
	return col.view(offset, count), true
}

// ItemColumnFloat64View implements types.Float64ColumnReader: zero-copy
// access to a float64 column's raw array. ok requires the whole window
// to be present (no nil slots) so element i is exactly the float64 that
// Item(offset+i, field) would box. Same lock-escape contract as
// ItemColumnView.
func (f *ColumnFrame) ItemColumnFloat64View(field string, offset, count int) ([]float64, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if offset < 0 || count < 0 || offset+count > f.rowCount {
		return nil, false
	}
	col, ok := f.columns[field]
	if !ok {
		return nil, false
	}
	return float64Window(col, offset, count)
}

func (f *ColumnFrame) BuildInput(
	opName string,
	spec *config.InputFieldSpec,
) (*types.OperatorInput, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	totalCommon := len(spec.StrictCommon) + len(spec.DefaultedCommon) + len(spec.NullableCommon)
	cs := make(map[string]any, totalCommon)

	// Strict common fields: must exist and be non-nil
	for _, field := range spec.StrictCommon {
		v, exists := f.common[field]
		if !exists || v == nil {
			return nil, fmt.Errorf("required field %q is nil in common", field)
		}
		cs[field] = v
	}
	// Defaulted common fields: substitute default on nil/missing
	for _, df := range spec.DefaultedCommon {
		v, exists := f.common[df.Name]
		if !exists || v == nil {
			cs[df.Name] = df.Default
		} else {
			cs[df.Name] = v
		}
	}
	// Nullable common fields: missing → error, nil → pass through
	for _, field := range spec.NullableCommon {
		v, exists := f.common[field]
		if !exists {
			return nil, fmt.Errorf("required field %q is missing in common", field)
		}
		cs[field] = v
	}

	// Validate strict item fields upfront (fail fast).
	// Column resolution is hoisted out of the per-item loop (previously a
	// map lookup per item × field). Iteration stays item-major so the
	// first-error priority is byte-identical across storage modes and
	// runtimes (see llmdoc/memory/reflections/review-driven-build-input-error-ordering.md).
	strictCols := make([]column, len(spec.StrictItem))
	for k, field := range spec.StrictItem {
		strictCols[k] = f.columns[field] // nil if absent
	}
	nullableCols := make([]column, len(spec.NullableItem))
	for k, field := range spec.NullableItem {
		nullableCols[k] = f.columns[field] // nil if absent
	}
	for i := 0; i < f.rowCount; i++ {
		for k, field := range spec.StrictItem {
			if strictCols[k] == nil || strictCols[k].isNull(i) {
				return nil, fmt.Errorf("required field %q is nil on item[%d]", field, i)
			}
		}
		for k, field := range spec.NullableItem {
			if nullableCols[k] == nil || !nullableCols[k].present(i) {
				return nil, fmt.Errorf("required field %q is missing on item[%d]", field, i)
			}
		}
	}

	// Build item defaults map and field list for lazy access
	var itemDefaults map[string]any
	if len(spec.DefaultedItem) > 0 {
		itemDefaults = make(map[string]any, len(spec.DefaultedItem))
		for _, df := range spec.DefaultedItem {
			itemDefaults[df.Name] = df.Default
		}
	}

	totalItem := len(spec.StrictItem) + len(spec.DefaultedItem) + len(spec.NullableItem)
	itemFields := make([]string, 0, totalItem)
	itemFields = append(itemFields, spec.StrictItem...)
	for _, df := range spec.DefaultedItem {
		itemFields = append(itemFields, df.Name)
	}
	itemFields = append(itemFields, spec.NullableItem...)

	return types.NewLazyOperatorInput(cs, f, itemDefaults, itemFields, 0, f.rowCount), nil
}

func (f *ColumnFrame) ApplyOutput(out *types.OperatorOutput, opName string, recall bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	// 1. Common writes
	for field, value := range out.GetCommonWrites() {
		if err := validateValue(field, value); err != nil {
			return fmt.Errorf("common write: %w", err)
		}
		f.common[field] = value
	}

	// 2. Item field writes
	// Cache the last-resolved column: operators typically write
	// field-major (the same field for consecutive indices), so this turns
	// the per-write map lookup into one per field run.
	var lastField string
	var lastCol column
	for _, w := range out.GetItemWrites() {
		if w.Index < 0 || w.Index >= f.rowCount {
			return fmt.Errorf("SetItem index %d out of range [0, %d)", w.Index, f.rowCount)
		}
		if err := validateValue(w.Field, w.Value); err != nil {
			return fmt.Errorf("item[%d] write: %w", w.Index, err)
		}
		if w.Field != lastField || lastCol == nil {
			col, ok := f.columns[w.Field]
			if !ok {
				col = newColumnForValue(w.Value, f.rowCount)
				f.columns[w.Field] = col
			}
			lastField = w.Field
			lastCol = col
		}
		if !lastCol.set(w.Index, w.Value) {
			// Type mismatch or present-null into a typed column: promote
			// to jsonColumn and retry (cannot fail there).
			promoted := lastCol.toJSON()
			f.columns[w.Field] = promoted
			lastCol = promoted
			lastCol.set(w.Index, w.Value)
		}
	}

	// 3. Removals
	if removed := out.GetRemovedItems(); len(removed) > 0 {
		for idx := range removed {
			if idx < 0 || idx >= f.rowCount {
				return fmt.Errorf("RemoveItem index %d out of range [0, %d)", idx, f.rowCount)
			}
		}
		bitmap := make([]bool, f.rowCount)
		for idx := range removed {
			bitmap[idx] = true
		}
		kept := f.rowCount - len(removed)
		for _, col := range f.columns {
			col.removeByBitmap(bitmap, kept)
		}
		f.rowCount = kept
	}

	// 4. Reorder
	if order := out.GetItemOrder(); order != nil {
		if len(order) != f.rowCount {
			return fmt.Errorf("SetItemOrder length %d does not match item count %d", len(order), f.rowCount)
		}
		// Validate every index is in range AND that the order is a true
		// permutation (each index appears exactly once). Without the
		// permutation check, set_item_order([0,0,0]) silently makes every
		// item a copy of item 0 — a data-loss bug with no observable error.
		seen := make([]bool, f.rowCount)
		for _, origIdx := range order {
			if origIdx < 0 || origIdx >= f.rowCount {
				return fmt.Errorf("SetItemOrder index %d out of range [0, %d)", origIdx, f.rowCount)
			}
			if seen[origIdx] {
				return fmt.Errorf("SetItemOrder duplicate index %d (order must be a permutation)", origIdx)
			}
			seen[origIdx] = true
		}
		// In-place cycle-following permutation; the visited scratch is
		// allocated once and shared across all columns (the cycle
		// structure depends only on `order`).
		visited := make([]bool, len(order))
		for _, col := range f.columns {
			col.reorder(order, visited)
		}
	}

	// 5. Additions (column-major batch append)
	if addedItems := out.GetAddedItems(); len(addedItems) > 0 {
		newCap := f.rowCount + len(addedItems)

		// Pass 1: validate + ensure every incoming field has a column.
		for _, added := range addedItems {
			if recall {
				added["_source"] = opName
			}
			for k, v := range added {
				if err := validateValue(k, v); err != nil {
					return fmt.Errorf("added item write: %w", err)
				}
				if _, ok := f.columns[k]; !ok {
					col := newColumnForValue(v, f.rowCount)
					col.grow(newCap)
					f.columns[k] = col
				}
			}
		}

		// Pass 2: column-major batch append — iterate the columns map once
		// instead of once per added item.
		for field, col := range f.columns {
			col.grow(newCap)
			for _, added := range addedItems {
				v, ok := added[field]
				if !ok {
					col.appendAbsent()
					continue
				}
				if !col.appendVal(v) {
					promoted := col.toJSON()
					promoted.grow(newCap)
					promoted.appendVal(v)
					f.columns[field] = promoted
					col = promoted
				}
			}
		}
		f.rowCount = newCap
	}

	return nil
}

func (f *ColumnFrame) ToResult(commonOut, itemOut []string) *types.Result {
	f.mu.RLock()
	common := projectMap(f.common, commonOut)
	items := make([]map[string]any, f.rowCount)
	// Column-major projection: resolve each output column once, then fill
	// down the rows.
	outCols := make([]column, len(itemOut))
	for k, field := range itemOut {
		outCols[k] = f.columns[field] // nil if absent
	}
	for i := 0; i < f.rowCount; i++ {
		items[i] = make(map[string]any, len(itemOut))
	}
	for k, field := range itemOut {
		col := outCols[k]
		if col == nil {
			continue
		}
		for i := 0; i < f.rowCount; i++ {
			if col.present(i) {
				items[i][field] = col.get(i)
			}
		}
	}
	f.mu.RUnlock()

	return &types.Result{Common: common, Items: items}
}
