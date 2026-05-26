package runtime

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Liam0205/pineapple/pine-go/internal/config"
	"github.com/Liam0205/pineapple/pine-go/internal/dataframe"
	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

// --- test helpers for data parallelism ---

// setItemFieldOp writes a fixed value to each item's given field.
type setItemFieldOp struct {
	field string
	value any
}

func (o *setItemFieldOp) Init(params map[string]any) error { return nil }
func (o *setItemFieldOp) Execute(_ context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	for i := 0; i < in.ItemCount(); i++ {
		out.SetItem(i, o.field, o.value)
	}
	return nil
}

// doubleItemOp reads a numeric item field and writes it *2 to an output field.
type doubleItemOp struct {
	readField  string
	writeField string
}

func (o *doubleItemOp) Init(params map[string]any) error { return nil }
func (o *doubleItemOp) Execute(_ context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	for i := 0; i < in.ItemCount(); i++ {
		v, ok := in.Item(i, o.readField).(float64)
		if !ok {
			return fmt.Errorf("item %d: expected float64 for %s, got %T", i, o.readField, in.Item(i, o.readField))
		}
		out.SetItem(i, o.writeField, v*2)
	}
	return nil
}

// shardWarningOp sets a warning containing the item count (for testing warning merge order).
type shardWarningOp struct{}

func (o *shardWarningOp) Init(params map[string]any) error { return nil }
func (o *shardWarningOp) Execute(_ context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	for i := 0; i < in.ItemCount(); i++ {
		out.SetItem(i, "seen", true)
	}
	if in.ItemCount() > 0 {
		out.SetWarning(fmt.Errorf("shard with %d items", in.ItemCount()))
	}
	return nil
}

// --- splitInput tests ---

func TestSplitInput_EvenSplit(t *testing.T) {
	items := make([]map[string]any, 10)
	for i := range items {
		items[i] = map[string]any{"id": i}
	}
	input := types.NewOperatorInput(map[string]any{"ctx": "v"}, items)

	parts, offsets := splitInput(input, 3)
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(parts))
	}
	// 10 / 3 = 3 rem 1: sizes 4,3,3
	wantSizes := []int{4, 3, 3}
	wantOffsets := []int{0, 4, 7}
	for i, p := range parts {
		if p.ItemCount() != wantSizes[i] {
			t.Errorf("part %d: size=%d, want %d", i, p.ItemCount(), wantSizes[i])
		}
		if offsets[i] != wantOffsets[i] {
			t.Errorf("part %d: offset=%d, want %d", i, offsets[i], wantOffsets[i])
		}
		if p.Common("ctx") != "v" {
			t.Errorf("part %d: common not shared", i)
		}
	}
}

func TestSplitInput_MoreShardsThanItems(t *testing.T) {
	items := []map[string]any{{"a": 1}, {"a": 2}, {"a": 3}}
	input := types.NewOperatorInput(map[string]any{}, items)

	parts, offsets := splitInput(input, 10)
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts (clamped), got %d", len(parts))
	}
	for i, p := range parts {
		if p.ItemCount() != 1 {
			t.Errorf("part %d: size=%d, want 1", i, p.ItemCount())
		}
		if offsets[i] != i {
			t.Errorf("part %d: offset=%d, want %d", i, offsets[i], i)
		}
	}
}

func TestSplitInput_ZeroItems(t *testing.T) {
	input := types.NewOperatorInput(map[string]any{"k": "v"}, nil)

	parts, offsets := splitInput(input, 4)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part for 0 items, got %d", len(parts))
	}
	if offsets[0] != 0 {
		t.Errorf("offset=%d, want 0", offsets[0])
	}
}

func TestSplitInput_SingleShard(t *testing.T) {
	items := []map[string]any{{"a": 1}, {"a": 2}}
	input := types.NewOperatorInput(map[string]any{}, items)

	parts, offsets := splitInput(input, 1)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0] != input {
		t.Error("single shard should return original input")
	}
	if offsets[0] != 0 {
		t.Errorf("offset=%d, want 0", offsets[0])
	}
}

// --- mergeOutputs tests ---

