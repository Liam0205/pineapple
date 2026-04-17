package dataframe

import (
	"fmt"
	"reflect"

	"github.com/Liam0205/pineapple/internal/types"
)

// Frame is a request-local row-store DataFrame.
// Not concurrency-safe — the runtime scheduler guards access with a mutex.
type Frame struct {
	common map[string]any
	items  []map[string]any
}

// New creates a Frame by shallow-copying the request data.
func New(common map[string]any, items []map[string]any) *Frame {
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
	return &Frame{common: c, items: its}
}

// Common returns a common-side field value.
func (f *Frame) Common(field string) any {
	return f.common[field]
}

// SetCommon writes a common-side field.
func (f *Frame) SetCommon(field string, value any) {
	f.common[field] = value
}

// ItemCount returns the number of items.
func (f *Frame) ItemCount() int {
	return len(f.items)
}

// Item returns a field value for the item at index.
func (f *Frame) Item(index int, field string) any {
	if index < 0 || index >= len(f.items) {
		return nil
	}
	return f.items[index][field]
}

// BuildInput constructs an OperatorInput snapshot from the current frame state.
// commonFields/itemFields are the $metadata declared fields.
// commonDefaults/itemDefaults provide fallback values for nil/missing fields.
func BuildInput(
	f *Frame,
	commonFields []string,
	itemFields []string,
	commonDefaults map[string]any,
	itemDefaults map[string]any,
) *types.OperatorInput {
	// Build common snapshot with defaults
	cs := make(map[string]any, len(commonFields))
	for _, field := range commonFields {
		v := f.common[field]
		if v == nil {
			if d, ok := commonDefaults[field]; ok {
				v = d
			}
		}
		cs[field] = v
	}

	// Build item snapshots with defaults
	its := make([]map[string]any, len(f.items))
	for i, item := range f.items {
		row := make(map[string]any, len(itemFields))
		for _, field := range itemFields {
			v := item[field]
			if v == nil {
				if d, ok := itemDefaults[field]; ok {
					v = d
				}
			}
			row[field] = v
		}
		its[i] = row
	}

	return types.NewOperatorInput(cs, its)
}

// ApplyOutput applies an operator's output to the frame.
// Order: common writes -> item field writes -> removals -> reorder -> additions.
// If recall is true, added items get an injected "_source" field.
func ApplyOutput(f *Frame, out *types.OperatorOutput, opName string, recall bool) error {
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

	// 3. Removals — build surviving items list
	removed := out.GetRemovedItems()
	if len(removed) > 0 {
		surviving := make([]map[string]any, 0, len(f.items)-len(removed))
		for i, item := range f.items {
			if _, rm := removed[i]; !rm {
				surviving = append(surviving, item)
			}
		}
		f.items = surviving
	}

	// 4. Reorder surviving items
	if order := out.GetItemOrder(); order != nil {
		if len(order) != len(f.items) {
			return fmt.Errorf("SetItemOrder length %d does not match item count %d", len(order), len(f.items))
		}
		reordered := make([]map[string]any, len(order))
		for newIdx, origIdx := range order {
			if origIdx < 0 || origIdx >= len(f.items) {
				return fmt.Errorf("SetItemOrder index %d out of range [0, %d)", origIdx, len(f.items))
			}
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

// ToResult extracts the final state into a Result.
func ToResult(f *Frame) *types.Result {
	common := make(map[string]any, len(f.common))
	for k, v := range f.common {
		common[k] = v
	}
	items := make([]map[string]any, len(f.items))
	for i, item := range f.items {
		row := make(map[string]any, len(item))
		for k, v := range item {
			row[k] = v
		}
		items[i] = row
	}
	return &types.Result{Common: common, Items: items}
}

// validateValue checks that a value is a Pine-supported type.
// Supported: nil, bool, int64, float64, string, []any, map[string]any.
// Other integer and float types are also accepted (widened at runtime).
func validateValue(field string, value any) error {
	if value == nil {
		return nil
	}
	switch value.(type) {
	case bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, string:
		return nil
	case []any, map[string]any:
		return nil
	}
	// Check for slices and maps with reflection as a fallback
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map:
		return nil
	}
	return fmt.Errorf("field %q: unsupported type %T", field, value)
}
