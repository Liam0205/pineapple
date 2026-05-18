package recall

import (
	"context"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func TestStaticOpInit(t *testing.T) {
	op := &StaticOp{}
	err := op.Init(map[string]any{
		"items": []any{
			map[string]any{"item_id": "a", "score": 1.0},
			map[string]any{"item_id": "b", "score": 2.0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(op.items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(op.items))
	}
}

func TestStaticOpInitMissing(t *testing.T) {
	op := &StaticOp{}
	err := op.Init(map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing items")
	}
}

func TestStaticOpInitBadType(t *testing.T) {
	op := &StaticOp{}
	err := op.Init(map[string]any{"items": "not_an_array"})
	if err == nil {
		t.Fatal("expected error for non-array items")
	}
}

func TestStaticOpInitBadItemType(t *testing.T) {
	op := &StaticOp{}
	err := op.Init(map[string]any{"items": []any{"not_a_map"}})
	if err == nil {
		t.Fatal("expected error for non-object item")
	}
}

func TestStaticOpExecute(t *testing.T) {
	op := &StaticOp{
		items: []map[string]any{
			{"item_id": "x", "val": 10},
			{"item_id": "y", "val": 20},
		},
	}
	in := pine.NewOperatorInput(map[string]any{}, nil)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
	added := out.GetAddedItems()
	if len(added) != 2 {
		t.Fatalf("expected 2 added items, got %d", len(added))
	}
	if added[0]["item_id"] != "x" || added[1]["item_id"] != "y" {
		t.Errorf("unexpected items: %v", added)
	}
}

func TestStaticOpExecuteDoesNotShareMemory(t *testing.T) {
	original := map[string]any{"item_id": "z"}
	op := &StaticOp{items: []map[string]any{original}}
	out := pine.NewOperatorOutput()
	_ = op.Execute(context.Background(), pine.NewOperatorInput(nil, nil), out)
	// Mutate the output — should not affect the operator's internal items
	added := out.GetAddedItems()
	added[0]["item_id"] = "mutated"
	if op.items[0]["item_id"] != "z" {
		t.Error("operator internal state was mutated through output")
	}
}

func TestStaticOpExecuteEmpty(t *testing.T) {
	op := &StaticOp{items: nil}
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), pine.NewOperatorInput(nil, nil), out)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.GetAddedItems()) != 0 {
		t.Error("expected 0 added items for empty config")
	}
}