func TestMergeOutputs_ItemWritesOffset(t *testing.T) {
	out0 := types.NewOperatorOutput()
	out0.SetItem(0, "score", 1.0)
	out0.SetItem(1, "score", 2.0)

	out1 := types.NewOperatorOutput()
	out1.SetItem(0, "score", 3.0)
	out1.SetItem(1, "score", 4.0)

	merged := mergeOutputs("op", []*types.OperatorOutput{out0, out1}, []int{0, 2})

	iw := merged.GetItemWrites()
	for absIdx, want := range map[int]float64{0: 1.0, 1: 2.0, 2: 3.0, 3: 4.0} {
		if iw[absIdx]["score"] != want {
			t.Errorf("itemWrites[%d][score]=%v, want %v", absIdx, iw[absIdx]["score"], want)
		}
	}
}

func TestMergeOutputs_WarningOrder(t *testing.T) {
	out0 := types.NewOperatorOutput()
	// no warning

	out1 := types.NewOperatorOutput()
	out1.SetWarning(fmt.Errorf("warn1"))

	out2 := types.NewOperatorOutput()
	out2.SetWarning(fmt.Errorf("warn2"))

	merged := mergeOutputs("op", []*types.OperatorOutput{out0, out1, out2}, []int{0, 0, 0})
	w := merged.GetWarning()
	if w == nil || w.Error() != "warn1" {
		t.Errorf("warning=%v, want warn1", w)
	}
}

func TestMergeOutputs_NilOutputSkipped(t *testing.T) {
	out0 := types.NewOperatorOutput()
	out0.SetItem(0, "x", 1)

	merged := mergeOutputs("op", []*types.OperatorOutput{out0, nil}, []int{0, 1})
	iw := merged.GetItemWrites()
	if iw[0]["x"] != 1 {
		t.Errorf("itemWrites[0][x]=%v, want 1", iw[0]["x"])
	}
}

// --- parallelExecute tests ---

func TestParallelExecute_TransformSuccess(t *testing.T) {
	items := make([]map[string]any, 10)
	for i := range items {
		items[i] = map[string]any{"val": float64(i)}
	}

	cop := &CompiledOperator{
		Name:     "double",
		Instance: &doubleItemOp{readField: "val", writeField: "doubled"},
		Config: config.OperatorConfig{
			TypeName:     "transform",
			OperatorType: "Transform",
			DataParallel: 3,
			Meta:         config.Metadata{ItemInput: []string{"val"}, ItemOutput: []string{"doubled"}},
		},
	}

	input := types.NewOperatorInput(map[string]any{}, items)
	output, err := parallelExecute(context.Background(), cop, input)
	if err != nil {
		t.Fatalf("parallelExecute: %v", err)
	}

	iw := output.GetItemWrites()
	for i := 0; i < 10; i++ {
		want := float64(i) * 2
		if iw[i]["doubled"] != want {
			t.Errorf("item %d: doubled=%v, want %v", i, iw[i]["doubled"], want)
		}
	}
}

func TestParallelExecute_ZeroItems(t *testing.T) {
	cop := &CompiledOperator{
		Name:     "noop",
		Instance: &setItemFieldOp{field: "x", value: 1},
		Config: config.OperatorConfig{
			TypeName:     "transform",
			OperatorType: "Transform",
			DataParallel: 4,
			Meta:         config.Metadata{ItemOutput: []string{"x"}},
		},
	}

	input := types.NewOperatorInput(map[string]any{}, nil)
	output, err := parallelExecute(context.Background(), cop, input)
	if err != nil {
		t.Fatalf("parallelExecute: %v", err)
	}
	if len(output.GetItemWrites()) != 0 {
		t.Errorf("expected no item writes for 0 items, got %d", len(output.GetItemWrites()))
	}
}

func TestParallelExecute_ShardError(t *testing.T) {
	cop := &CompiledOperator{
		Name:     "err_op",
		Instance: &errorOp{msg: "shard boom"},
		Config: config.OperatorConfig{
			TypeName:     "transform",
			OperatorType: "Transform",
			DataParallel: 3,
			Meta:         config.Metadata{ItemInput: []string{"x"}},
		},
	}

	items := make([]map[string]any, 6)
	for i := range items {
		items[i] = map[string]any{"x": i}
	}
	input := types.NewOperatorInput(map[string]any{}, items)
	_, err := parallelExecute(context.Background(), cop, input)
	if err == nil {
		t.Fatal("expected error from shard")
	}
}

