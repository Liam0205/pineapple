package pine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"testing"

	pine "github.com/Liam0205/pineapple"
	"github.com/Liam0205/pineapple/internal/registry"
	"github.com/Liam0205/pineapple/internal/types"
)

// --- test operators ---

type noopTestOp struct{}

func (o *noopTestOp) Init(params map[string]any) error { return nil }
func (o *noopTestOp) Execute(_ context.Context, _ *types.OperatorInput, _ *types.OperatorOutput) error {
	return nil
}

// setFieldOp sets a common field to a configured value.
type setFieldOp struct {
	field string
	value any
}

func (o *setFieldOp) Init(params map[string]any) error {
	o.field = params["field"].(string)
	o.value = params["value"]
	return nil
}
func (o *setFieldOp) Execute(_ context.Context, _ *types.OperatorInput, out *types.OperatorOutput) error {
	out.SetCommon(o.field, o.value)
	return nil
}

// enrichItemOp reads item_score and common multiplier, writes item_adjusted.
type enrichItemOp struct{}

func (o *enrichItemOp) Init(params map[string]any) error { return nil }
func (o *enrichItemOp) Execute(_ context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	mult, _ := in.Common("multiplier").(float64)
	for i := 0; i < in.ItemCount(); i++ {
		score, _ := in.Item(i, "item_score").(float64)
		out.SetItem(i, "item_adjusted", score*mult)
	}
	return nil
}

// recallFixedOp adds fixed items.
type recallFixedOp struct {
	items []map[string]any
}

func (o *recallFixedOp) Init(params map[string]any) error {
	// items come from JSON as []any
	rawItems, ok := params["fixed_items"].([]any)
	if !ok {
		return nil
	}
	for _, ri := range rawItems {
		if m, ok := ri.(map[string]any); ok {
			o.items = append(o.items, m)
		}
	}
	return nil
}
func (o *recallFixedOp) Execute(_ context.Context, _ *types.OperatorInput, out *types.OperatorOutput) error {
	for _, item := range o.items {
		out.AddItem(item)
	}
	return nil
}

// mergeTestOp does simple dedup by item_id, keeping first occurrence.
type mergeTestOp struct{}

func (o *mergeTestOp) Init(params map[string]any) error { return nil }
func (o *mergeTestOp) Execute(_ context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	seen := make(map[any]bool)
	for i := 0; i < in.ItemCount(); i++ {
		id := in.Item(i, "item_id")
		if seen[id] {
			out.RemoveItem(i)
		} else {
			seen[id] = true
		}
	}
	return nil
}

// filterOfflineOp removes items with item_status == "offline".
type filterOfflineOp struct{}

func (o *filterOfflineOp) Init(params map[string]any) error { return nil }
func (o *filterOfflineOp) Execute(_ context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	for i := 0; i < in.ItemCount(); i++ {
		if in.Item(i, "item_status") == "offline" {
			out.RemoveItem(i)
		}
	}
	return nil
}

// sortDescOp sorts items by item_score descending via SetItemOrder.
type sortDescOp struct{}

func (o *sortDescOp) Init(params map[string]any) error { return nil }
func (o *sortDescOp) Execute(_ context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	n := in.ItemCount()
	type kv struct {
		idx   int
		score float64
	}
	items := make([]kv, n)
	for i := 0; i < n; i++ {
		s, _ := in.Item(i, "item_score").(float64)
		items[i] = kv{idx: i, score: s}
	}
	// Simple bubble sort (test only)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if items[j].score > items[i].score {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
	order := make([]int, n)
	for i, kv := range items {
		order[i] = kv.idx
	}
	out.SetItemOrder(order)
	return nil
}

// doubleCommonOp reads a field and writes it * 2.
type doubleCommonOp struct{}

func (o *doubleCommonOp) Init(params map[string]any) error { return nil }
func (o *doubleCommonOp) Execute(_ context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	v, _ := in.Common("x").(float64)
	out.SetCommon("x", v*2)
	return nil
}

