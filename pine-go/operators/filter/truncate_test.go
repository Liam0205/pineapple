package filter

import (
	"context"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func TestTruncateOpInit(t *testing.T) {
	op := &TruncateOp{}
	err := op.Init(map[string]any{"top_n": int64(5)})
	if err != nil {
		t.Fatal(err)
	}
	if op.topN != 5 {
		t.Errorf("expected topN=5, got %d", op.topN)
	}
}

func TestTruncateOpInitFloat(t *testing.T) {
	op := &TruncateOp{}
	err := op.Init(map[string]any{"top_n": float64(3)})
	if err != nil {
		t.Fatal(err)
	}
	if op.topN != 3 {
		t.Errorf("expected topN=3, got %d", op.topN)
	}
}

func TestTruncateOpInitNegative(t *testing.T) {
	op := &TruncateOp{}
	err := op.Init(map[string]any{"top_n": int64(-1)})
	if err == nil {
		t.Fatal("expected error for negative top_n")
	}
}

func TestTruncateOpExecute(t *testing.T) {
	op := &TruncateOp{topN: 2}
	items := []map[string]any{
		{"item_id": "a"},
		{"item_id": "b"},
		{"item_id": "c"},
		{"item_id": "d"},
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
		t.Error("expected item[2] removed")
	}
	if _, ok := removed[3]; !ok {
		t.Error("expected item[3] removed")
	}
}

func TestTruncateOpExecuteNoTruncation(t *testing.T) {
	op := &TruncateOp{topN: 10}
	items := []map[string]any{{"item_id": "a"}, {"item_id": "b"}}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	_ = op.Execute(context.Background(), in, out)
	if len(out.GetRemovedItems()) != 0 {
		t.Error("expected no removals when top_n > item count")
	}
}

func TestTruncateOpExecuteZero(t *testing.T) {
	op := &TruncateOp{topN: 0}
	items := []map[string]any{{"item_id": "a"}, {"item_id": "b"}}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	_ = op.Execute(context.Background(), in, out)
	removed := out.GetRemovedItems()
	if len(removed) != 2 {
		t.Fatalf("expected 2 removals, got %d", len(removed))
	}
}

func TestTruncateOpExecuteEmpty(t *testing.T) {
	op := &TruncateOp{topN: 5}
	in := pine.NewOperatorInput(nil, nil)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
}
