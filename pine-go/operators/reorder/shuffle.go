// Operator: reorder_shuffle_by_salt
// Type: Reorder
// Description: Deterministic hash-based shuffle using a caller-provided salt.
//
// Params: (none)
//
// The salt is built by concatenating common_input field values with "|".
// Each item is hashed using the first item_input field to produce a
// deterministic ordering. Same salt + same items → same shuffle.
//
// Metadata contract (typical usage):
//
//	CommonInput:  [<salt_fields...>]
//	CommonOutput: []
//	ItemInput:    [<item_key_field>]
//	ItemOutput:   []
package reorder

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strconv"
	"strings"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "reorder_shuffle_by_salt",
		Type:        pine.OpTypeReorder,
		Description: "Deterministic hash-based shuffle using a caller-provided salt.",
		Params:      map[string]pine.ParamSpec{},
	}, func() pine.Operator {
		return &ShuffleBySaltOp{}
	})
}

// ShuffleBySaltOp reorders items via deterministic FNV hashing.
type ShuffleBySaltOp struct {
	pine.MetadataHolder
	pine.ConsumesRowSetMarker
	pine.MutatesRowSetMarker
}

func (o *ShuffleBySaltOp) Init(params map[string]any) error { return nil }

func (o *ShuffleBySaltOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	n := in.ItemCount()
	if n == 0 {
		return nil
	}

	// Build salt prefix from common_input fields.
	var sb strings.Builder
	for i, field := range o.CommonInput {
		if i > 0 {
			sb.WriteByte('|')
		}
		sb.WriteString(anyToString(in.Common(field)))
	}
	sb.WriteByte('|')
	saltPrefix := sb.String()

	// Hash each item using the first item_input field.
	itemField := o.ItemInput[0]
	type ranked struct {
		idx int
		r   float64
		id  uint64
	}
	items := make([]ranked, n)
	for i := 0; i < n; i++ {
		itemVal := anyToString(in.Item(i, itemField))
		key := saltPrefix + itemVal
		items[i] = ranked{idx: i, r: hashToUnitInterval(key), id: parseUint64(itemVal)}
	}

	sort.Slice(items, func(a, b int) bool {
		if items[a].r != items[b].r {
			return items[a].r < items[b].r
		}
		if items[a].id != items[b].id {
			return items[a].id < items[b].id
		}
		return items[a].idx < items[b].idx
	})

	order := make([]int, n)
	for i, item := range items {
		order[i] = item.idx
	}
	out.SetItemOrder(order)
	return nil
}

func hashToUnitInterval(s string) float64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return float64(h.Sum64()) / (float64(math.MaxUint64) + 1.0)
}

func anyToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int64:
		return fmt.Sprintf("%g", float64(x))
	case uint64:
		return fmt.Sprintf("%g", float64(x))
	case float64:
		return fmt.Sprintf("%g", x)
	case int:
		return fmt.Sprintf("%g", float64(x))
	case nil:
		return ""
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

func parseUint64(s string) uint64 {
	n, _ := strconv.ParseUint(s, 10, 64)
	return n
}
