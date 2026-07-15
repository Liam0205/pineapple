// Operator: recall_static
// Type: Recall
// Description: Emits a configurable static set of items for testing and validation.
//
// Params:
//   - items (any, required): JSON array of item maps to emit as candidates.
//   - set_common (any, optional): JSON object of common fields to write. Lets a
//     recall emit request-level common state (e.g. a recall-generated request
//     id) that downstream operators consume. Field names must be declared in
//     the operator's common_output metadata for the DAG to build read edges.
//
// Metadata contract (typical usage):
//
//	CommonInput:  []
//	CommonOutput: []
//	ItemInput:    []
//	ItemOutput:   [item_id, ...]
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
			"items":      {Type: "any", Required: true, Description: "JSON array of item maps to emit as candidates."},
			"set_common": {Type: "any", Required: false, Description: "JSON object of common fields the recall writes."},
		},
	}, func() pine.Operator {
		return &StaticOp{}
	})
}

// StaticOp emits a fixed set of items configured at Init time, and optionally
// writes a fixed set of common fields.
type StaticOp struct {
	pine.MetadataHolder
	pine.AdditiveWritesRowSetMarker
	items     []map[string]any
	setCommon map[string]any
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
	if sc, ok := params["set_common"]; ok && sc != nil {
		m, ok := sc.(map[string]any)
		if !ok {
			return fmt.Errorf("recall_static: 'set_common' must be a JSON object, got %T", sc)
		}
		o.setCommon = m
	}
	return nil
}

func (o *StaticOp) Execute(_ context.Context, _ *pine.OperatorInput, out *pine.OperatorOutput) error {
	for field, value := range o.setCommon {
		out.SetCommon(field, value)
	}
	for _, item := range o.items {
		cp := make(map[string]any, len(item))
		for k, v := range item {
			cp[k] = v
		}
		out.AddItem(cp)
	}
	return nil
}