func TestParallelExecute_ShardPanic(t *testing.T) {
	cop := &CompiledOperator{
		Name:     "panic_op",
		Instance: &panicOp{},
		Config: config.OperatorConfig{
			TypeName:     "transform",
			OperatorType: "Transform",
			DataParallel: 2,
			Meta:         config.Metadata{ItemInput: []string{"x"}},
		},
	}

	items := []map[string]any{{"x": 1}, {"x": 2}}
	input := types.NewOperatorInput(map[string]any{}, items)
	_, err := parallelExecute(context.Background(), cop, input)
	if err == nil {
		t.Fatal("expected panic error")
	}
	var panicErr *types.PanicError
	if !errors.As(err, &panicErr) {
		t.Errorf("expected PanicError, got %T", err)
	}
}

// --- scheduler integration test with data_parallel ---

func TestRunDataParallelTransform(t *testing.T) {
	items := make([]map[string]any, 10)
	for i := range items {
		items[i] = map[string]any{"val": float64(i)}
	}

	cop := &CompiledOperator{
		Name:     "double",
		Instance: &doubleItemOp{readField: "val", writeField: "doubled"},
		Config: config.OperatorConfig{
			TypeName:     "transform",
			OperatorType: "Transform",
			DataParallel: 3,
			Meta:         config.Metadata{ItemInput: []string{"val"}, ItemOutput: []string{"doubled"}},
			InputSpec:    config.ComputeInputFieldSpec(config.Metadata{ItemInput: []string{"val"}, ItemOutput: []string{"doubled"}}, nil, nil, nil),
		},
	}

	plan := buildPlan(t, []string{"double"}, map[string]*CompiledOperator{
		"double": cop,
	})
	frame := dataframe.New(map[string]any{}, items)
	_, traces, err := Run(context.Background(), plan, frame, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		want := float64(i) * 2
		if frame.Item(i, "doubled") != want {
			t.Errorf("item %d: doubled=%v, want %v", i, frame.Item(i, "doubled"), want)
		}
	}
	if len(traces) != 1 || traces[0].Name != "double" {
		t.Errorf("traces: %v", traces)
	}
}

