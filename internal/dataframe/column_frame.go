package dataframe

import (
	"fmt"
	"sync"

	"github.com/Liam0205/pineapple/internal/types"
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

func (f *ColumnFrame) BuildInput(
	commonFields []string,
	itemFields []string,
	commonDefaults map[string]any,
	itemDefaults map[string]any,
) *types.OperatorInput {
	f.mu.RLock()
	cs := make(map[string]any, len(commonFields))
	for _, field := range commonFields {
		v, exists := f.common[field]
		if exists {
			if v == nil {
				if d, ok := commonDefaults[field]; ok {
					v = d
				}
			}
			cs[field] = v
		} else if d, ok := commonDefaults[field]; ok {
			cs[field] = d
		}
	}

	its := make([]map[string]any, f.rowCount)
	for i := 0; i < f.rowCount; i++ {
		row := make(map[string]any, len(itemFields))
		for _, field := range itemFields {
			col, colExists := f.columns[field]
			if colExists && f.present[field][i] {
				v := col[i]
				if v == nil {
					if d, ok := itemDefaults[field]; ok {
						v = d
					}
				}
				row[field] = v
			} else if d, ok := itemDefaults[field]; ok {
				row[field] = d
			}
		}
		its[i] = row
	}
	f.mu.RUnlock()

	return types.NewOperatorInput(cs, its)
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
	for idx, fields := range out.GetItemWrites() {
		if idx < 0 || idx >= f.rowCount {
			return fmt.Errorf("SetItem index %d out of range [0, %d)", idx, f.rowCount)
		}
		for field, value := range fields {
			if err := validateValue(field, value); err != nil {
				return fmt.Errorf("item[%d] write: %w", idx, err)
			}
			col, ok := f.columns[field]
			if !ok {
				col = make([]any, f.rowCount)
				f.columns[field] = col
				f.present[field] = make([]bool, f.rowCount)
			}
			col[idx] = value
			f.present[field][idx] = true
		}
	}

	// 3. Removals
	if removed := out.GetRemovedItems(); len(removed) > 0 {
		for idx := range removed {
			if idx < 0 || idx >= f.rowCount {
				return fmt.Errorf("RemoveItem index %d out of range [0, %d)", idx, f.rowCount)
			}
		}
		newCount := f.rowCount - len(removed)
		for field, col := range f.columns {
			newCol := make([]any, 0, newCount)
			newPresent := make([]bool, 0, newCount)
			for i, v := range col {
				if _, rm := removed[i]; !rm {
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
		for _, origIdx := range order {
			if origIdx < 0 || origIdx >= f.rowCount {
				return fmt.Errorf("SetItemOrder index %d out of range [0, %d)", origIdx, f.rowCount)
			}
		}
		for field, col := range f.columns {
			newCol := make([]any, len(order))
			newPresent := make([]bool, len(order))
			for newIdx, origIdx := range order {
				newCol[newIdx] = col[origIdx]
				newPresent[newIdx] = f.present[field][origIdx]
			}
			f.columns[field] = newCol
			f.present[field] = newPresent
		}
	}

	// 5. Additions
	for _, added := range out.GetAddedItems() {
		row := make(map[string]any, len(added)+1)
		for k, v := range added {
			row[k] = v
		}
		if recall {
			row["_source"] = opName
		}
		for k, v := range row {
			if err := validateValue(k, v); err != nil {
				return fmt.Errorf("added item write: %w", err)
			}
			if _, ok := f.columns[k]; !ok {
				f.columns[k] = make([]any, f.rowCount, f.rowCount+1)
				f.present[k] = make([]bool, f.rowCount, f.rowCount+1)
			}
		}
		for field, col := range f.columns {
			value, ok := row[field]
			f.columns[field] = append(col, value)
			f.present[field] = append(f.present[field], ok)
		}
		f.rowCount++
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
