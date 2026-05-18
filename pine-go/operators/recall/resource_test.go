package recall

import (
	"context"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
	"github.com/Liam0205/pineapple/pine-go/pkg/resource"
)

func TestResourceOpBasic(t *testing.T) {
	op := &ResourceOp{}
	if err := op.Init(map[string]any{"resource_name": "candidates"}); err != nil {
		t.Fatal(err)
	}
	op.SetMetadata(nil, nil, nil, []string{"item_id", "item_score"})

	items := []map[string]any{
		{"item_id": "a", "item_score": 0.9},
		{"item_id": "b", "item_score": 0.8},
	}
	ctx := resource.WithResources(context.Background(), resource.NewStatic(map[string]any{
		"candidates": items,
	}))

	in := pine.NewOperatorInput(nil, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(ctx, in, out); err != nil {
		t.Fatal(err)
	}

	added := out.GetAddedItems()
	if len(added) != 2 {
		t.Fatalf("got %d items, want 2", len(added))
	}
	if added[0]["item_id"] != "a" {
		t.Errorf("item[0].item_id = %v, want a", added[0]["item_id"])
	}
	if added[1]["item_score"] != 0.8 {
		t.Errorf("item[1].item_score = %v, want 0.8", added[1]["item_score"])
	}
}

func TestResourceOpAnySlice(t *testing.T) {
	op := &ResourceOp{}
	if err := op.Init(map[string]any{"resource_name": "data"}); err != nil {
		t.Fatal(err)
	}

	items := []any{
		map[string]any{"id": "x"},
		map[string]any{"id": "y"},
	}
	ctx := resource.WithResources(context.Background(), resource.NewStatic(map[string]any{
		"data": items,
	}))

	in := pine.NewOperatorInput(nil, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(ctx, in, out); err != nil {
		t.Fatal(err)
	}

	added := out.GetAddedItems()
	if len(added) != 2 {
		t.Fatalf("got %d items, want 2", len(added))
	}
}

func TestResourceOpNoProvider(t *testing.T) {
	op := &ResourceOp{}
	if err := op.Init(map[string]any{"resource_name": "x"}); err != nil {
		t.Fatal(err)
	}

	in := pine.NewOperatorInput(nil, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err == nil {
		t.Error("expected error when no resource provider")
	}
}

func TestResourceOpBadType(t *testing.T) {
	op := &ResourceOp{}
	if err := op.Init(map[string]any{"resource_name": "bad"}); err != nil {
		t.Fatal(err)
	}

	ctx := resource.WithResources(context.Background(), resource.NewStatic(map[string]any{
		"bad": "not a slice",
	}))
	in := pine.NewOperatorInput(nil, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(ctx, in, out); err == nil {
		t.Error("expected error for non-slice resource")
	}
}

func TestResourceOpIsolatesItems(t *testing.T) {
	op := &ResourceOp{}
	if err := op.Init(map[string]any{"resource_name": "data"}); err != nil {
		t.Fatal(err)
	}

	original := []map[string]any{{"id": "a", "val": 1.0}}
	ctx := resource.WithResources(context.Background(), resource.NewStatic(map[string]any{
		"data": original,
	}))

	in := pine.NewOperatorInput(nil, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(ctx, in, out); err != nil {
		t.Fatal(err)
	}

	added := out.GetAddedItems()
	added[0]["val"] = 999.0
	if original[0]["val"] != 1.0 {
		t.Error("Execute must shallow-copy items to avoid aliasing the resource")
	}
}
