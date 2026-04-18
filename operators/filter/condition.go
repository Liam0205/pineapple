// Operator: filter_condition
// Type: Filter
// Description: Removes items where a specified field equals a given value.
//
// Params:
//   - value (any, required): Items where field == value are removed.
//
// The item field to check is determined by item_input metadata (first field).
//
// Metadata contract (typical usage):
//   CommonInput:  []
//   CommonOutput: []
//   ItemInput:    [<field>]
//   ItemOutput:   []
package filter

import (
	"context"
	"fmt"

	pine "github.com/Liam0205/pineapple"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "filter_condition",
		Type:        pine.OpTypeFilter,
		Description: "Removes items where a specified field equals a given value.",
		Params: map[string]pine.ParamSpec{
			"value": {Type: "any", Required: true, Description: "Items where field == value are removed."},
		},
	}, func() pine.Operator {
		return &ConditionOp{}
	})
}

// ConditionOp removes items where a field matches a specific value.
type ConditionOp struct {
	pine.MetadataHolder
	value any
}

func (o *ConditionOp) Init(params map[string]any) error {
	v, ok := params["value"]
	if !ok {
		return fmt.Errorf("filter_condition: missing required param 'value'")
	}
	o.value = v
	return nil
}

func (o *ConditionOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	field := o.ItemInput[0]
	for i := 0; i < in.ItemCount(); i++ {
		if fmt.Sprintf("%v", in.Item(i, field)) == fmt.Sprintf("%v", o.value) {
			out.RemoveItem(i)
		}
	}
	return nil
}
