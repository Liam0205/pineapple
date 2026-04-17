// Operator: reorder_sort
// Category: Reorder
// Description: Sorts items by a numeric field in ascending or descending order.
//
// Params:
//   - field (string, required): Item field to sort by.
//   - order (string, optional, default="desc"): Sort direction — "asc" or "desc".
//
// Metadata contract (typical usage):
//   CommonInput:  []
//   CommonOutput: []
//   ItemInput:    [<field>]
//   ItemOutput:   []
package reorder

import (
	"context"
	"fmt"
	"sort"

	pine "github.com/Liam0205/pineapple"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "reorder_sort",
		Category:    "Reorder",
		Description: "Sorts items by a numeric field in ascending or descending order.",
		Params: map[string]pine.ParamSpec{
			"field": {Type: "string", Required: true, Description: "Item field to sort by."},
			"order": {Type: "string", Required: false, Default: "desc", Description: "Sort direction — \"asc\" or \"desc\"."},
		},
	}, func() pine.Operator {
		return &SortOp{}
	})
}

// SortOp sorts items by a numeric field.
type SortOp struct {
	field     string
	ascending bool
}

func (o *SortOp) Init(params map[string]any) error {
	o.field = params["field"].(string)
	order := params["order"].(string)
	switch order {
	case "asc":
		o.ascending = true
	case "desc":
		o.ascending = false
	default:
		return fmt.Errorf("reorder_sort: unsupported order %q", order)
	}
	return nil
}

func (o *SortOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	n := in.ItemCount()
	if n == 0 {
		return nil
	}

	// Collect values and indices
	type entry struct {
		idx int
		val float64
	}
	entries := make([]entry, n)
	for i := 0; i < n; i++ {
		v, err := sortToFloat64(in.Item(i, o.field))
		if err != nil {
			return fmt.Errorf("reorder_sort: item[%d].%s: %w", i, o.field, err)
		}
		entries[i] = entry{idx: i, val: v}
	}

	// Sort
	if o.ascending {
		sort.Slice(entries, func(i, j int) bool { return entries[i].val < entries[j].val })
	} else {
		sort.Slice(entries, func(i, j int) bool { return entries[i].val > entries[j].val })
	}

	// Build order
	order := make([]int, n)
	for i, e := range entries {
		order[i] = e.idx
	}
	out.SetItemOrder(order)
	return nil
}

func sortToFloat64(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int64:
		return float64(x), nil
	case int:
		return float64(x), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", v)
	}
}
