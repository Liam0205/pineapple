// Operator: transform_resource_lookup
// Type: Transform
// Description: Enriches items by looking up values from a named resource.
//
// Params:
//   - resource_name (string, required): Name of the resource to read. The resource
//     value must be a map[string]any serving as a lookup table.
//   - lookup_key (string, required): Item field whose value is used as the lookup key.
//   - output_field (string, required): Item field to write the looked-up value to.
//   - default_value (any, optional): Value to use when the key is not found in the
//     resource. If unset, missing keys are silently skipped.
//
// Metadata contract (typical usage):
//
//	ItemInput:  [<lookup_key>]
//	ItemOutput: [<output_field>]
package transform

import (
	"context"
	"fmt"
	"strconv"

	pine "github.com/Liam0205/pineapple/pine-go"
	"github.com/Liam0205/pineapple/pine-go/pkg/resource"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "transform_resource_lookup",
		Type:        pine.OpTypeTransform,
		Description: "Enriches items by looking up values from a named resource.",
		Params: map[string]pine.ParamSpec{
			"resource_name": {Type: "string", Required: true, Description: "Name of the resource to read."},
			"lookup_key":    {Type: "string", Required: true, Description: "Item field whose value is used as the lookup key."},
			"output_field":  {Type: "string", Required: true, Description: "Item field to write the looked-up value to."},
			"default_value": {Type: "any", Required: false, Description: "Value to use when the key is not found. Missing keys are skipped if unset."},
		},
	}, func() pine.Operator {
		return &ResourceLookupOp{}
	})
}

type ResourceLookupOp struct {
	pine.MetadataHolder
	pine.ConcurrentSafeMarker
	resourceName string
	lookupKey    string
	outputField  string
	defaultValue any
	hasDefault   bool
}

func (o *ResourceLookupOp) Init(params map[string]any) error {
	o.resourceName = params["resource_name"].(string)
	o.lookupKey = params["lookup_key"].(string)
	o.outputField = params["output_field"].(string)
	if v, ok := params["default_value"]; ok {
		o.defaultValue = v
		o.hasDefault = true
	}
	return nil
}

func (o *ResourceLookupOp) Execute(ctx context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	rp := resource.FromContext(ctx)
	if rp == nil {
		return fmt.Errorf("transform_resource_lookup: no resource provider in context")
	}
	raw, ok := rp.Get(o.resourceName)
	if !ok {
		return fmt.Errorf("transform_resource_lookup: resource %q not found", o.resourceName)
	}
	table, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("transform_resource_lookup: resource %q is %T, want map[string]any", o.resourceName, raw)
	}

	for i := 0; i < in.ItemCount(); i++ {
		raw := in.Item(i, o.lookupKey)
		if raw == nil {
			if o.hasDefault {
				out.SetItem(i, o.outputField, o.defaultValue)
			}
			continue
		}
		var key string
		switch v := raw.(type) {
		case string:
			key = v
		case float64:
			if v == float64(int64(v)) {
				key = strconv.FormatInt(int64(v), 10)
			} else {
				key = strconv.FormatFloat(v, 'f', -1, 64)
			}
		default:
			key = fmt.Sprintf("%v", raw)
		}
		if val, found := table[key]; found {
			out.SetItem(i, o.outputField, val)
		} else if o.hasDefault {
			out.SetItem(i, o.outputField, o.defaultValue)
		}
	}
	return nil
}
