package reorder

import (
	"context"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func TestSortOpInit(t *testing.T) {
	op := &SortOp{}
	err := op.Init(map[string]any{"order": "desc"})
	if err != nil {
		t.Fatal(err)
	}
	op.SetMetadata(nil, nil, []string{"score"}, nil)
	if op.ItemInput[0] != "score" || op.ascending {
		t.Errorf("unexpected config: ItemInput[0]=%s, ascending=%v", op.ItemInput[0], op.ascending)
	}
}

func TestSortOpInitAsc(t *testing.T) {
	op := &SortOp{}
	err := op.Init(map[string]any{"order": "asc"})
	if err != nil {
		t.Fatal(err)
	}
	if !op.ascending {
		t.Error("expected ascending=true")
	}
}

func TestSortOpInitBadOrder(t *testing.T) {
	op := &SortOp{}
	err := op.Init(map[string]any{"order": "random"})
	if err == nil {
		t.Fatal("expected error for unsupported order")
	}
}

func TestSortOpExecuteDesc(t *testing.T) {
	op := &SortOp{}
	op.SetMetadata(nil, nil, []string{"score"}, nil)
	items := []map[string]any{
		{"item_id": "a", "score": 10.0},
		{"item_id": "b", "score": 30.0},
		{"item_id": "c", "score": 20.0},
	}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
	order := out.GetItemOrder()
	// Expected order: b(30) -> c(20) -> a(10) = indices [1, 2, 0]
	if len(order) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(order))
	}
	if order[0] != 1 || order[1] != 2 || order[2] != 0 {
		t.Errorf("unexpected order: %v", order)
	}
}

func TestSortOpExecuteAsc(t *testing.T) {
	op := &SortOp{ascending: true}
	op.SetMetadata(nil, nil, []string{"score"}, nil)
	items := []map[string]any{
		{"item_id": "a", "score": 30.0},
		{"item_id": "b", "score": 10.0},
		{"item_id": "c", "score": 20.0},
	}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	_ = op.Execute(context.Background(), in, out)
	order := out.GetItemOrder()
	// Expected: b(10) -> c(20) -> a(30) = indices [1, 2, 0]
	if order[0] != 1 || order[1] != 2 || order[2] != 0 {
		t.Errorf("unexpected order: %v", order)
	}
}

func TestSortOpExecuteEmpty(t *testing.T) {
	op := &SortOp{}
	op.SetMetadata(nil, nil, []string{"score"}, nil)
	in := pine.NewOperatorInput(nil, nil)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
	if out.GetItemOrder() != nil {
		t.Error("expected nil order for empty items")
	}
}

func TestSortOpExecuteInt64(t *testing.T) {
	op := &SortOp{}
	op.SetMetadata(nil, nil, []string{"score"}, nil)
	items := []map[string]any{
		{"score": int64(5)},
		{"score": int64(15)},
		{"score": int64(10)},
	}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
	order := out.GetItemOrder()
	if order[0] != 1 || order[1] != 2 || order[2] != 0 {
		t.Errorf("unexpected order: %v", order)
	}
}

func TestSortOpExecuteBadType(t *testing.T) {
	op := &SortOp{}
	op.SetMetadata(nil, nil, []string{"score"}, nil)
	items := []map[string]any{{"score": "not_a_number"}}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err == nil {
		t.Fatal("expected error for non-numeric value")
	}
}
