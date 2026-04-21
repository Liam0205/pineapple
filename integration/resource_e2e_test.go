package integration

import (
	"context"
	"fmt"
	"testing"

	pine "github.com/Liam0205/pineapple"
	_ "github.com/Liam0205/pineapple/operators"
	"github.com/Liam0205/pineapple/pkg/resource"
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
	val, ok := rp.Get(o.resourceName)
	if !ok {
		return fmt.Errorf("resource %q not found", o.resourceName)
	}
	out.SetCommon("resource_value", val)
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
