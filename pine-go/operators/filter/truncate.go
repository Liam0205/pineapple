// Operator: filter_truncate
// Type: Filter
// Description: Keeps only the first N items, removing the rest.
//
// Params:
//   - top_n (int64, required, templatable): Number of items to keep. Supports {{field}} interpolation.
//
// Metadata contract (typical usage):
//
//	CommonInput:  []
//	CommonOutput: []
//	ItemInput:    []
//	ItemOutput:   []
package filter

import (
	"context"
	"fmt"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "filter_truncate",
		Type:        pine.OpTypeFilter,
		Description: "Keeps only the first N items, removing the rest.",
		Params: map[string]pine.ParamSpec{
			"top_n": {Type: "int64", Required: true, Templatable: true, Description: "Number of items to keep. Supports {{field}} interpolation."},
		},
	}, func() pine.Operator {
		return &TruncateOp{}
	})
}

// TruncateOp keeps only the first top_n items.
type TruncateOp struct {
	pine.MetadataHolder
	pine.ConsumesRowSetMarker
	pine.MutatesRowSetMarker
	topN int64
}

func (o *TruncateOp) Init(params map[string]any) error {
	switch v := params["top_n"].(type) {
	case int64:
		o.topN = v
	case float64:
		o.topN = int64(v)
	case string:
		// Templatable marker (e.g. "{{user_tier_limit}}"). The per-request
		// value arrives via input.TemplatedParam at execute time;
		// BuildTemplatedParamPlan guarantees the fallback is never read.
		o.topN = 0
	default:
		return fmt.Errorf("filter_truncate: top_n must be numeric, got %T", params["top_n"])
	}
	if o.topN < 0 {
		return fmt.Errorf("filter_truncate: top_n must be non-negative, got %d", o.topN)
	}
	return nil
}

func (o *TruncateOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	// top_n is templatable (#74). When the DSL configured a {{field}}
	// marker the engine resolved it against this request's common frame
	// before Execute; otherwise the init-time value is used. The inner
	// type assertion is unreachable: top_n is declared int64 in schema
	// and ResolveTemplatedParams coerces the substituted string via
	// strconv.ParseInt → int64. Kept as defense in depth.
	topN := o.topN
	if v, ok := in.TemplatedParam("top_n"); ok {
		if n, ok := v.(int64); ok {
			topN = n
		}
	}
	// Mirror Init's invariant at execute time: a templated negative value
	// would otherwise silently remove all items (i >= negative is always
	// true), masking a configuration bug.
	if topN < 0 {
		return fmt.Errorf("filter_truncate: top_n must be non-negative, got %d", topN)
	}
	for i := int64(0); i < int64(in.ItemCount()); i++ {
		if i >= topN {
			out.RemoveItem(int(i))
		}
	}
	return nil
}
