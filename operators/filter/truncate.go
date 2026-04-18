// Operator: filter_truncate
// Type: Filter
// Description: Keeps only the first N items, removing the rest.
//
// Params:
//   - top_n (int64, required): Number of items to keep.
//
// Metadata contract (typical usage):
//   CommonInput:  []
//   CommonOutput: []
//   ItemInput:    []
//   ItemOutput:   []
package filter

import (
	"context"
	"fmt"

	pine "github.com/Liam0205/pineapple"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "filter_truncate",
		Type:        pine.OpTypeFilter,
		Description: "Keeps only the first N items, removing the rest.",
		Params: map[string]pine.ParamSpec{
			"top_n": {Type: "int64", Required: true, Description: "Number of items to keep."},
		},
	}, func() pine.Operator {
		return &TruncateOp{}
	})
}

// TruncateOp keeps only the first top_n items.
type TruncateOp struct {
	pine.MetadataHolder
	topN int64
}

func (o *TruncateOp) Init(params map[string]any) error {
	switch v := params["top_n"].(type) {
	case int64:
		o.topN = v
	case float64:
		o.topN = int64(v)
	default:
		return fmt.Errorf("filter_truncate: top_n must be numeric, got %T", params["top_n"])
	}
	if o.topN < 0 {
		return fmt.Errorf("filter_truncate: top_n must be non-negative, got %d", o.topN)
	}
	return nil
}

func (o *TruncateOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	for i := int64(0); i < int64(in.ItemCount()); i++ {
		if i >= o.topN {
			out.RemoveItem(int(i))
		}
	}
	return nil
}
