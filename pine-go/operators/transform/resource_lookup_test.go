package transform

import (
	"context"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
	"github.com/Liam0205/pineapple/pine-go/pkg/resource"
)

func TestResourceLookupBasic(t *testing.T) {
	op := &ResourceLookupOp{}
	if err := op.Init(map[string]any{
		"resource_name": "features",
		"lookup_key":    "item_id",
		"output_field":  "item_feature",
	}); err != nil {
		t.Fatal(err)
	}
	op.SetMetadata(nil, nil, []string{"item_id"}, []string{"item_feature"})

	table := map[string]any{
		"a": 1.0,
		"b": 2.0,
		"c": 3.0,
	}
	ctx := resource.WithResources(context.Background(), resource.NewStatic(map[string]any{
		"features": table,
	}))

	items := []map[string]any{
		{"item_id": "a"},
		{"item_id": "b"},
		{"item_id": "missing"},
	}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	if err := op.Execute(ctx, in, out); err != nil {
		t.Fatal(err)
	}

	iw := out.GetItemWrites()
	if iw[0]["item_feature"] != 1.0 {
		t.Errorf("item[0] = %v, want 1.0", iw[0]["item_feature"])
	}
	if iw[1]["item_feature"] != 2.0 {
		t.Errorf("item[1] = %v, want 2.0", iw[1]["item_feature"])
	}
	if _, exists := iw[2]["item_feature"]; exists {
		t.Errorf("item[2] should not have item_feature (missing key, no default)")
	}
}

func TestResourceLookupWithDefault(t *testing.T) {
	op := &ResourceLookupOp{}
	if err := op.Init(map[string]any{
		"resource_name": "features",
		"lookup_key":    "item_id",
		"output_field":  "item_feature",
		"default_value": -1.0,
	}); err != nil {
		t.Fatal(err)
	}
	op.SetMetadata(nil, nil, []string{"item_id"}, []string{"item_feature"})

	ctx := resource.WithResources(context.Background(), resource.NewStatic(map[string]any{
		"features": map[string]any{"a": 10.0},
	}))

	items := []map[string]any{
		{"item_id": "a"},
		{"item_id": "unknown"},
	}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	if err := op.Execute(ctx, in, out); err != nil {
		t.Fatal(err)
	}

	iw := out.GetItemWrites()
	if iw[0]["item_feature"] != 10.0 {
		t.Errorf("item[0] = %v, want 10.0", iw[0]["item_feature"])
	}
	if iw[1]["item_feature"] != -1.0 {
		t.Errorf("item[1] = %v, want -1.0 (default)", iw[1]["item_feature"])
	}
}

func TestResourceLookupNoProvider(t *testing.T) {
	op := &ResourceLookupOp{}
	if err := op.Init(map[string]any{
		"resource_name": "x",
		"lookup_key":    "k",
		"output_field":  "v",
	}); err != nil {
		t.Fatal(err)
	}

	in := pine.NewOperatorInput(nil, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err == nil {
		t.Error("expected error when no resource provider")
	}
}

func TestResourceLookupBadType(t *testing.T) {
	op := &ResourceLookupOp{}
	if err := op.Init(map[string]any{
		"resource_name": "bad",
		"lookup_key":    "k",
		"output_field":  "v",
	}); err != nil {
		t.Fatal(err)
	}

	ctx := resource.WithResources(context.Background(), resource.NewStatic(map[string]any{
		"bad": "not a map",
	}))
	in := pine.NewOperatorInput(nil, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(ctx, in, out); err == nil {
		t.Error("expected error for non-map resource")
	}
}

func TestResourceLookupNumericKey(t *testing.T) {
	op := &ResourceLookupOp{}
	if err := op.Init(map[string]any{
		"resource_name": "features",
		"lookup_key":    "item_id",
		"output_field":  "item_feature",
	}); err != nil {
		t.Fatal(err)
	}
	op.SetMetadata(nil, nil, []string{"item_id"}, []string{"item_feature"})

	table := map[string]any{
		"1": "found_int",
		"2": "found_int2",
	}
	ctx := resource.WithResources(context.Background(), resource.NewStatic(map[string]any{
		"features": table,
	}))

	items := []map[string]any{
		{"item_id": float64(1)},
		{"item_id": float64(2)},
		{"item_id": float64(99)},
	}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	if err := op.Execute(ctx, in, out); err != nil {
		t.Fatal(err)
	}

	iw := out.GetItemWrites()
	if iw[0]["item_feature"] != "found_int" {
		t.Errorf("item[0] = %v, want found_int", iw[0]["item_feature"])
	}
	if iw[1]["item_feature"] != "found_int2" {
		t.Errorf("item[1] = %v, want found_int2", iw[1]["item_feature"])
	}
	if _, exists := iw[2]["item_feature"]; exists {
		t.Errorf("item[2] should not have item_feature (key 99 not in table)")
	}
}

func TestResourceLookupNilKey(t *testing.T) {
	op := &ResourceLookupOp{}
	if err := op.Init(map[string]any{
		"resource_name": "features",
		"lookup_key":    "item_id",
		"output_field":  "item_feature",
		"default_value": "fallback",
	}); err != nil {
		t.Fatal(err)
	}
	op.SetMetadata(nil, nil, []string{"item_id"}, []string{"item_feature"})

	ctx := resource.WithResources(context.Background(), resource.NewStatic(map[string]any{
		"features": map[string]any{"a": "val"},
	}))

	items := []map[string]any{
		{"item_id": nil},
		{},
	}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	if err := op.Execute(ctx, in, out); err != nil {
		t.Fatal(err)
	}

	iw := out.GetItemWrites()
	if iw[0]["item_feature"] != "fallback" {
		t.Errorf("item[0] = %v, want fallback (nil key with default)", iw[0]["item_feature"])
	}
	if iw[1]["item_feature"] != "fallback" {
		t.Errorf("item[1] = %v, want fallback (missing key with default)", iw[1]["item_feature"])
	}
}
