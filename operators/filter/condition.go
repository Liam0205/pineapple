// Operator: filter_condition
// Category: Filter
// Description: Removes items where a specified field equals a given value.
//
// Params:
//   - field (string, required): Item field to check.
//   - value (any, required): Items where field == value are removed.
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
		Category:    "Filter",
		Description: "Removes items where a specified field equals a given value.",
		Params: map[string]pine.ParamSpec{
			"field": {Type: "string", Required: true, Description: "Item field to check."},
			"value": {Type: "any", Required: true, Description: "Items where field == value are removed."},
		},
	}, func() pine.Operator {
		return &ConditionOp{}
	})
}

// ConditionOp removes items where a field matches a specific value.
type ConditionOp struct {
	field string
	value any
}

func (o *ConditionOp) Init(params map[string]any) error {
	o.field = params["field"].(string)
	v, ok := params["value"]
	if !ok {
		return fmt.Errorf("filter_condition: missing required param 'value'")
	}
	o.value = v
	return nil
}

func (o *ConditionOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	for i := 0; i < in.ItemCount(); i++ {
		if fmt.Sprintf("%v", in.Item(i, o.field)) == fmt.Sprintf("%v", o.value) {
			out.RemoveItem(i)
		}
	}
	return nil
}