func TestRunDataParallelWarning(t *testing.T) {
	items := make([]map[string]any, 6)
	for i := range items {
		items[i] = map[string]any{"id": i}
	}

	cop := &CompiledOperator{
		Name:     "warn_op",
		Instance: &shardWarningOp{},
		Config: config.OperatorConfig{
			TypeName:     "transform",
			OperatorType: "Transform",
			DataParallel: 3,
			Meta:         config.Metadata{ItemInput: []string{"id"}, ItemOutput: []string{"seen"}},
			InputSpec:    config.ComputeInputFieldSpec(config.Metadata{ItemInput: []string{"id"}, ItemOutput: []string{"seen"}}, nil, nil, nil),
		},
	}

	plan := buildPlan(t, []string{"warn_op"}, map[string]*CompiledOperator{
		"warn_op": cop,
	})
	frame := dataframe.New(map[string]any{}, items)
	warnings, _, err := Run(context.Background(), plan, frame, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	// All items should have been processed
	for i := 0; i < 6; i++ {
		if frame.Item(i, "seen") != true {
			t.Errorf("item %d not seen", i)
		}
	}
}

func TestRunDataParallelZeroItems(t *testing.T) {
	cop := &CompiledOperator{
		Name:     "noop",
		Instance: &setItemFieldOp{field: "x", value: 1},
		Config: config.OperatorConfig{
			TypeName:     "transform",
			OperatorType: "Transform",
			DataParallel: 4,
			Meta:         config.Metadata{ItemOutput: []string{"x"}},
			InputSpec:    &config.InputFieldSpec{},
		},
	}

	plan := buildPlan(t, []string{"noop"}, map[string]*CompiledOperator{
		"noop": cop,
	})
	frame := dataframe.New(map[string]any{}, nil)
	_, _, err := Run(context.Background(), plan, frame, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if frame.ItemCount() != 0 {
		t.Errorf("expected 0 items, got %d", frame.ItemCount())
	}
}

func TestDataParallelEquivalence(t *testing.T) {
	const itemCount = 100

	items := make([]map[string]any, itemCount)
	for i := range items {
		items[i] = map[string]any{"val": float64(i)}
	}

	baseCop := &CompiledOperator{
		Name:     "double",
		Instance: &doubleItemOp{readField: "val", writeField: "doubled"},
		Config: config.OperatorConfig{
			TypeName:     "transform",
			OperatorType: "Transform",
			DataParallel: 1,
			Meta:         config.Metadata{ItemInput: []string{"val"}, ItemOutput: []string{"doubled"}},
			InputSpec:    config.ComputeInputFieldSpec(config.Metadata{ItemInput: []string{"val"}, ItemOutput: []string{"doubled"}}, nil, nil, nil),
		},
	}

	plan1 := buildPlan(t, []string{"double"}, map[string]*CompiledOperator{"double": baseCop})
	frame1 := dataframe.New(map[string]any{}, items)
	warnings1, _, err := Run(context.Background(), plan1, frame1, nil, nil)
	if err != nil {
		t.Fatalf("baseline (dp=1): %v", err)
	}

	for _, shards := range []int{2, 3, 4, 7} {
		t.Run(fmt.Sprintf("shards=%d", shards), func(t *testing.T) {
			dpItems := make([]map[string]any, itemCount)
			for i := range dpItems {
				dpItems[i] = map[string]any{"val": float64(i)}
			}

			dpCop := &CompiledOperator{
				Name:     "double",
				Instance: &doubleItemOp{readField: "val", writeField: "doubled"},
				Config: config.OperatorConfig{
					TypeName:     "transform",
					OperatorType: "Transform",
					DataParallel: shards,
					Meta:         config.Metadata{ItemInput: []string{"val"}, ItemOutput: []string{"doubled"}},
					InputSpec:    config.ComputeInputFieldSpec(config.Metadata{ItemInput: []string{"val"}, ItemOutput: []string{"doubled"}}, nil, nil, nil),
				},
			}

			plan := buildPlan(t, []string{"double"}, map[string]*CompiledOperator{"double": dpCop})
			frame := dataframe.New(map[string]any{}, dpItems)
			warnings, _, err := Run(context.Background(), plan, frame, nil, nil)
			if err != nil {
				t.Fatalf("dp=%d: %v", shards, err)
			}

			if len(warnings) != len(warnings1) {
				t.Errorf("warning count: dp=%d got %d, baseline got %d", shards, len(warnings), len(warnings1))
			}

			for i := 0; i < itemCount; i++ {
				got := frame.Item(i, "doubled")
				want := frame1.Item(i, "doubled")
				if got != want {
					t.Errorf("item %d: dp=%d got %v, baseline got %v", i, shards, got, want)
				}
			}
		})
	}
}

func FuzzDataParallelEquivalence(f *testing.F) {
	f.Add(10, 3, 0)
	f.Add(0, 8, 42)
	f.Add(63, 16, -7)

	f.Fuzz(func(t *testing.T, rawItemCount, rawShards, seed int) {
		itemCount := boundedAbs(rawItemCount, 128)
		shards := boundedAbs(rawShards, 32) + 1

		items := make([]map[string]any, itemCount)
		for i := range items {
			items[i] = map[string]any{"val": float64(seed + i*17)}
		}

		baseFrame := dataframe.New(map[string]any{}, cloneRuntimeItems(items))
		basePlan := buildPlan(t, []string{"double"}, map[string]*CompiledOperator{
			"double": {
				Name:     "double",
				Instance: &doubleItemOp{readField: "val", writeField: "doubled"},
				Config: config.OperatorConfig{
					TypeName:     "transform",
					OperatorType: "Transform",
					DataParallel: 1,
					Meta:         config.Metadata{ItemInput: []string{"val"}, ItemOutput: []string{"doubled"}},
					InputSpec:    config.ComputeInputFieldSpec(config.Metadata{ItemInput: []string{"val"}, ItemOutput: []string{"doubled"}}, nil, nil, nil),
				},
			},
		})
		if _, _, err := Run(context.Background(), basePlan, baseFrame, nil, nil); err != nil {
			t.Fatalf("baseline: %v", err)
		}

		dpFrame := dataframe.New(map[string]any{}, cloneRuntimeItems(items))
		dpPlan := buildPlan(t, []string{"double"}, map[string]*CompiledOperator{
			"double": {
				Name:     "double",
				Instance: &doubleItemOp{readField: "val", writeField: "doubled"},
				Config: config.OperatorConfig{
					TypeName:     "transform",
					OperatorType: "Transform",
					DataParallel: shards,
					Meta:         config.Metadata{ItemInput: []string{"val"}, ItemOutput: []string{"doubled"}},
					InputSpec:    config.ComputeInputFieldSpec(config.Metadata{ItemInput: []string{"val"}, ItemOutput: []string{"doubled"}}, nil, nil, nil),
				},
			},
		})
		if _, _, err := Run(context.Background(), dpPlan, dpFrame, nil, nil); err != nil {
			t.Fatalf("data_parallel=%d: %v", shards, err)
		}

		for i := 0; i < itemCount; i++ {
			if got, want := dpFrame.Item(i, "doubled"), baseFrame.Item(i, "doubled"); got != want {
				t.Fatalf("item %d: data_parallel=%d got %v, want %v", i, shards, got, want)
			}
		}
	})
}

func boundedAbs(v, limit int) int {
	if limit <= 0 {
		return 0
	}
	if v < 0 {
		maxInt := int(^uint(0) >> 1)
		if v == -maxInt-1 {
			return (maxInt%limit + 1) % limit
		}
		v = -v
	}
	return v % limit
}

func TestBoundedAbs(t *testing.T) {
	tests := []struct {
		name  string
		value int
		limit int
		want  int
	}{
		{name: "positive", value: 35, limit: 16, want: 3},
		{name: "negative", value: -35, limit: 16, want: 3},
		{name: "minus_one", value: -1, limit: 16, want: 1},
		{name: "zero_limit", value: -35, limit: 0, want: 0},
	}
	maxInt := int(^uint(0) >> 1)
	tests = append(tests, struct {
		name  string
		value int
		limit int
		want  int
	}{
		name:  "min_int",
		value: -maxInt - 1,
		limit: 17,
		want:  (maxInt%17 + 1) % 17,
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := boundedAbs(tt.value, tt.limit); got != tt.want {
				t.Fatalf("boundedAbs(%d, %d) = %d, want %d", tt.value, tt.limit, got, tt.want)
			}
		})
	}
}

func cloneRuntimeItems(in []map[string]any) []map[string]any {
	out := make([]map[string]any, len(in))
	for i, item := range in {
		row := make(map[string]any, len(item))
		for k, v := range item {
			row[k] = v
		}
		out[i] = row
	}
	return out
}

// --- race safety tests (meaningful under go test -race) ---

// safeStatelessOp is a stateless operator — safe for concurrent Execute.
type safeStatelessOp struct {
	types.ConcurrentSafeMarker
	readField  string
	writeField string
}

func (o *safeStatelessOp) Init(params map[string]any) error { return nil }
func (o *safeStatelessOp) Execute(_ context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	for i := 0; i < in.ItemCount(); i++ {
		v, _ := in.Item(i, o.readField).(float64)
		out.SetItem(i, o.writeField, v+1)
	}
	return nil
}

func TestParallelExecuteRaceSafe(t *testing.T) {
	const itemCount = 200
	items := make([]map[string]any, itemCount)
	for i := range items {
		items[i] = map[string]any{"val": float64(i)}
	}

	cop := &CompiledOperator{
		Name:     "safe_op",
		Instance: &safeStatelessOp{readField: "val", writeField: "result"},
		Config: config.OperatorConfig{
			TypeName:     "transform_test",
			OperatorType: "Transform",
			DataParallel: 8,
			Meta:         config.Metadata{ItemInput: []string{"val"}, ItemOutput: []string{"result"}},
		},
	}

	for iter := 0; iter < 10; iter++ {
		input := types.NewOperatorInput(map[string]any{}, cloneRuntimeItems(items))
		output, err := parallelExecute(context.Background(), cop, input)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		iw := output.GetItemWrites()
		for i := 0; i < itemCount; i++ {
			want := float64(i) + 1
			if iw[i]["result"] != want {
				t.Errorf("iter %d item %d: got %v, want %v", iter, i, iw[i]["result"], want)
			}
		}
	}
}
