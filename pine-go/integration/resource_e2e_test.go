package integration

import (
	"context"
	"fmt"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
	_ "github.com/Liam0205/pineapple/pine-go/operators"
	"github.com/Liam0205/pineapple/pine-go/pkg/resource"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "transform_test_resource_read",
		Type:        pine.OpTypeTransform,
		Description: "Test-only operator that reads a named resource and writes its value to a common output field.",
		Params: map[string]pine.ParamSpec{
			"resource_name": {Type: "string", Required: true, Description: "Name of the resource to read."},
		},
	}, func() pine.Operator {
		return &testResourceReadOp{}
	})
}

type testResourceReadOp struct {
	resourceName string
}

func (o *testResourceReadOp) Init(params map[string]any) error {
	o.resourceName = params["resource_name"].(string)
	return nil
}

func (o *testResourceReadOp) Execute(ctx context.Context, _ *pine.OperatorInput, out *pine.OperatorOutput) error {
	rp := resource.FromContext(ctx)
	if rp == nil {
		return fmt.Errorf("no resource provider in context")
	}
	h, ok := rp.Get(o.resourceName)
	if !ok {
		return fmt.Errorf("resource %q not found", o.resourceName)
	}
	defer h.Release()
	out.SetCommon("resource_value", h.Value())
	return nil
}

func TestResourceE2E(t *testing.T) {
	cfg := loadConfig(t, "../testdata/e2e_resource_pipeline.json")
	engine, err := pine.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}

	provider := resource.NewStatic(map[string]any{
		"my_resource": "hello_from_resource",
	})

	ctx := resource.WithResources(context.Background(), provider)
	result, err := engine.Execute(ctx, &pine.Request{
		Common: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, ok := result.Common["resource_value"]
	if !ok {
		t.Fatal("resource_value not in result common")
	}
	if got != "hello_from_resource" {
		t.Errorf("resource_value = %v, want %q", got, "hello_from_resource")
	}
}

func TestRecallResourceE2E(t *testing.T) {
	cfg := loadConfig(t, "../testdata/e2e_recall_resource.json")
	engine, err := pine.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}

	candidates := []map[string]any{
		{"item_id": "x", "item_score": 0.95},
		{"item_id": "y", "item_score": 0.80},
	}
	ctx := resource.WithResources(context.Background(), resource.NewStatic(map[string]any{
		"candidates": candidates,
	}))
	result, err := engine.Execute(ctx, &pine.Request{Common: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(result.Items))
	}
	if result.Items[0]["item_id"] != "x" {
		t.Errorf("items[0].item_id = %v, want x", result.Items[0]["item_id"])
	}
	if result.Items[1]["item_score"] != 0.80 {
		t.Errorf("items[1].item_score = %v, want 0.80", result.Items[1]["item_score"])
	}
}

func TestResourceLookupE2E(t *testing.T) {
	cfg := loadConfig(t, "../testdata/e2e_resource_lookup.json")
	engine, err := pine.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}

	features := map[string]any{
		"a": 100.0,
		"b": 200.0,
	}
	ctx := resource.WithResources(context.Background(), resource.NewStatic(map[string]any{
		"features": features,
	}))
	result, err := engine.Execute(ctx, &pine.Request{Common: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Items) != 3 {
		t.Fatalf("got %d items, want 3", len(result.Items))
	}
	if result.Items[0]["item_feature"] != 100.0 {
		t.Errorf("items[0].item_feature = %v, want 100.0", result.Items[0]["item_feature"])
	}
	if result.Items[1]["item_feature"] != 200.0 {
		t.Errorf("items[1].item_feature = %v, want 200.0", result.Items[1]["item_feature"])
	}
	if result.Items[2]["item_feature"] != -1.0 {
		t.Errorf("items[2].item_feature = %v, want -1.0 (default)", result.Items[2]["item_feature"])
	}
}
