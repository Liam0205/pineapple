package filter

import (
	"context"
	"testing"

	pine "github.com/Liam0205/pineapple"
)

func TestPaginateOpBasic(t *testing.T) {
	op := &PaginateOp{}
	op.SetMetadata([]string{"page", "size"}, nil, nil, nil)

	items := make([]map[string]any, 10)
	for i := range items {
		items[i] = map[string]any{"id": i}
	}
	in := pine.NewOperatorInput(map[string]any{"page": 1, "size": 3}, items)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	removed := out.GetRemovedItems()
	// page=1, size=3 → keep items [3,4,5], remove [0,1,2,6,7,8,9]
	expected := map[int]bool{0: true, 1: true, 2: true, 6: true, 7: true, 8: true, 9: true}
	if len(removed) != len(expected) {
		t.Fatalf("removed %d items, want %d", len(removed), len(expected))
	}
	for idx := range removed {
		if !expected[idx] {
			t.Errorf("unexpected removal of item %d", idx)
		}
	}
}

func TestPaginateOpFirstPage(t *testing.T) {
	op := &PaginateOp{}
	op.SetMetadata([]string{"page", "size"}, nil, nil, nil)

	items := make([]map[string]any, 5)
	for i := range items {
		items[i] = map[string]any{"id": i}
	}
	in := pine.NewOperatorInput(map[string]any{"page": 0, "size": 3}, items)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	removed := out.GetRemovedItems()
	// page=0, size=3 → keep [0,1,2], remove [3,4]
	if len(removed) != 2 {
		t.Fatalf("removed %d items, want 2", len(removed))
	}
}

func TestPaginateOpBeyondEnd(t *testing.T) {
	op := &PaginateOp{}
	op.SetMetadata([]string{"page", "size"}, nil, nil, nil)

	items := make([]map[string]any, 3)
	for i := range items {
		items[i] = map[string]any{"id": i}
	}
	in := pine.NewOperatorInput(map[string]any{"page": 10, "size": 5}, items)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	removed := out.GetRemovedItems()
	// page=10, size=5 → start=50, all items removed
	if len(removed) != 3 {
		t.Fatalf("removed %d items, want 3", len(removed))
	}
}

func TestPaginateOpEmpty(t *testing.T) {
	op := &PaginateOp{}
	op.SetMetadata([]string{"page", "size"}, nil, nil, nil)

	in := pine.NewOperatorInput(map[string]any{"page": 0, "size": 10}, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
}

func TestPaginateOpFloat64Params(t *testing.T) {
	op := &PaginateOp{}
	op.SetMetadata([]string{"page", "size"}, nil, nil, nil)

	items := make([]map[string]any, 5)
	for i := range items {
		items[i] = map[string]any{"id": i}
	}
	// JSON numbers decode as float64
	in := pine.NewOperatorInput(map[string]any{"page": float64(0), "size": float64(2)}, items)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	removed := out.GetRemovedItems()
	// keep [0,1], remove [2,3,4]
	if len(removed) != 3 {
		t.Fatalf("removed %d items, want 3", len(removed))
	}
}
