// Operator: transform_size
// Type: Transform
// Description: Outputs the current item count to a common field.
//
// Params: (none)
//
// This operator implements ConsumesRowSet so that
// it waits for all recalls and barriers to stabilize the item set.
//
// Metadata contract (typical usage):
//
//	CommonInput:  []
//	CommonOutput: [<target_field>]
//	ItemInput:    []
//	ItemOutput:   []
package transform

import (
	"context"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "transform_size",
		Type:        pine.OpTypeTransform,
		Description: "Outputs the current item count to a common field.",
		Params:      map[string]pine.ParamSpec{},
	}, func() pine.Operator {
		return &SizeOp{}
	})
}

// SizeOp outputs the current item count to the first common_output field.
type SizeOp struct {
	pine.MetadataHolder
	pine.ConcurrentSafeMarker
	pine.ConsumesRowSetMarker
}

func (o *SizeOp) Init(params map[string]any) error { return nil }

func (o *SizeOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	out.SetCommon(o.CommonOutput[0], in.ItemCount())
	return nil
}
