package dataframe

import (
	"fmt"
	"reflect"

	"github.com/Liam0205/pineapple/internal/types"
)

// RowFrame is a request-local row-store DataFrame.
type RowFrame struct {
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
	return f.common[field]
}

func (f *RowFrame) SetCommon(field string, value any) {
	f.common[field] = value
}

func (f *RowFrame) ItemCount() int {
	return len(f.items)
}

func (f *RowFrame) Item(index int, field string) any {
	if index < 0 || index >= len(f.items) {
		return nil
	}
	return f.items[index][field]
}

func (f *RowFrame) BuildInput(
	commonFields []string,
	itemFields []string,
	commonDefaults map[string]any,
	itemDefaults map[string]any,
) *types.OperatorInput {
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

func (f *RowFrame) ApplyOutput(out *types.OperatorOutput, opName string, recall bool) error {
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

func (f *RowFrame) ToResult(commonOut, itemOut []string) *types.Result {
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
	switch value.(type) {
	case bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, string:
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
