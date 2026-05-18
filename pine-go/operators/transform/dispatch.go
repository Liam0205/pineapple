// Operator: transform_dispatch
// Type: Transform
// Description: Copies a common-side field value to every item as an item-side field.
//
// The source common field is determined by common_input metadata (first field).
// The target item field is determined by item_output metadata (first field).
//
// Metadata contract (typical usage):
//   CommonInput:  [<common_field>]
//   CommonOutput: []
//   ItemInput:    []
//   ItemOutput:   [<item_field>]
package transform

import (
	"context"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "transform_dispatch",
		Type:        pine.OpTypeTransform,
		Description: "Copies a common-side field value to every item as an item-side field.",
		Params:      map[string]pine.ParamSpec{},
	}, func() pine.Operator {
		return &DispatchOp{}
	})
}

// DispatchOp copies a common field value to all items.
type DispatchOp struct {
	pine.MetadataHolder
	pine.ConcurrentSafeMarker
}

func (o *DispatchOp) Init(params map[string]any) error {
	return nil
}

func (o *DispatchOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	commonField := o.CommonInput[0]
	itemField := o.ItemOutput[0]
	val := in.Common(commonField)
	for i := 0; i < in.ItemCount(); i++ {
		out.SetItem(i, itemField, val)
	}
	return nil
}
