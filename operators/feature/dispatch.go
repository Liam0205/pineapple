// Operator: feature_dispatch
// Category: Feature
// Description: Copies a common-side field value to every item as an item-side field.
//
// Params:
//   - common_field (string, required): Source common field to read.
//   - item_field (string, required): Target item field to write.
//
// Metadata contract (typical usage):
//   CommonInput:  [<common_field>]
//   CommonOutput: []
//   ItemInput:    []
//   ItemOutput:   [<item_field>]
package feature

import (
	"context"

	pine "github.com/Liam0205/pineapple"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "feature_dispatch",
		Category:    "Feature",
		Description: "Copies a common-side field value to every item as an item-side field.",
		Params: map[string]pine.ParamSpec{
			"common_field": {Type: "string", Required: true, Description: "Source common field to read."},
			"item_field":   {Type: "string", Required: true, Description: "Target item field to write."},
		},
	}, func() pine.Operator {
		return &DispatchOp{}
	})
}

// DispatchOp copies a common field value to all items.
type DispatchOp struct {
	commonField string
	itemField   string
}

func (o *DispatchOp) Init(params map[string]any) error {
	o.commonField = params["common_field"].(string)
	o.itemField = params["item_field"].(string)
	return nil
}

func (o *DispatchOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	val := in.Common(o.commonField)
	for i := 0; i < in.ItemCount(); i++ {
		out.SetItem(i, o.itemField, val)
	}
	return nil
}
