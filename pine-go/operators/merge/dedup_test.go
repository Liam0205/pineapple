package merge

import (
	"context"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func TestDedupOpInit(t *testing.T) {
	op := &DedupOp{}
	err := op.Init(map[string]any{"strategy": "first"})
	if err != nil {
		t.Fatal(err)
	}
	op.SetMetadata(nil, nil, []string{"item_id", "_source"}, nil)
	if op.ItemInput[0] != "item_id" || op.strategy != "first" {
		t.Errorf("unexpected config: %+v", op)
	}
}

func TestDedupOpInitBadStrategy(t *testing.T) {
	op := &DedupOp{}
	err := op.Init(map[string]any{"strategy": "unknown"})
	if err == nil {
		t.Fatal("expected error for unsupported strategy")
	}
}

func TestDedupOpExecute(t *testing.T) {
	op := &DedupOp{}
	op.SetMetadata(nil, nil, []string{"item_id"}, nil)
	op.strategy = "first"
	items := []map[string]any{
		{"item_id": "a", "score": 1.0},
		{"item_id": "b", "score": 2.0},
		{"item_id": "a", "score": 3.0}, // duplicate
		{"item_id": "c", "score": 4.0},
		{"item_id": "b", "score": 5.0}, // duplicate
	}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
	removed := out.GetRemovedItems()
	if len(removed) != 2 {
		t.Fatalf("expected 2 removals, got %d", len(removed))
	}
	if _, ok := removed[2]; !ok {
		t.Error("expected item[2] to be removed")
	}
	if _, ok := removed[4]; !ok {
		t.Error("expected item[4] to be removed")
	}
}

func TestDedupOpExecuteNoDuplicates(t *testing.T) {
	op := &DedupOp{}
	op.SetMetadata(nil, nil, []string{"item_id"}, nil)
	op.strategy = "first"
	items := []map[string]any{
		{"item_id": "a"},
		{"item_id": "b"},
	}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	_ = op.Execute(context.Background(), in, out)
	if len(out.GetRemovedItems()) != 0 {
		t.Error("expected no removals")
	}
}

func TestDedupOpExecuteEmpty(t *testing.T) {
	op := &DedupOp{}
	op.SetMetadata(nil, nil, []string{"item_id"}, nil)
	op.strategy = "first"
	in := pine.NewOperatorInput(nil, nil)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
}
