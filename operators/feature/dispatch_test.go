package feature

import (
	"context"
	"testing"

	pine "github.com/Liam0205/pineapple"
)

func TestDispatchOpInit(t *testing.T) {
	op := &DispatchOp{}
	err := op.Init(map[string]any{"common_field": "scene", "item_field": "item_scene"})
	if err != nil {
		t.Fatal(err)
	}
	if op.commonField != "scene" || op.itemField != "item_scene" {
		t.Errorf("unexpected config: %+v", op)
	}
}

func TestDispatchOpExecute(t *testing.T) {
	op := &DispatchOp{commonField: "scene", itemField: "item_scene"}
	items := []map[string]any{
		{"item_id": "a"},
		{"item_id": "b"},
		{"item_id": "c"},
	}
	in := pine.NewOperatorInput(map[string]any{"scene": "homepage"}, items)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
	writes := out.GetItemWrites()
	for i := 0; i < 3; i++ {
		if writes[i]["item_scene"] != "homepage" {
			t.Errorf("item[%d] item_scene = %v", i, writes[i]["item_scene"])
		}
	}
}

func TestDispatchOpExecuteEmpty(t *testing.T) {
	op := &DispatchOp{commonField: "scene", itemField: "item_scene"}
	in := pine.NewOperatorInput(map[string]any{"scene": "test"}, nil)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.GetItemWrites()) != 0 {
		t.Error("expected no writes for empty items")
	}
}

func TestDispatchOpExecuteNilCommon(t *testing.T) {
	op := &DispatchOp{commonField: "missing", itemField: "item_missing"}
	items := []map[string]any{{"item_id": "a"}}
	in := pine.NewOperatorInput(map[string]any{}, items)
	out := pine.NewOperatorOutput()
	_ = op.Execute(context.Background(), in, out)
	writes := out.GetItemWrites()
	if writes[0]["item_missing"] != nil {
		t.Errorf("expected nil for missing common field, got %v", writes[0]["item_missing"])
	}
}