// errorTestOp returns an error.
type errorTestOp struct{}

func (o *errorTestOp) Init(params map[string]any) error { return nil }
func (o *errorTestOp) Execute(_ context.Context, _ *types.OperatorInput, _ *types.OperatorOutput) error {
	return fmt.Errorf("deliberate error")
}

// panicTestOp panics.
type panicTestOp struct{}

func (o *panicTestOp) Init(params map[string]any) error { return nil }
func (o *panicTestOp) Execute(_ context.Context, _ *types.OperatorInput, _ *types.OperatorOutput) error {
	panic("deliberate panic")
}

// warningTestOp emits a warning.
type warningTestOp struct{}

func (o *warningTestOp) Init(params map[string]any) error { return nil }
func (o *warningTestOp) Execute(_ context.Context, _ *types.OperatorInput, out *types.OperatorOutput) error {
	out.SetWarning(fmt.Errorf("test warning"))
	out.SetCommon("fallback", "ok")
	return nil
}

// metadataCaptureOp records the CommonInput it received via SetMetadata
// and writes it as a JSON string into a common field for assertion.
type metadataCaptureOp struct {
	types.MetadataHolder
}

func (o *metadataCaptureOp) Init(params map[string]any) error { return nil }
func (o *metadataCaptureOp) Execute(_ context.Context, _ *types.OperatorInput, out *types.OperatorOutput) error {
	out.SetCommon("_captured_common_input", fmt.Sprintf("%v", o.CommonInput))
	return nil
}

// --- register test operators ---

func init() {
	registry.Reset()
	registry.Register(types.OperatorSchema{Name: "noop", Type: types.OpTypeTransform, Description: "No-op test operator."}, func() types.Operator { return &noopTestOp{} })
	registry.Register(types.OperatorSchema{
		Name:        "set_field",
		Type:        types.OpTypeTransform,
		Description: "Sets a common field to a fixed value.",
		Params: map[string]types.ParamSpec{
			"field": {Type: "string", Required: true, Description: "Field name."},
			"value": {Type: "any", Required: true, Description: "Field value."},
		},
	}, func() types.Operator { return &setFieldOp{} })
	registry.Register(types.OperatorSchema{Name: "enrich_item", Type: types.OpTypeTransform, Description: "Enriches items."}, func() types.Operator { return &enrichItemOp{} })
	registry.Register(types.OperatorSchema{Name: "transform_normalize", Type: types.OpTypeTransform, Description: "Whole-item-set normalization."}, func() types.Operator { return &noopTestOp{} })
	registry.Register(types.OperatorSchema{Name: "recall_fixed", Type: types.OpTypeRecall, Description: "Fixed recall."}, func() types.Operator { return &recallFixedOp{} })
	registry.Register(types.OperatorSchema{Name: "merge_dedup", Type: types.OpTypeMerge, Description: "Test dedup."}, func() types.Operator { return &mergeTestOp{} })
	registry.Register(types.OperatorSchema{Name: "filter_offline", Type: types.OpTypeFilter, Description: "Filter offline."}, func() types.Operator { return &filterOfflineOp{} })
	registry.Register(types.OperatorSchema{Name: "sort_desc", Type: types.OpTypeReorder, Description: "Sort descending."}, func() types.Operator { return &sortDescOp{} })
	registry.Register(types.OperatorSchema{Name: "double_common", Type: types.OpTypeTransform, Description: "Doubles common field."}, func() types.Operator { return &doubleCommonOp{} })
	registry.Register(types.OperatorSchema{Name: "error_op", Type: types.OpTypeTransform, Description: "Always errors."}, func() types.Operator { return &errorTestOp{} })
	registry.Register(types.OperatorSchema{Name: "panic_op", Type: types.OpTypeTransform, Description: "Always panics."}, func() types.Operator { return &panicTestOp{} })
	registry.Register(types.OperatorSchema{Name: "warning_op", Type: types.OpTypeTransform, Description: "Emits warning."}, func() types.Operator { return &warningTestOp{} })
	registry.Register(types.OperatorSchema{Name: "metadata_capture", Type: types.OpTypeTransform, Description: "Captures metadata."}, func() types.Operator { return &metadataCaptureOp{} })
}

