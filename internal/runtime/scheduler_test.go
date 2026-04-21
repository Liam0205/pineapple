package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Liam0205/pineapple/internal/config"
	"github.com/Liam0205/pineapple/internal/dag"
	"github.com/Liam0205/pineapple/internal/dataframe"
	"github.com/Liam0205/pineapple/internal/types"
)

// --- test operator helpers ---

// setCommonOp sets a common field to a fixed value.
type setCommonOp struct {
	field string
	value any
}

func (o *setCommonOp) Init(params map[string]any) error {
	o.field = params["field"].(string)
	o.value = params["value"]
	return nil
}
func (o *setCommonOp) Execute(_ context.Context, _ *types.OperatorInput, out *types.OperatorOutput) error {
	out.SetCommon(o.field, o.value)
	return nil
}

// readAndSetOp reads a common field and writes its value * 2 to another.
type readAndSetOp struct {
	readField  string
	writeField string
}

func (o *readAndSetOp) Init(params map[string]any) error {
	o.readField = params["read"].(string)
	o.writeField = params["write"].(string)
	return nil
}
func (o *readAndSetOp) Execute(_ context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	v, ok := in.Common(o.readField).(float64)
	if !ok {
		return fmt.Errorf("expected float64 for %s", o.readField)
	}
	out.SetCommon(o.writeField, v*2)
	return nil
}

// recallOp adds items via AddItem.
type recallTestOp struct {
	items []map[string]any
}

func (o *recallTestOp) Init(params map[string]any) error { return nil }
func (o *recallTestOp) Execute(_ context.Context, _ *types.OperatorInput, out *types.OperatorOutput) error {
	for _, item := range o.items {
		out.AddItem(item)
	}
	return nil
}

// filterOp removes items where a field matches a value.
type filterTestOp struct{}

func (o *filterTestOp) Init(params map[string]any) error { return nil }
func (o *filterTestOp) Execute(_ context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	for i := 0; i < in.ItemCount(); i++ {
		if in.Item(i, "remove") == true {
			out.RemoveItem(i)
		}
	}
	return nil
}

// reorderOp reverses item order.
type reorderTestOp struct{}

func (o *reorderTestOp) Init(params map[string]any) error { return nil }
func (o *reorderTestOp) Execute(_ context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	n := in.ItemCount()
	order := make([]int, n)
	for i := 0; i < n; i++ {
		order[i] = n - 1 - i
	}
	out.SetItemOrder(order)
	return nil
}

// errorOp returns an error.
type errorOp struct{ msg string }

func (o *errorOp) Init(params map[string]any) error { return nil }
func (o *errorOp) Execute(_ context.Context, _ *types.OperatorInput, _ *types.OperatorOutput) error {
	return fmt.Errorf("%s", o.msg)
}

// panicOp panics.
type panicOp struct{}

func (o *panicOp) Init(params map[string]any) error { return nil }
func (o *panicOp) Execute(_ context.Context, _ *types.OperatorInput, _ *types.OperatorOutput) error {
	panic("test panic")
}

// warningOp sets a warning but returns nil.
type warningOp struct{}

func (o *warningOp) Init(params map[string]any) error { return nil }
func (o *warningOp) Execute(_ context.Context, _ *types.OperatorInput, out *types.OperatorOutput) error {
	out.SetWarning(fmt.Errorf("recoverable warning"))
	out.SetCommon("fallback", "default_value")
	return nil
}

// sleepOp sleeps for a duration to help test parallelism.
type sleepOp struct {
	d       time.Duration
	started *atomic.Int64
}

func (o *sleepOp) Init(params map[string]any) error { return nil }
func (o *sleepOp) Execute(_ context.Context, _ *types.OperatorInput, out *types.OperatorOutput) error {
	o.started.Add(1)
	time.Sleep(o.d)
	out.SetCommon("done", true)
	return nil
}

// --- helper to build plan ---

func buildPlan(t *testing.T, seq []string, cops map[string]*CompiledOperator) *Plan {
	t.Helper()
	ops := make(map[string]config.OperatorConfig, len(cops))
	for name, cop := range cops {
		ops[name] = cop.Config
	}
	g, err := dag.Build(seq, ops)
	if err != nil {
		t.Fatalf("dag.Build: %v", err)
	}
	ordered := make([]*CompiledOperator, len(seq))
	for i, name := range seq {
		ordered[i] = cops[name]
	}
	return &Plan{Graph: g, Operators: ordered}
}

