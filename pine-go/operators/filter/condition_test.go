package filter

import (
	"context"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func TestConditionOpInit(t *testing.T) {
	op := &ConditionOp{}
	err := op.Init(map[string]any{"value": "offline"})
	if err != nil {
		t.Fatal(err)
	}
	op.SetMetadata(nil, nil, []string{"status"}, nil)
	if op.ItemInput[0] != "status" {
		t.Errorf("expected ItemInput[0]=status, got %s", op.ItemInput[0])
	}
}

func TestConditionOpExecute(t *testing.T) {
	op := &ConditionOp{}
	op.SetMetadata(nil, nil, []string{"status"}, nil)
	op.value = "offline"
	items := []map[string]any{
		{"item_id": "a", "status": "online"},
		{"item_id": "b", "status": "offline"},
		{"item_id": "c", "status": "online"},
		{"item_id": "d", "status": "offline"},
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
	if _, ok := removed[1]; !ok {
		t.Error("expected item[1] removed")
	}
	if _, ok := removed[3]; !ok {
		t.Error("expected item[3] removed")
	}
}

func TestConditionOpExecuteNoMatch(t *testing.T) {
	op := &ConditionOp{}
	op.SetMetadata(nil, nil, []string{"status"}, nil)
	op.value = "offline"
	items := []map[string]any{
		{"item_id": "a", "status": "online"},
	}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	_ = op.Execute(context.Background(), in, out)
	if len(out.GetRemovedItems()) != 0 {
		t.Error("expected no removals")
	}
}

func TestConditionOpExecuteNumericValue(t *testing.T) {
	op := &ConditionOp{}
	op.SetMetadata(nil, nil, []string{"flag"}, nil)
	op.value = float64(0)
	items := []map[string]any{
		{"flag": float64(0)},
		{"flag": float64(1)},
	}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	_ = op.Execute(context.Background(), in, out)
	removed := out.GetRemovedItems()
	if len(removed) != 1 {
		t.Fatalf("expected 1 removal, got %d", len(removed))
	}
	if _, ok := removed[0]; !ok {
		t.Error("expected item[0] removed")
	}
}

func TestConditionOpExecuteEmpty(t *testing.T) {
	op := &ConditionOp{}
	op.SetMetadata(nil, nil, []string{"status"}, nil)
	op.value = "offline"
	in := pine.NewOperatorInput(nil, nil)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
}
