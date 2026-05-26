package dataframe

import (
	"fmt"
	"math"
	"reflect"
	"sync"

	"github.com/Liam0205/pineapple/pine-go/internal/config"
	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

// RowFrame is a request-local row-store DataFrame.
// It is concurrency-safe: concurrent reads (BuildInput, Common, Item) are allowed,
// while mutations (ApplyOutput, SetCommon) are exclusive.
type RowFrame struct {
	mu     sync.RWMutex
	common map[string]any
	items  []map[string]any
}

func newRowFrame(common map[string]any, items []map[string]any) *RowFrame {
	c := make(map[string]any, len(common))
	for k, v := range common {
		c[k] = v
	}
	its := make([]map[string]any, len(items))
	for i, item := range items {
		row := make(map[string]any, len(item))
		for k, v := range item {
			row[k] = v
		}
		its[i] = row
	}
	return &RowFrame{common: c, items: its}
}

func (f *RowFrame) Common(field string) any {
	f.mu.RLock()
	v := f.common[field]
	f.mu.RUnlock()
	return v
}

func (f *RowFrame) SetCommon(field string, value any) {
	f.mu.Lock()
	f.common[field] = value
	f.mu.Unlock()
}

func (f *RowFrame) ItemCount() int {
	f.mu.RLock()
	n := len(f.items)
	f.mu.RUnlock()
	return n
}

func (f *RowFrame) Item(index int, field string) any {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if index < 0 || index >= len(f.items) {
		return nil
	}
	return f.items[index][field]
}

func (f *RowFrame) BuildInput(
	opName string,
	spec *config.InputFieldSpec,
) (*types.OperatorInput, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	totalCommon := len(spec.StrictCommon) + len(spec.DefaultedCommon)
	cs := make(map[string]any, totalCommon)

	for _, field := range spec.StrictCommon {
		v, exists := f.common[field]
		if !exists || v == nil {
			return nil, fmt.Errorf("operator %q: required field %q is nil in common", opName, field)
		}
		cs[field] = v
	}
	for _, df := range spec.DefaultedCommon {
		v, exists := f.common[df.Name]
		if !exists || v == nil {
			cs[df.Name] = df.Default
		} else {
			cs[df.Name] = v
		}
	}

	totalItem := len(spec.StrictItem) + len(spec.DefaultedItem)
	its := make([]map[string]any, len(f.items))
	for i, item := range f.items {
		row := make(map[string]any, totalItem)

		for _, field := range spec.StrictItem {
			v, exists := item[field]
			if !exists || v == nil {
				return nil, fmt.Errorf("operator %q: required field %q is nil on item[%d]", opName, field, i)
			}
			row[field] = v
		}
		for _, df := range spec.DefaultedItem {
			v, exists := item[df.Name]
			if !exists || v == nil {
				row[df.Name] = df.Default
			} else {
				row[df.Name] = v
			}
		}

		its[i] = row
	}

	return types.NewOperatorInput(cs, its), nil
}

func (f *RowFrame) ApplyOutput(out *types.OperatorOutput, opName string, recall bool) error {
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
		if idx < 0 || idx >= len(f.items) {
			return fmt.Errorf("SetItem index %d out of range [0, %d)", idx, len(f.items))
		}
		for field, value := range fields {
			if err := validateValue(field, value); err != nil {
				return fmt.Errorf("item[%d] write: %w", idx, err)
			}
			f.items[idx][field] = value
		}
	}

	// 3. Removals
	removed := out.GetRemovedItems()
	if len(removed) > 0 {
		for idx := range removed {
			if idx < 0 || idx >= len(f.items) {
				return fmt.Errorf("RemoveItem index %d out of range [0, %d)", idx, len(f.items))
			}
		}
		surviving := make([]map[string]any, 0, len(f.items)-len(removed))
		for i, item := range f.items {
			if _, rm := removed[i]; !rm {
				surviving = append(surviving, item)
			}
		}
		f.items = surviving
	}

	// 4. Reorder
	if order := out.GetItemOrder(); order != nil {
		if len(order) != len(f.items) {
			return fmt.Errorf("SetItemOrder length %d does not match item count %d", len(order), len(f.items))
		}
		// Permutation check — without this, set_item_order([0,0,0]) would
		// silently duplicate item 0 across the frame.
		seen := make([]bool, len(f.items))
		reordered := make([]map[string]any, len(order))
		for newIdx, origIdx := range order {
			if origIdx < 0 || origIdx >= len(f.items) {
				return fmt.Errorf("SetItemOrder index %d out of range [0, %d)", origIdx, len(f.items))
			}
			if seen[origIdx] {
				return fmt.Errorf("SetItemOrder duplicate index %d (order must be a permutation)", origIdx)
			}
			seen[origIdx] = true
			reordered[newIdx] = f.items[origIdx]
		}
		f.items = reordered
	}

	// 5. Additions
	for _, added := range out.GetAddedItems() {
		row := make(map[string]any, len(added)+1)
		for k, v := range added {
			if err := validateValue(k, v); err != nil {
				return fmt.Errorf("added item write: %w", err)
			}
			row[k] = v
		}
		if recall {
			row["_source"] = opName
		}
		f.items = append(f.items, row)
	}

	return nil
}

func (f *RowFrame) ToResult(commonOut, itemOut []string) *types.Result {
	f.mu.RLock()
	defer f.mu.RUnlock()

	common := projectMap(f.common, commonOut)
	items := make([]map[string]any, len(f.items))
	for i, item := range f.items {
		items[i] = projectMap(item, itemOut)
	}
	return &types.Result{Common: common, Items: items}
}

func projectMap(src map[string]any, fields []string) map[string]any {
	out := make(map[string]any, len(fields))
	for _, k := range fields {
		if v, ok := src[k]; ok {
			out[k] = v
		}
	}
	return out
}

func validateValue(field string, value any) error {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("field %q: NaN/Inf is not a valid JSON value", field)
		}
		return nil
	case float32:
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return fmt.Errorf("field %q: NaN/Inf is not a valid JSON value", field)
		}
		return nil
	case bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		string:
		return nil
	case []any, map[string]any:
		return nil
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map:
		return nil
	}
	return fmt.Errorf("field %q: unsupported type %T", field, value)
}