// --- tests ---

func TestRunSimpleChain(t *testing.T) {
	// op_a writes x=10, op_b reads x writes y=x*2
	opA := &CompiledOperator{
		Name:     "op_a",
		Instance: &setCommonOp{field: "x", value: 10.0},
		Config: config.OperatorConfig{
			TypeName: "set", Meta: config.Metadata{CommonOutput: []string{"x"}},
		},
	}
	opB := &CompiledOperator{
		Name:     "op_b",
		Instance: &readAndSetOp{readField: "x", writeField: "y"},
		Config: config.OperatorConfig{
			TypeName: "rw", Meta: config.Metadata{CommonInput: []string{"x"}, CommonOutput: []string{"y"}},
		},
	}

	plan := buildPlan(t, []string{"op_a", "op_b"}, map[string]*CompiledOperator{
		"op_a": opA, "op_b": opB,
	})
	frame := dataframe.New(map[string]any{}, nil)
	warnings, traces, err := Run(context.Background(), plan, frame, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if frame.Common("x") != 10.0 {
		t.Errorf("x = %v", frame.Common("x"))
	}
	if frame.Common("y") != 20.0 {
		t.Errorf("y = %v, want 20", frame.Common("y"))
	}
	// Verify trace
	if len(traces) != 2 {
		t.Fatalf("trace count = %d, want 2", len(traces))
	}
	for _, tr := range traces {
		if tr.Duration <= 0 {
			t.Errorf("trace %q duration = %v, want > 0", tr.Name, tr.Duration)
		}
		if tr.Skipped {
			t.Errorf("trace %q should not be skipped", tr.Name)
		}
	}
}

func TestRunParallelOps(t *testing.T) {
	// Two independent ops should start ~simultaneously
	startedA := &atomic.Int64{}
	startedB := &atomic.Int64{}

	opA := &CompiledOperator{
		Name:     "op_a",
		Instance: &sleepOp{d: 50 * time.Millisecond, started: startedA},
		Config: config.OperatorConfig{
			TypeName: "sleep", Meta: config.Metadata{CommonOutput: []string{"a_done"}},
		},
	}
	opB := &CompiledOperator{
		Name:     "op_b",
		Instance: &sleepOp{d: 50 * time.Millisecond, started: startedB},
		Config: config.OperatorConfig{
			TypeName: "sleep", Meta: config.Metadata{CommonOutput: []string{"b_done"}},
		},
	}

	plan := buildPlan(t, []string{"op_a", "op_b"}, map[string]*CompiledOperator{
		"op_a": opA, "op_b": opB,
	})
	frame := dataframe.New(map[string]any{}, nil)

	start := time.Now()
	_, _, err := Run(context.Background(), plan, frame, nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}
	// If parallel, total time should be ~50ms, not ~100ms
	if elapsed > 90*time.Millisecond {
		t.Errorf("parallel ops took %v, expected ~50ms", elapsed)
	}
}

func TestRunSkipTrue(t *testing.T) {
	// ctrl writes _if_1=true, branch has skip="_if_1" -> should be skipped
	ctrl := &CompiledOperator{
		Name:     "ctrl",
		Instance: &setCommonOp{field: "_if_1", value: true},
		Config: config.OperatorConfig{
			TypeName: "set", Meta: config.Metadata{CommonOutput: []string{"_if_1"}},
		},
	}
	branch := &CompiledOperator{
		Name:     "branch",
		Instance: &setCommonOp{field: "executed", value: true},
		Config: config.OperatorConfig{
			TypeName: "set", Meta: config.Metadata{CommonInput: []string{"_if_1"}, CommonOutput: []string{"executed"}},
			Skip: "_if_1",
		},
	}

	plan := buildPlan(t, []string{"ctrl", "branch"}, map[string]*CompiledOperator{
		"ctrl": ctrl, "branch": branch,
	})
	frame := dataframe.New(map[string]any{}, nil)
	_, traces, err := Run(context.Background(), plan, frame, nil)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Common("executed") != nil {
		t.Error("branch should have been skipped")
	}
	// Verify trace records skip
	var branchTrace *types.OpTrace
	for i := range traces {
		if traces[i].Name == "branch" {
			branchTrace = &traces[i]
			break
		}
	}
	if branchTrace == nil {
		t.Fatal("no trace for branch operator")
	}
	if !branchTrace.Skipped {
		t.Error("branch trace should be marked skipped")
	}
}

func TestRunSkipFalse(t *testing.T) {
	// ctrl writes _if_1=false, branch has skip="_if_1" -> should execute
	ctrl := &CompiledOperator{
		Name:     "ctrl",
		Instance: &setCommonOp{field: "_if_1", value: false},
		Config: config.OperatorConfig{
			TypeName: "set", Meta: config.Metadata{CommonOutput: []string{"_if_1"}},
		},
	}
	branch := &CompiledOperator{
		Name:     "branch",
		Instance: &setCommonOp{field: "executed", value: true},
		Config: config.OperatorConfig{
			TypeName: "set", Meta: config.Metadata{CommonInput: []string{"_if_1"}, CommonOutput: []string{"executed"}},
			Skip: "_if_1",
		},
	}

	plan := buildPlan(t, []string{"ctrl", "branch"}, map[string]*CompiledOperator{
		"ctrl": ctrl, "branch": branch,
	})
	frame := dataframe.New(map[string]any{}, nil)
	_, _, err := Run(context.Background(), plan, frame, nil)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Common("executed") != true {
		t.Error("branch should have executed")
	}
}

func TestRunFatalError(t *testing.T) {
	opA := &CompiledOperator{
		Name:     "op_a",
		Instance: &errorOp{msg: "boom"},
		Config: config.OperatorConfig{
			TypeName: "err", Meta: config.Metadata{CommonOutput: []string{"x"}},
		},
	}
	opB := &CompiledOperator{
		Name:     "op_b",
		Instance: &setCommonOp{field: "y", value: 1},
		Config: config.OperatorConfig{
			TypeName: "set", Meta: config.Metadata{CommonInput: []string{"x"}, CommonOutput: []string{"y"}},
		},
	}

	plan := buildPlan(t, []string{"op_a", "op_b"}, map[string]*CompiledOperator{
		"op_a": opA, "op_b": opB,
	})
	frame := dataframe.New(map[string]any{}, nil)
	_, _, err := Run(context.Background(), plan, frame, nil)
	if err == nil {
		t.Fatal("expected fatal error")
	}
	var execErr *types.ExecutionError
	if !errors.As(err, &execErr) {
		t.Errorf("expected ExecutionError, got %T", err)
	}
	// op_b should not have executed
	if frame.Common("y") != nil {
		t.Error("op_b should not have executed after op_a error")
	}
}

func TestRunPanicRecovery(t *testing.T) {
	opA := &CompiledOperator{
		Name:     "op_a",
		Instance: &panicOp{},
		Config: config.OperatorConfig{
			TypeName: "panic", Meta: config.Metadata{CommonOutput: []string{"x"}},
		},
	}

	plan := buildPlan(t, []string{"op_a"}, map[string]*CompiledOperator{
		"op_a": opA,
	})
	frame := dataframe.New(map[string]any{}, nil)
	_, _, err := Run(context.Background(), plan, frame, nil)
	if err == nil {
		t.Fatal("expected panic error")
	}
	var panicErr *types.PanicError
	if !errors.As(err, &panicErr) {
		t.Errorf("expected PanicError, got %T: %v", err, err)
	}
}

func TestRunWarningContinues(t *testing.T) {
	opA := &CompiledOperator{
		Name:     "op_a",
		Instance: &warningOp{},
		Config: config.OperatorConfig{
			TypeName: "warn", Meta: config.Metadata{CommonOutput: []string{"fallback"}},
		},
	}
	opB := &CompiledOperator{
		Name:     "op_b",
		Instance: &setCommonOp{field: "after_warning", value: true},
		Config: config.OperatorConfig{
			TypeName: "set", Meta: config.Metadata{CommonInput: []string{"fallback"}, CommonOutput: []string{"after_warning"}},
		},
	}

	plan := buildPlan(t, []string{"op_a", "op_b"}, map[string]*CompiledOperator{
		"op_a": opA, "op_b": opB,
	})
	frame := dataframe.New(map[string]any{}, nil)
	warnings, _, err := Run(context.Background(), plan, frame, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if warnings[0].Operator != "op_a" {
		t.Errorf("warning operator = %q", warnings[0].Operator)
	}
	if frame.Common("after_warning") != true {
		t.Error("op_b should have executed after warning")
	}
}

func TestRunRecallInjectsSource(t *testing.T) {
	opA := &CompiledOperator{
		Name: "recall_a",
		Instance: &recallTestOp{items: []map[string]any{
			{"item_id": int64(1)},
			{"item_id": int64(2)},
		}},
		Config: config.OperatorConfig{
			TypeName: "recall", Recall: true,
			Meta: config.Metadata{ItemOutput: []string{"item_id"}},
		},
	}

	plan := buildPlan(t, []string{"recall_a"}, map[string]*CompiledOperator{
		"recall_a": opA,
	})
	frame := dataframe.New(map[string]any{}, nil)
	_, _, err := Run(context.Background(), plan, frame, nil)
	if err != nil {
		t.Fatal(err)
	}
	if frame.ItemCount() != 2 {
		t.Fatalf("item count = %d", frame.ItemCount())
	}
	for i := 0; i < frame.ItemCount(); i++ {
		if frame.Item(i, "_source") != "recall_a" {
			t.Errorf("item %d _source = %v", i, frame.Item(i, "_source"))
		}
	}
}

func TestRunFilterRemovesItems(t *testing.T) {
	items := []map[string]any{
		{"id": int64(1), "remove": false},
		{"id": int64(2), "remove": true},
		{"id": int64(3), "remove": false},
	}
	opA := &CompiledOperator{
		Name:     "filter",
		Instance: &filterTestOp{},
		Config: config.OperatorConfig{
			TypeName: "filter",
			Meta:     config.Metadata{ItemInput: []string{"id", "remove"}},
		},
	}

	plan := buildPlan(t, []string{"filter"}, map[string]*CompiledOperator{
		"filter": opA,
	})
	frame := dataframe.New(map[string]any{}, items)
	_, _, err := Run(context.Background(), plan, frame, nil)
	if err != nil {
		t.Fatal(err)
	}
	if frame.ItemCount() != 2 {
		t.Fatalf("item count = %d, want 2", frame.ItemCount())
	}
	if frame.Item(0, "id") != int64(1) || frame.Item(1, "id") != int64(3) {
		t.Errorf("items = %v, %v", frame.Item(0, "id"), frame.Item(1, "id"))
	}
}

func TestRunReorderReverses(t *testing.T) {
	items := []map[string]any{
		{"id": "a"}, {"id": "b"}, {"id": "c"},
	}
	opA := &CompiledOperator{
		Name:     "reorder",
		Instance: &reorderTestOp{},
		Config: config.OperatorConfig{
			TypeName: "reorder",
			Meta:     config.Metadata{ItemInput: []string{"id"}},
		},
	}

	plan := buildPlan(t, []string{"reorder"}, map[string]*CompiledOperator{
		"reorder": opA,
	})
	frame := dataframe.New(map[string]any{}, items)
	_, _, err := Run(context.Background(), plan, frame, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"c", "b", "a"}
	for i, w := range want {
		if frame.Item(i, "id") != w {
			t.Errorf("item %d = %v, want %s", i, frame.Item(i, "id"), w)
		}
	}
}

func TestRunConcurrentExecutions(t *testing.T) {
	// Same plan executed concurrently — no shared state leakage
	opA := &CompiledOperator{
		Name:     "op_a",
		Instance: &setCommonOp{field: "x", value: 1.0},
		Config: config.OperatorConfig{
			TypeName: "set", Meta: config.Metadata{CommonOutput: []string{"x"}},
		},
	}

	plan := buildPlan(t, []string{"op_a"}, map[string]*CompiledOperator{
		"op_a": opA,
	})

	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			frame := dataframe.New(map[string]any{}, nil)
			_, _, err := Run(context.Background(), plan, frame, nil)
			if err != nil {
				errs <- err
				return
			}
			if frame.Common("x") != 1.0 {
				errs <- fmt.Errorf("x = %v", frame.Common("x"))
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestRunContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	opA := &CompiledOperator{
		Name:     "op_a",
		Instance: &setCommonOp{field: "x", value: 1},
		Config: config.OperatorConfig{
			TypeName: "set", Meta: config.Metadata{CommonOutput: []string{"x"}},
		},
	}

	plan := buildPlan(t, []string{"op_a"}, map[string]*CompiledOperator{
		"op_a": opA,
	})
	frame := dataframe.New(map[string]any{}, nil)
	_, _, err := Run(ctx, plan, frame, nil)
	// With immediate cancellation, op may or may not execute.
	// The key is: no panic, no hang.
	_ = err
}
