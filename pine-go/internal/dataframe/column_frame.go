package dataframe

import (
	"fmt"
	"sync"

	"github.com/Liam0205/pineapple/pine-go/internal/config"
	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

// ColumnFrame is a request-local column-store DataFrame.
// It is concurrency-safe via a single RWMutex: read operations take RLock,
// write operations take Lock.
type ColumnFrame struct {
	mu       sync.RWMutex
	common   map[string]any
	columns  map[string][]any
	present  map[string][]bool
	rowCount int
}

func newColumnFrame(common map[string]any, items []map[string]any) *ColumnFrame {
	c := make(map[string]any, len(common))
	for k, v := range common {
		c[k] = v
	}

	n := len(items)
	cols := make(map[string][]any)
	presence := make(map[string][]bool)

	// Scan all items to collect field union and build columns.
	for i, item := range items {
		for k, v := range item {
			col, ok := cols[k]
			if !ok {
				col = make([]any, n)
				cols[k] = col
				presence[k] = make([]bool, n)
			}
			col[i] = v
			presence[k][i] = true
		}
	}

	return &ColumnFrame{common: c, columns: cols, present: presence, rowCount: n}
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
	return col[index]
}

// ItemColumnView implements types.ColumnReader: it returns a zero-copy
// window of the field's column under a single lock acquisition. Escaping
// the lock is safe because the DAG scheduler hazard-orders writers of this
// field and row-set mutating operators (including the in-place reorder in
// ApplyOutput) relative to the reader — see types.ColumnReader.
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
	return col[offset : offset+count : offset+count], true
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
	strictCols := make([][]any, len(spec.StrictItem))
	strictPresent := make([][]bool, len(spec.StrictItem))
	for k, field := range spec.StrictItem {
		strictCols[k] = f.columns[field] // nil if absent
		strictPresent[k] = f.present[field]
	}
	nullablePresent := make([][]bool, len(spec.NullableItem))
	for k, field := range spec.NullableItem {
		nullablePresent[k] = f.present[field] // nil if absent
	}
	for i := 0; i < f.rowCount; i++ {
		for k, field := range spec.StrictItem {
			if strictCols[k] == nil || !strictPresent[k][i] || strictCols[k][i] == nil {
				return nil, fmt.Errorf("required field %q is nil on item[%d]", field, i)
			}
		}
		for k, field := range spec.NullableItem {
			if nullablePresent[k] == nil || !nullablePresent[k][i] {
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
	// 2 map lookups per write into 2 per field run.
	var lastField string
	var lastCol []any
	var lastPresent []bool
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
				col = make([]any, f.rowCount)
				f.columns[w.Field] = col
				f.present[w.Field] = make([]bool, f.rowCount)
			}
			lastField = w.Field
			lastCol = col
			lastPresent = f.present[w.Field]
		}
		lastCol[w.Index] = w.Value
		lastPresent[w.Index] = true
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
		newCount := f.rowCount - len(removed)
		for field, col := range f.columns {
			newCol := make([]any, 0, newCount)
			newPresent := make([]bool, 0, newCount)
			for i, v := range col {
				if !bitmap[i] {
					newCol = append(newCol, v)
					newPresent = append(newPresent, f.present[field][i])
				}
			}
			f.columns[field] = newCol
			f.present[field] = newPresent
		}
		f.rowCount = newCount
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
		// In-place permutation via cycle following. `order` is a validated
		// length-N permutation of [0, N); each cycle is walked once,
		// performing ≤ N moves per column with zero per-column allocation.
		// Replaces the prior "allocate fresh slice + copy each slot" loop
		// which paid 2K large allocations (data + presence) per reorder.
		// The cycle structure depends only on `order`, so `visited` is
		// allocated once and reset per column.
		visited := make([]bool, len(order))
		for field, col := range f.columns {
			presentCol := f.present[field]
			for i := range visited {
				visited[i] = false
			}
			for i := range order {
				if visited[i] {
					continue
				}
				if order[i] == i {
					visited[i] = true
					continue
				}
				tmpVal := col[i]
				tmpPres := presentCol[i]
				j := i
				for {
					src := order[j]
					if src == i {
						col[j] = tmpVal
						presentCol[j] = tmpPres
						visited[j] = true
						break
					}
					col[j] = col[src]
					presentCol[j] = presentCol[src]
					visited[j] = true
					j = src
				}
			}
		}
	}

	// 5. Additions (zero-copy: take ownership of the caller's map)
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
					f.columns[k] = make([]any, f.rowCount, newCap)
					f.present[k] = make([]bool, f.rowCount, newCap)
				}
			}
		}

		// Pass 2: column-major batch append — iterate the columns map once
		// instead of once per added item (the previous item-major loop paid
		// O(added × cols) map iterations).
		for field, col := range f.columns {
			presentCol := f.present[field]
			if cap(col) < newCap {
				grown := make([]any, f.rowCount, newCap)
				copy(grown, col)
				col = grown
				grownP := make([]bool, f.rowCount, newCap)
				copy(grownP, presentCol)
				presentCol = grownP
			}
			for _, added := range addedItems {
				v, ok := added[field]
				col = append(col, v)
				presentCol = append(presentCol, ok)
			}
			f.columns[field] = col
			f.present[field] = presentCol
		}
		f.rowCount = newCap
	}

	return nil
}

func (f *ColumnFrame) ToResult(commonOut, itemOut []string) *types.Result {
	f.mu.RLock()
	common := projectMap(f.common, commonOut)
	items := make([]map[string]any, f.rowCount)
	for i := 0; i < f.rowCount; i++ {
		row := make(map[string]any, len(itemOut))
		for _, field := range itemOut {
			if col, ok := f.columns[field]; ok && f.present[field][i] {
				row[field] = col[i]
			}
		}
		items[i] = row
	}
	f.mu.RUnlock()

	return &types.Result{Common: common, Items: items}
}
