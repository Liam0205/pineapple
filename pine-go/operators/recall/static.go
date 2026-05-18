// Operator: recall_static
// Type: Recall
// Description: Emits a configurable static set of items for testing and validation.
//
// Params:
//   - items (any, required): JSON array of item maps to emit as candidates.
//
// Metadata contract (typical usage):
//   CommonInput:  []
//   CommonOutput: []
//   ItemInput:    []
//   ItemOutput:   [item_id, ...]
package recall

import (
	"context"
	"fmt"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "recall_static",
		Type:        pine.OpTypeRecall,
		Description: "Emits a configurable static set of items for testing and validation.",
		Params: map[string]pine.ParamSpec{
			"items": {Type: "any", Required: true, Description: "JSON array of item maps to emit as candidates."},
		},
	}, func() pine.Operator {
		return &StaticOp{}
	})
}

// StaticOp emits a fixed set of items configured at Init time.
type StaticOp struct {
	pine.MetadataHolder
	items []map[string]any
}

func (o *StaticOp) Init(params map[string]any) error {
	raw, ok := params["items"]
	if !ok {
		return fmt.Errorf("recall_static: missing required param 'items'")
	}
	arr, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("recall_static: 'items' must be a JSON array, got %T", raw)
	}
	o.items = make([]map[string]any, len(arr))
	for i, v := range arr {
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("recall_static: items[%d] must be an object, got %T", i, v)
		}
		o.items[i] = m
	}
	return nil
}

func (o *StaticOp) Execute(_ context.Context, _ *pine.OperatorInput, out *pine.OperatorOutput) error {
	for _, item := range o.items {
		cp := make(map[string]any, len(item))
		for k, v := range item {
			cp[k] = v
		}
		out.AddItem(cp)
	}
	return nil
}
