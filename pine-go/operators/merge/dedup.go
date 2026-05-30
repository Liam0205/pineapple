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
	"encoding/json"
	"fmt"
	"strconv"

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
		key := dedupKey(raw)
		if _, dup := seen[key]; dup {
			out.RemoveItem(i)
		} else {
			seen[key] = struct{}{}
		}
	}
	return nil
}

func dedupKey(v any) string {
	if v == nil {
		return "N:"
	}
	switch x := v.(type) {
	case bool:
		if x {
			return "B:1"
		}
		return "B:0"
	case float64:
		if x == 0 {
			x = 0 // canonicalize -0.0 → +0.0 (IEEE 754)
		}
		return "F:" + strconv.FormatFloat(x, 'g', -1, 64)
	case int64:
		return "F:" + strconv.FormatFloat(float64(x), 'g', -1, 64)
	case int:
		return "F:" + strconv.FormatFloat(float64(x), 'g', -1, 64)
	case string:
		return "S:" + x
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("O:%v", v)
		}
		return "O:" + string(b)
	}
}
