// Operator: filter_paginate
// Type: Filter
// Description: Keeps only items in the [page*size, page*size+size) range, removes the rest.
//
// Params: (none)
//
// Pagination parameters (page, size) are read from common_input fields
// by position: common_input[0] = page (0-indexed), common_input[1] = size.
//
// Metadata contract (typical usage):
//   CommonInput:  [<page_field>, <size_field>]
//   CommonOutput: []
//   ItemInput:    []
//   ItemOutput:   []
package filter

import (
	"context"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "filter_paginate",
		Type:        pine.OpTypeFilter,
		Description: "Keeps only items in the [page*size, page*size+size) range, removes the rest.",
		Params:      map[string]pine.ParamSpec{},
	}, func() pine.Operator {
		return &PaginateOp{}
	})
}

// PaginateOp removes items outside the requested page window.
type PaginateOp struct {
	pine.MetadataHolder
}

func (o *PaginateOp) Init(params map[string]any) error { return nil }

func (o *PaginateOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	n := in.ItemCount()
	if n == 0 {
		return nil
	}

	page := toInt(in.Common(o.CommonInput[0]))
	size := toInt(in.Common(o.CommonInput[1]))
	if size <= 0 {
		size = 10
	}
	if page < 0 {
		page = 0
	}

	start := page * size
	end := start + size
	if end > n {
		end = n
	}

	for i := 0; i < n; i++ {
		if i < start || i >= end {
			out.RemoveItem(i)
		}
	}
	return nil
}

func toInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case uint64:
		return int(x)
	default:
		return 0
	}
}