// --- helper ---

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func makeConfig(operators map[string]any, pipelineMap map[string]any, contract map[string]any) map[string]any {
	return map[string]any{
		"_PINEAPPLE_VERSION": pine.Version,
		"pipeline_config": map[string]any{
			"operators":    operators,
			"pipeline_map": pipelineMap,
		},
		"pipeline_group": map[string]any{
			"main": map[string]any{"pipeline": []string{"stage1"}},
		},
		"flow_contract": contract,
	}
}

// --- integration tests ---

func TestEngineBasicPipeline(t *testing.T) {
	cfg := makeConfig(
		map[string]any{
			"enrich_A1": map[string]any{
				"type_name": "enrich_item",
				"$metadata": map[string]any{
					"common_input":  []string{"multiplier"},
					"common_output": []string{},
					"item_input":    []string{"item_score"},
					"item_output":   []string{"item_adjusted"},
				},
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"enrich_A1"}}},
		map[string]any{
			"common_input": []string{"multiplier"},
			"item_input":   []string{"item_score"},
			"item_output":  []string{"item_adjusted"},
		},
	)

	engine, err := pine.NewEngine(mustJSON(t, cfg))
	if err != nil {
		t.Fatal(err)
	}

	result, err := engine.Execute(context.Background(), &pine.Request{
		Common: map[string]any{"multiplier": 2.0},
		Items: []map[string]any{
			{"item_score": 10.0},
			{"item_score": 20.0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %d", len(result.Items))
	}
	if result.Items[0]["item_adjusted"] != 20.0 {
		t.Errorf("item 0 adjusted = %v", result.Items[0]["item_adjusted"])
	}
	if result.Items[1]["item_adjusted"] != 40.0 {
		t.Errorf("item 1 adjusted = %v", result.Items[1]["item_adjusted"])
	}
}

func TestEngineHazardChain(t *testing.T) {
	// op_a writes x=5, op_b reads x writes x=x*2=10, op_c reads x writes x=x*2=20
	cfg := makeConfig(
		map[string]any{
			"set_x": map[string]any{
				"type_name": "set_field",
				"$metadata": map[string]any{
					"common_input": []string{}, "common_output": []string{"x"},
					"item_input": []string{}, "item_output": []string{},
				},
				"field": "x", "value": 5.0,
			},
			"double1": map[string]any{
				"type_name": "double_common",
				"$metadata": map[string]any{
					"common_input": []string{"x"}, "common_output": []string{"x"},
					"item_input": []string{}, "item_output": []string{},
				},
			},
			"double2": map[string]any{
				"type_name": "double_common",
				"$metadata": map[string]any{
					"common_input": []string{"x"}, "common_output": []string{"x"},
					"item_input": []string{}, "item_output": []string{},
				},
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"set_x", "double1", "double2"}}},
		map[string]any{"common_input": []string{}, "common_output": []string{"x"}},
	)

	engine, err := pine.NewEngine(mustJSON(t, cfg))
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Execute(context.Background(), &pine.Request{Common: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Common["x"] != 20.0 {
		t.Errorf("x = %v, want 20", result.Common["x"])
	}
}

func TestEngineRecallAndMerge(t *testing.T) {
	cfg := makeConfig(
		map[string]any{
			"recall_a": map[string]any{
				"type_name": "recall_fixed",
				"recall":    true,
				"$metadata": map[string]any{
					"common_input": []string{}, "common_output": []string{},
					"item_input": []string{}, "item_output": []string{"item_id", "item_score"},
				},
				"fixed_items": []map[string]any{
					{"item_id": "i1", "item_score": 0.9},
					{"item_id": "i2", "item_score": 0.8},
				},
			},
			"recall_b": map[string]any{
				"type_name": "recall_fixed",
				"recall":    true,
				"$metadata": map[string]any{
					"common_input": []string{}, "common_output": []string{},
					"item_input": []string{}, "item_output": []string{"item_id", "item_score"},
				},
				"fixed_items": []map[string]any{
					{"item_id": "i2", "item_score": 0.7},
					{"item_id": "i3", "item_score": 0.6},
				},
			},
			"merge": map[string]any{
				"type_name": "merge_dedup",
				"$metadata": map[string]any{
					"common_input": []string{}, "common_output": []string{},
					"item_input":  []string{"item_id"},
					"item_output": []string{"item_id"},
				},
				"sources": []string{"recall_a", "recall_b"},
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"recall_a", "recall_b", "merge"}}},
		map[string]any{"common_input": []string{}, "item_output": []string{"item_id", "item_score", "_source"}},
	)

	engine, err := pine.NewEngine(mustJSON(t, cfg))
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Execute(context.Background(), &pine.Request{Common: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}

	// 4 items recalled (2+2), merge deduplicates by item_id -> 3 unique
	if len(result.Items) != 3 {
		t.Errorf("items = %d, want 3 (after dedup)", len(result.Items))
	}

	// Verify _source was injected
	for _, item := range result.Items {
		src := item["_source"]
		if src != "recall_a" && src != "recall_b" {
			t.Errorf("unexpected _source = %v", src)
		}
	}
}

func TestEngineSkipControlFlow(t *testing.T) {
	cfg := makeConfig(
		map[string]any{
			"ctrl": map[string]any{
				"type_name": "set_field",
				"$metadata": map[string]any{
					"common_input": []string{}, "common_output": []string{"_if_1"},
					"item_input": []string{}, "item_output": []string{},
				},
				"for_branch_control": true,
				"field":              "_if_1",
				"value":              true,
			},
			"branch_op": map[string]any{
				"type_name": "set_field",
				"$metadata": map[string]any{
					"common_input": []string{"_if_1"}, "common_output": []string{"branch_ran"},
					"item_input": []string{}, "item_output": []string{},
				},
				"skip":  "_if_1",
				"field": "branch_ran",
				"value": true,
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"ctrl", "branch_op"}}},
		map[string]any{"common_input": []string{}},
	)

	engine, err := pine.NewEngine(mustJSON(t, cfg))
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Execute(context.Background(), &pine.Request{Common: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	// _if_1=true -> branch_op skipped
	if result.Common["branch_ran"] != nil {
		t.Error("branch_op should have been skipped")
	}
}

func TestEngineSkipFieldNotInMetadata(t *testing.T) {
	// Verify that the skip (control-flow) field is filtered out of
	// the operator's MetadataHolder.CommonInput at engine build time.
	cfg := makeConfig(
		map[string]any{
			"ctrl": map[string]any{
				"type_name": "set_field",
				"$metadata": map[string]any{
					"common_input": []string{}, "common_output": []string{"_if_1"},
					"item_input": []string{}, "item_output": []string{},
				},
				"for_branch_control": true,
				"field":              "_if_1",
				"value":              false,
			},
			"branch_op": map[string]any{
				"type_name": "metadata_capture",
				"$metadata": map[string]any{
					"common_input": []string{"_if_1", "user_id"}, "common_output": []string{"_captured_common_input"},
					"item_input": []string{}, "item_output": []string{},
				},
				"skip": "_if_1",
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"ctrl", "branch_op"}}},
		map[string]any{
			"common_input":  []string{"user_id"},
			"common_output": []string{"_captured_common_input"},
		},
	)

	engine, err := pine.NewEngine(mustJSON(t, cfg))
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Execute(context.Background(), &pine.Request{
		Common: map[string]any{"user_id": "u123"},
	})
	if err != nil {
		t.Fatal(err)
	}

	captured, _ := result.Common["_captured_common_input"].(string)
	if captured == "" {
		t.Fatal("branch_op should have executed (skip=_if_1=false)")
	}
	// The captured CommonInput should contain only user_id, not _if_1
	if captured != "[user_id]" {
		t.Errorf("expected CommonInput=[user_id], got %s", captured)
	}
}

func TestEngineFatalError(t *testing.T) {
	cfg := makeConfig(
		map[string]any{
			"bad_op": map[string]any{
				"type_name": "error_op",
				"$metadata": map[string]any{
					"common_input": []string{}, "common_output": []string{"x"},
					"item_input": []string{}, "item_output": []string{},
				},
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"bad_op"}}},
		map[string]any{"common_input": []string{}},
	)

	engine, err := pine.NewEngine(mustJSON(t, cfg))
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Execute(context.Background(), &pine.Request{Common: map[string]any{}})
	if err == nil {
		t.Fatal("expected error")
	}
	var execErr *pine.ExecutionError
	if !errors.As(err, &execErr) {
		t.Errorf("expected ExecutionError, got %T", err)
	}
}

func TestEnginePanic(t *testing.T) {
	cfg := makeConfig(
		map[string]any{
			"panic_op": map[string]any{
				"type_name": "panic_op",
				"$metadata": map[string]any{
					"common_input": []string{}, "common_output": []string{"x"},
					"item_input": []string{}, "item_output": []string{},
				},
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"panic_op"}}},
		map[string]any{"common_input": []string{}},
	)

	engine, err := pine.NewEngine(mustJSON(t, cfg))
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Execute(context.Background(), &pine.Request{Common: map[string]any{}})
	if err == nil {
		t.Fatal("expected panic error")
	}
	var panicErr *pine.PanicError
	if !errors.As(err, &panicErr) {
		t.Errorf("expected PanicError, got %T", err)
	}

	// Engine should still work after panic
	_, err2 := engine.Execute(context.Background(), &pine.Request{Common: map[string]any{}})
	if err2 == nil {
		t.Fatal("expected same panic on second call")
	}
}

func TestEngineWarning(t *testing.T) {
	cfg := makeConfig(
		map[string]any{
			"warn_op": map[string]any{
				"type_name": "warning_op",
				"$metadata": map[string]any{
					"common_input": []string{}, "common_output": []string{"fallback"},
					"item_input": []string{}, "item_output": []string{},
				},
			},
			"after_op": map[string]any{
				"type_name": "noop",
				"$metadata": map[string]any{
					"common_input": []string{"fallback"}, "common_output": []string{},
					"item_input": []string{}, "item_output": []string{},
				},
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"warn_op", "after_op"}}},
		map[string]any{"common_input": []string{}, "common_output": []string{"fallback"}},
	)

	engine, err := pine.NewEngine(mustJSON(t, cfg))
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Execute(context.Background(), &pine.Request{Common: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(result.Warnings))
	}
	if result.Common["fallback"] != "ok" {
		t.Errorf("fallback = %v", result.Common["fallback"])
	}
}

