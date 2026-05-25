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
		// V-9: stringify the key before using as map key. Go maps panic on
		// unhashable types (map/slice) used as keys. C++/Java/Python already
		// use string-based dedup keys via sprint/GoFormat. fmt.Sprint produces
		// a deterministic string for any Go value.
		key := fmt.Sprint(raw)
		if _, dup := seen[key]; dup {
			out.RemoveItem(i)
		} else {
			seen[key] = struct{}{}
		}
	}
	return nil
}
