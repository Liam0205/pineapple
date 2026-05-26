// Operator: merge_dedup
// Type: Merge
// Description: Deduplicates items by a key field, keeping the first occurrence.
//
// Params:
//   - strategy (string, optional, default="first"): Dedup strategy — "first" keeps first occurrence.
//
// The dedup key field is determined by item_input metadata (first field).
//
// Metadata contract (typical usage):
//   CommonInput:  []
//   CommonOutput: []
//   ItemInput:    [item_id, _source]
//   ItemOutput:   [item_id]
package merge

import (
	"context"
	"fmt"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "merge_dedup",
		Type:        pine.OpTypeMerge,
		Description: "Deduplicates items by a key field, keeping the first occurrence.",
		Params: map[string]pine.ParamSpec{
			"strategy": {Type: "string", Required: false, Default: "first", Description: "Dedup strategy — \"first\" keeps first occurrence."},
		},
	}, func() pine.Operator {
		return &DedupOp{}
	})
}

// DedupOp removes duplicate items based on a key field.
type DedupOp struct {
	pine.MetadataHolder
	pine.ConsumesRowSetMarker
	pine.MutatesRowSetMarker
	strategy string
}

func (o *DedupOp) Init(params map[string]any) error {
	o.strategy = params["strategy"].(string)
	if o.strategy != "first" {
		return fmt.Errorf("merge_dedup: unsupported strategy %q", o.strategy)
	}
	return nil
}

func (o *DedupOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	dedupBy := o.ItemInput[0]
	seen := make(map[string]struct{})
	for i := 0; i < in.ItemCount(); i++ {
		raw := in.Item(i, dedupBy)
		// V-12: use type-prefixed key so different JSON types with the same
		// string representation don't collide (e.g., bool true vs string
		// "true"). V-9 used bare fmt.Sprint which lost type info. This
		// matches Java (Object.equals type-aware), Python (set uses type in
		// __eq__/__hash__), and C++ (dedup_key prefixes "B:"/"S:"/"F:"/"N:").
		key := fmt.Sprintf("%T:%v", raw, raw)
		if _, dup := seen[key]; dup {
			out.RemoveItem(i)
		} else {
			seen[key] = struct{}{}
		}
	}
	return nil
}