func TestEngineValidation(t *testing.T) {
	cfg := makeConfig(
		map[string]any{
			"op": map[string]any{
				"type_name": "noop",
				"$metadata": map[string]any{
					"common_input": []string{"required_field"}, "common_output": []string{},
					"item_input": []string{}, "item_output": []string{},
				},
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"op"}}},
		map[string]any{"common_input": []string{"required_field"}},
	)

	engine, err := pine.NewEngine(mustJSON(t, cfg))
	if err != nil {
		t.Fatal(err)
	}

	// Missing required field
	_, err = engine.Execute(context.Background(), &pine.Request{Common: map[string]any{}})
	if err == nil {
		t.Fatal("expected validation error")
	}
	var valErr *pine.ValidationError
	if !errors.As(err, &valErr) {
		t.Errorf("expected ValidationError, got %T", err)
	}

	// Nil request
	_, err = engine.Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}

	// Nil common
	_, err = engine.Execute(context.Background(), &pine.Request{})
	if err == nil {
		t.Fatal("expected error for nil common")
	}
}

func TestEngineConcurrentExecute(t *testing.T) {
	cfg := makeConfig(
		map[string]any{
			"enrich": map[string]any{
				"type_name": "enrich_item",
				"$metadata": map[string]any{
					"common_input": []string{"multiplier"}, "common_output": []string{},
					"item_input": []string{"item_score"}, "item_output": []string{"item_adjusted"},
				},
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"enrich"}}},
		map[string]any{
			"common_input": []string{"multiplier"},
			"item_input":   []string{"item_score"},
			"item_output":  []string{"item_adjusted"},
		},
	)

	engine, err := pine.NewEngine(mustJSON(t, cfg))
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(mult float64) {
			defer wg.Done()
			result, err := engine.Execute(context.Background(), &pine.Request{
				Common: map[string]any{"multiplier": mult},
				Items:  []map[string]any{{"item_score": 10.0}},
			})
			if err != nil {
				errs <- err
				return
			}
			expected := 10.0 * mult
			if result.Items[0]["item_adjusted"] != expected {
				errs <- fmt.Errorf("mult=%v: adjusted=%v, want %v",
					mult, result.Items[0]["item_adjusted"], expected)
			}
		}(float64(i + 1))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestEngineFilterAndSort(t *testing.T) {
	// filter reads item_status, sort reads item_score.
	// To enforce filter-before-sort ordering (filter changes item count),
	// both declare item_status in item_input so the DAG creates a dependency.
	cfg := makeConfig(
		map[string]any{
			"filter": map[string]any{
				"type_name": "filter_offline",
				"$metadata": map[string]any{
					"common_input": []string{}, "common_output": []string{},
					"item_input": []string{"item_status"}, "item_output": []string{"item_status"},
				},
			},
			"sort": map[string]any{
				"type_name": "sort_desc",
				"$metadata": map[string]any{
					"common_input": []string{}, "common_output": []string{},
					"item_input": []string{"item_status", "item_score"}, "item_output": []string{},
				},
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"filter", "sort"}}},
		map[string]any{
			"common_input": []string{},
			"item_input":   []string{"item_status", "item_score"},
			"item_output":  []string{"item_id", "item_status", "item_score"},
		},
	)

	engine, err := pine.NewEngine(mustJSON(t, cfg))
	if err != nil {
		t.Fatal(err)
	}

	result, err := engine.Execute(context.Background(), &pine.Request{
		Common: map[string]any{},
		Items: []map[string]any{
			{"item_id": "a", "item_status": "online", "item_score": 1.0},
			{"item_id": "b", "item_status": "offline", "item_score": 5.0},
			{"item_id": "c", "item_status": "online", "item_score": 3.0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// b filtered out, remaining sorted desc by score: c(3.0), a(1.0)
	if len(result.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(result.Items))
	}
	if result.Items[0]["item_id"] != "c" {
		t.Errorf("first item = %v, want c", result.Items[0]["item_id"])
	}
	if result.Items[1]["item_id"] != "a" {
		t.Errorf("second item = %v, want a", result.Items[1]["item_id"])
	}
}

func TestNewEngineInvalidConfig(t *testing.T) {
	_, err := pine.NewEngine([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestDataParallelValidation(t *testing.T) {
	tests := []struct {
		name      string
		typeName  string
		dp        int
		commonOut []string
		wantErr   bool
	}{
		{"Transform dp=3 no common_output", "noop", 3, nil, false},
		{"Transform dp=1 with common_output", "set_field", 1, []string{"x"}, false},
		{"Transform dp=3 with common_output", "noop", 3, []string{"x"}, true},
		{"Recall dp=2", "recall_fixed", 2, nil, true},
		{"Filter dp=2", "filter_offline", 2, nil, true},
		{"Merge dp=2", "merge_dedup", 2, nil, true},
		{"Reorder dp=2", "sort_desc", 2, nil, true},
		{"Whole-item-set transform dp=2", "transform_normalize", 2, nil, true},
		{"dp=0 normalizes to 1", "noop", 0, nil, false},
		{"dp=-1 rejected", "noop", -1, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opCfg := map[string]any{
				"type_name": tt.typeName,
				"$metadata": map[string]any{
					"common_input":  []string{},
					"common_output": tt.commonOut,
					"item_input":    []string{},
					"item_output":   []string{},
				},
			}
			if tt.dp != 0 {
				opCfg["data_parallel"] = tt.dp
			}
			if tt.typeName == "set_field" {
				opCfg["field"] = "x"
				opCfg["value"] = 1.0
			}
			if tt.typeName == "merge_dedup" {
				opCfg["sources"] = []string{}
			}
			cfg := makeConfig(
				map[string]any{"op": opCfg},
				map[string]any{"stage1": map[string]any{"pipeline": []string{"op"}}},
				map[string]any{"common_input": []string{}},
			)
			_, err := pine.NewEngine(mustJSON(t, cfg))
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestLogPrefixFromJSON(t *testing.T) {
	origPrefix := log.Prefix()
	origFlags := log.Flags()
	defer func() { log.SetPrefix(origPrefix); log.SetFlags(origFlags) }()
	cfg := makeConfig(
		map[string]any{
			"op": map[string]any{
				"type_name": "noop",
				"$metadata": map[string]any{
					"common_input": []string{}, "common_output": []string{},
					"item_input": []string{}, "item_output": []string{},
				},
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"op"}}},
		map[string]any{"common_input": []string{}},
	)
	cfg["log_prefix"] = "[test] "
	_, err := pine.NewEngine(mustJSON(t, cfg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := log.Prefix(); got != "[test] " {
		t.Errorf("log.Prefix() = %q, want %q", got, "[test] ")
	}
	wantFlags := log.Ldate | log.Ltime | log.Lshortfile
	if got := log.Flags(); got != wantFlags {
		t.Errorf("log.Flags() = %d, want %d", got, wantFlags)
	}
}

func TestLogPrefixOptionOverridesJSON(t *testing.T) {
	origPrefix := log.Prefix()
	origFlags := log.Flags()
	defer func() { log.SetPrefix(origPrefix); log.SetFlags(origFlags) }()
	cfg := makeConfig(
		map[string]any{
			"op": map[string]any{
				"type_name": "noop",
				"$metadata": map[string]any{
					"common_input": []string{}, "common_output": []string{},
					"item_input": []string{}, "item_output": []string{},
				},
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"op"}}},
		map[string]any{"common_input": []string{}},
	)
	cfg["log_prefix"] = "[json] "
	_, err := pine.NewEngine(mustJSON(t, cfg), pine.WithLogPrefix("[opt] "))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := log.Prefix(); got != "[opt] " {
		t.Errorf("log.Prefix() = %q, want %q", got, "[opt] ")
	}
}

func TestNewEngineUnknownOperator(t *testing.T) {
	cfg := makeConfig(
		map[string]any{
			"op": map[string]any{
				"type_name": "nonexistent_type",
				"$metadata": map[string]any{
					"common_input": []string{}, "common_output": []string{},
					"item_input": []string{}, "item_output": []string{},
				},
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"op"}}},
		map[string]any{"common_input": []string{}},
	)

	_, err := pine.NewEngine(mustJSON(t, cfg))
	if err == nil {
		t.Error("expected error for unknown operator type")
	}
}
