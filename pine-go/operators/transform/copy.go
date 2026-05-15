// Operator: transform_copy
// Type: Transform
// Description: Copies field values between common and item dimensions.
//
// Params:
//   - direction (string, required): Copy direction. One of
//     "common_to_item", "item_to_common", "common_to_common", "item_to_item".
//
// Fields are determined by metadata: input fields are read in order and
// written to the corresponding output fields by position.
//
// Metadata contract (typical usage):
//   direction="common_to_item":
//     CommonInput:  [<source_fields...>]
//     ItemOutput:   [<target_fields...>]  (each item gets the same common value)
//   direction="item_to_item":
//     ItemInput:    [<source_fields...>]
//     ItemOutput:   [<target_fields...>]
//   direction="common_to_common":
//     CommonInput:  [<source_fields...>]
//     CommonOutput: [<target_fields...>]
//   direction="item_to_common" (typically used with row_dependency):
//     ItemInput:    [<source_field>]
//     CommonOutput: [<target_field>]   (collects all item values into a list)
package transform

import (
	"context"
	"fmt"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "transform_copy",
		Type:        pine.OpTypeTransform,
		Description: "Copies field values between common and item dimensions.",
		Params: map[string]pine.ParamSpec{
			"direction": {Type: "string", Required: true, Description: `Copy direction: "common_to_item", "item_to_common", "common_to_common", or "item_to_item".`},
		},
	}, func() pine.Operator {
		return &CopyOp{}
	})
}

// CopyOp copies field values between common and item dimensions.
type CopyOp struct {
	pine.MetadataHolder
	pine.ConcurrentSafeMarker
	direction string
}

func (o *CopyOp) Init(params map[string]any) error {
	o.direction = params["direction"].(string)
	switch o.direction {
	case "common_to_item", "item_to_common", "common_to_common", "item_to_item":
		return nil
	default:
		return fmt.Errorf("transform_copy: unsupported direction %q", o.direction)
	}
}

func (o *CopyOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	switch o.direction {
	case "common_to_common":
		for i, src := range o.CommonInput {
			out.SetCommon(o.CommonOutput[i], in.Common(src))
		}

	case "common_to_item":
		n := in.ItemCount()
		for i, src := range o.CommonInput {
			val := in.Common(src)
			dst := o.ItemOutput[i]
			for j := 0; j < n; j++ {
				out.SetItem(j, dst, val)
			}
		}

	case "item_to_item":
		n := in.ItemCount()
		for i, src := range o.ItemInput {
			dst := o.ItemOutput[i]
			for j := 0; j < n; j++ {
				out.SetItem(j, dst, in.Item(j, src))
			}
		}

	case "item_to_common":
		// Collect all item values into a list per field.
		n := in.ItemCount()
		for i, src := range o.ItemInput {
			vals := make([]any, n)
			for j := 0; j < n; j++ {
				vals[j] = in.Item(j, src)
			}
			out.SetCommon(o.CommonOutput[i], vals)
		}
	}
	return nil
}
