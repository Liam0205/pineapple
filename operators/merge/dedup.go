// Operator: merge_dedup
// Type: Merge
// Description: Deduplicates items by a key field, keeping the first occurrence.
//
// Params:
//   - dedup_by (string, required): Field name to deduplicate on.
//   - strategy (string, optional, default="first"): Dedup strategy — "first" keeps first occurrence.
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

	pine "github.com/Liam0205/pineapple"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "merge_dedup",
		Type:        pine.OpTypeMerge,
		Description: "Deduplicates items by a key field, keeping the first occurrence.",
		Params: map[string]pine.ParamSpec{
			"dedup_by": {Type: "string", Required: true, Description: "Field name to deduplicate on."},
			"strategy": {Type: "string", Required: false, Default: "first", Description: "Dedup strategy — \"first\" keeps first occurrence."},
		},
	}, func() pine.Operator {
		return &DedupOp{}
	})
}

// DedupOp removes duplicate items based on a key field.
type DedupOp struct {
	dedupBy  string
	strategy string
}

func (o *DedupOp) Init(params map[string]any) error {
	o.dedupBy = params["dedup_by"].(string)
	o.strategy = params["strategy"].(string)
	if o.strategy != "first" {
		return fmt.Errorf("merge_dedup: unsupported strategy %q", o.strategy)
	}
	return nil
}

func (o *DedupOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	seen := make(map[any]struct{})
	for i := 0; i < in.ItemCount(); i++ {
		key := in.Item(i, o.dedupBy)
		if _, dup := seen[key]; dup {
			out.RemoveItem(i)
		} else {
			seen[key] = struct{}{}
		}
	}
	return nil
}
