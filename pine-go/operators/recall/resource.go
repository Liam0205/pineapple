// Operator: recall_resource
// Type: Recall
// Description: Recalls items from a named resource.
//
// Params:
//   - resource_name (string, required): Name of the resource to read. The resource
//     value must be a []map[string]any (list of item maps).
//
// Metadata contract (typical usage):
//
//	ItemOutput: [<fields present in the resource items>]
package recall

import (
	"context"
	"fmt"

	pine "github.com/Liam0205/pineapple/pine-go"
	"github.com/Liam0205/pineapple/pine-go/pkg/resource"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "recall_resource",
		Type:        pine.OpTypeRecall,
		Description: "Recalls items from a named resource.",
		Params: map[string]pine.ParamSpec{
			"resource_name": {Type: "string", Required: true, Description: "Name of the resource to read."},
		},
	}, func() pine.Operator {
		return &ResourceOp{}
	})
}

type ResourceOp struct {
	pine.MetadataHolder
	resourceName string
}

func (o *ResourceOp) Init(params map[string]any) error {
	o.resourceName = params["resource_name"].(string)
	return nil
}

func (o *ResourceOp) Execute(ctx context.Context, _ *pine.OperatorInput, out *pine.OperatorOutput) error {
	rp := resource.FromContext(ctx)
	if rp == nil {
		return fmt.Errorf("recall_resource: no resource provider in context")
	}
	raw, ok := rp.Get(o.resourceName)
	if !ok {
		return fmt.Errorf("recall_resource: resource %q not found", o.resourceName)
	}

	switch items := raw.(type) {
	case []map[string]any:
		for _, item := range items {
			cp := make(map[string]any, len(item))
			for k, v := range item {
				cp[k] = v
			}
			out.AddItem(cp)
		}
	case []any:
		for i, v := range items {
			m, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("recall_resource: items[%d] is %T, want map[string]any", i, v)
			}
			cp := make(map[string]any, len(m))
			for k, val := range m {
				cp[k] = val
			}
			out.AddItem(cp)
		}
	default:
		return fmt.Errorf("recall_resource: resource %q is %T, want []map[string]any", o.resourceName, raw)
	}
	return nil
}
