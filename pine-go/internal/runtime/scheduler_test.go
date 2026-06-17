package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Liam0205/pineapple/pine-go/internal/config"
	"github.com/Liam0205/pineapple/pine-go/internal/dag"
	"github.com/Liam0205/pineapple/pine-go/internal/dataframe"
	"github.com/Liam0205/pineapple/pine-go/internal/types"
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

// badRemoveOp emits an out-of-range removal to force ApplyOutput failure.
type badRemoveOp struct{}

func (o *badRemoveOp) Init(params map[string]any) error { return nil }
func (o *badRemoveOp) Execute(_ context.Context, _ *types.OperatorInput, out *types.OperatorOutput) error {
	out.RemoveItem(1)
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

// captureKeysOp records the common keys it sees in the input.
type captureKeysOp struct {
	mu   sync.Mutex
	keys []string
}

func (o *captureKeysOp) Init(params map[string]any) error { return nil }
func (o *captureKeysOp) Execute(_ context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	o.mu.Lock()
	o.keys = in.CommonKeys()
	o.mu.Unlock()
	out.SetCommon("captured", true)
	return nil
}

// --- helper to build plan ---

func buildPlan(t *testing.T, seq []string, cops map[string]*CompiledOperator) *Plan {
	t.Helper()
	ops := make(map[string]config.OperatorConfig, len(cops))
	for name, cop := range cops {
		ops[name] = cop.Config
	}
	g, err := dag.Build(seq, ops, nil)
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
			InputSpec: &config.InputFieldSpec{},
		},
	}
	opB := &CompiledOperator{
		Name:     "op_b",
		Instance: &readAndSetOp{readField: "x", writeField: "y"},
		Config: config.OperatorConfig{
			TypeName:  "rw",
			Meta:      config.Metadata{CommonInput: []string{"x"}, CommonOutput: []string{"y"}},
			InputSpec: config.ComputeInputFieldSpec(config.Metadata{CommonInput: []string{"x"}, CommonOutput: []string{"y"}}, nil, nil, nil, nil, nil),
		},
	}

	plan := buildPlan(t, []string{"op_a", "op_b"}, map[string]*CompiledOperator{
		"op_a": opA, "op_b": opB,
	})
	frame := dataframe.New(map[string]any{}, nil)
	warnings, traces, err := Run(context.Background(), plan, frame, nil, nil)
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
			TypeName:  "sleep",
			Meta:      config.Metadata{CommonOutput: []string{"a_done"}},
			InputSpec: &config.InputFieldSpec{},
		},
	}
	opB := &CompiledOperator{
		Name:     "op_b",
		Instance: &sleepOp{d: 50 * time.Millisecond, started: startedB},
		Config: config.OperatorConfig{
			TypeName:  "sleep",
			Meta:      config.Metadata{CommonOutput: []string{"b_done"}},
			InputSpec: &config.InputFieldSpec{},
		},
	}

	plan := buildPlan(t, []string{"op_a", "op_b"}, map[string]*CompiledOperator{
		"op_a": opA, "op_b": opB,
	})
	frame := dataframe.New(map[string]any{}, nil)

	start := time.Now()
	_, _, err := Run(context.Background(), plan, frame, nil, nil)
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
			TypeName:  "set",
			Meta:      config.Metadata{CommonOutput: []string{"_if_1"}},
			InputSpec: &config.InputFieldSpec{},
		},
	}
	branch := &CompiledOperator{
		Name:     "branch",
		Instance: &setCommonOp{field: "executed", value: true},
		Config: config.OperatorConfig{
			TypeName:  "set",
			Meta:      config.Metadata{CommonInput: []string{"_if_1"}, CommonOutput: []string{"executed"}},
			Skip:      []string{"_if_1"},
			InputSpec: config.ComputeInputFieldSpec(config.Metadata{CommonInput: []string{"_if_1"}, CommonOutput: []string{"executed"}}, nil, nil, nil, nil, []string{"_if_1"}),
		},
	}

	plan := buildPlan(t, []string{"ctrl", "branch"}, map[string]*CompiledOperator{
		"ctrl": ctrl, "branch": branch,
	})
	frame := dataframe.New(map[string]any{}, nil)
	_, traces, err := Run(context.Background(), plan, frame, nil, nil)
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
		return // unreachable, but staticcheck SA5011 needs an explicit terminator
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
			TypeName:  "set",
			Meta:      config.Metadata{CommonOutput: []string{"_if_1"}},
			InputSpec: &config.InputFieldSpec{},
		},
	}
	branch := &CompiledOperator{
		Name:     "branch",
		Instance: &setCommonOp{field: "executed", value: true},
		Config: config.OperatorConfig{
			TypeName:  "set",
			Meta:      config.Metadata{CommonInput: []string{"_if_1"}, CommonOutput: []string{"executed"}},
			Skip:      []string{"_if_1"},
			InputSpec: config.ComputeInputFieldSpec(config.Metadata{CommonInput: []string{"_if_1"}, CommonOutput: []string{"executed"}}, nil, nil, nil, nil, []string{"_if_1"}),
		},
	}

	plan := buildPlan(t, []string{"ctrl", "branch"}, map[string]*CompiledOperator{
		"ctrl": ctrl, "branch": branch,
	})
	frame := dataframe.New(map[string]any{}, nil)
	_, _, err := Run(context.Background(), plan, frame, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Common("executed") != true {
		t.Error("branch should have executed")
	}
}

func TestRunSkipMultiple(t *testing.T) {
	// Two skip fields: _if_1=false, _if_2=true → should be skipped (any true → skip)
	ctrl1 := &CompiledOperator{
		Name:     "ctrl1",
		Instance: &setCommonOp{field: "_if_1", value: false},
		Config: config.OperatorConfig{
			TypeName:  "set",
			Meta:      config.Metadata{CommonOutput: []string{"_if_1"}},
			InputSpec: &config.InputFieldSpec{},
		},
	}
	ctrl2 := &CompiledOperator{
		Name:     "ctrl2",
		Instance: &setCommonOp{field: "_if_2", value: true},
		Config: config.OperatorConfig{
			TypeName:  "set",
			Meta:      config.Metadata{CommonInput: []string{"_if_1"}, CommonOutput: []string{"_if_2"}},
			InputSpec: config.ComputeInputFieldSpec(config.Metadata{CommonInput: []string{"_if_1"}, CommonOutput: []string{"_if_2"}}, nil, nil, nil, nil, nil),
		},
	}
	branch := &CompiledOperator{
		Name:     "branch",
		Instance: &setCommonOp{field: "executed", value: true},
		Config: config.OperatorConfig{
			TypeName:  "set",
			Meta:      config.Metadata{CommonInput: []string{"_if_1", "_if_2"}, CommonOutput: []string{"executed"}},
			Skip:      []string{"_if_1", "_if_2"},
			InputSpec: config.ComputeInputFieldSpec(config.Metadata{CommonInput: []string{"_if_1", "_if_2"}, CommonOutput: []string{"executed"}}, nil, nil, nil, nil, []string{"_if_1", "_if_2"}),
		},
	}

	plan := buildPlan(t, []string{"ctrl1", "ctrl2", "branch"}, map[string]*CompiledOperator{
		"ctrl1": ctrl1, "ctrl2": ctrl2, "branch": branch,
	})
	frame := dataframe.New(map[string]any{}, nil)
	_, traces, err := Run(context.Background(), plan, frame, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Common("executed") != nil {
		t.Error("branch should have been skipped because _if_2 is true")
	}
	var branchTrace *types.OpTrace
	for i := range traces {
		if traces[i].Name == "branch" {
			branchTrace = &traces[i]
			break
		}
	}
	if branchTrace == nil {
		t.Fatal("no trace for branch operator")
		return // unreachable, but staticcheck SA5011 needs an explicit terminator
	}
	if !branchTrace.Skipped {
		t.Error("branch trace should be marked skipped")
	}
}

func TestRunFatalError(t *testing.T) {
	opA := &CompiledOperator{
		Name:     "op_a",
		Instance: &errorOp{msg: "boom"},
		Config: config.OperatorConfig{
			TypeName:  "err",
			Meta:      config.Metadata{CommonOutput: []string{"x"}},
			InputSpec: &config.InputFieldSpec{},
		},
	}
	opB := &CompiledOperator{
		Name:     "op_b",
		Instance: &setCommonOp{field: "y", value: 1},
		Config: config.OperatorConfig{
			TypeName:  "set",
			Meta:      config.Metadata{CommonInput: []string{"x"}, CommonOutput: []string{"y"}},
			InputSpec: config.ComputeInputFieldSpec(config.Metadata{CommonInput: []string{"x"}, CommonOutput: []string{"y"}}, nil, nil, nil, nil, nil),
		},
	}

	plan := buildPlan(t, []string{"op_a", "op_b"}, map[string]*CompiledOperator{
		"op_a": opA, "op_b": opB,
	})
	frame := dataframe.New(map[string]any{}, nil)
	_, traces, err := Run(context.Background(), plan, frame, nil, nil)
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
	// traces should not contain empty entries from unexecuted operators
	for _, tr := range traces {
		if tr.Name == "" {
			t.Error("trace contains empty entry from unexecuted operator")
		}
	}
	// only op_a should appear in traces
	if len(traces) != 1 {
		t.Errorf("expected 1 trace entry, got %d", len(traces))
	} else if traces[0].Name != "op_a" {
		t.Errorf("expected trace for op_a, got %q", traces[0].Name)
	}
}

func TestRunApplyOutputErrorRecordsErrorStats(t *testing.T) {
	frame := dataframe.New(map[string]any{}, []map[string]any{{"remove": true}})
	op := &CompiledOperator{
		Name:     "bad_apply",
		Instance: &badRemoveOp{},
		Config: config.OperatorConfig{
			TypeName:  "filter",
			Meta:      config.Metadata{ItemInput: []string{"remove"}},
			InputSpec: config.ComputeInputFieldSpec(config.Metadata{ItemInput: []string{"remove"}}, nil, nil, nil, nil, nil),
		},
	}
	plan := buildPlan(t, []string{"bad_apply"}, map[string]*CompiledOperator{
		"bad_apply": op,
	})

	stats := NewStats()
	_, _, err := Run(context.Background(), plan, frame, stats, nil)
	if err == nil {
		t.Fatal("expected apply output error")
	}
	snap := stats.Snapshot()["bad_apply"]
	if snap.ErrorCount != 1 {
		t.Errorf("error_count = %d, want 1", snap.ErrorCount)
	}
	if snap.ExecCount != 0 {
		t.Errorf("exec_count = %d, want 0", snap.ExecCount)
	}
}

func TestRunSkipFieldFilteredFromInput(t *testing.T) {
	// ctrl_op writes _if_1=false, branch_op has skip=_if_1 and
	// common_input=[_if_1, user_id]. The scheduler should filter _if_1
	// out of BuildInput so the operator only sees user_id.
	ctrlOp := &CompiledOperator{
		Name:     "ctrl_op",
		Instance: &setCommonOp{field: "_if_1", value: false},
		Config: config.OperatorConfig{
			TypeName:         "set",
			ForBranchControl: true,
			Meta:             config.Metadata{CommonOutput: []string{"_if_1"}},
			InputSpec:        &config.InputFieldSpec{},
		},
	}
	capture := &captureKeysOp{}
	branchOp := &CompiledOperator{
		Name:     "branch_op",
		Instance: capture,
		Config: config.OperatorConfig{
			TypeName:  "capture",
			Skip:      []string{"_if_1"},
			Meta:      config.Metadata{CommonInput: []string{"_if_1", "user_id"}, CommonOutput: []string{"captured"}},
			InputSpec: config.ComputeInputFieldSpec(config.Metadata{CommonInput: []string{"_if_1", "user_id"}, CommonOutput: []string{"captured"}}, nil, nil, nil, nil, []string{"_if_1"}),
		},
	}

	plan := buildPlan(t, []string{"ctrl_op", "branch_op"}, map[string]*CompiledOperator{
		"ctrl_op":   ctrlOp,
		"branch_op": branchOp,
	})
	frame := dataframe.New(map[string]any{"user_id": "u123"}, nil)
	_, _, err := Run(context.Background(), plan, frame, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// branch_op should have executed (skip=_if_1=false means not skipped)
	if frame.Common("captured") != true {
		t.Error("branch_op should have executed")
	}

	// The operator input should NOT contain the skip field _if_1
	capture.mu.Lock()
	keys := capture.keys
	capture.mu.Unlock()
	for _, k := range keys {
		if k == "_if_1" {
			t.Error("skip field _if_1 should not appear in operator input")
		}
	}
	// Should contain user_id
	found := false
	for _, k := range keys {
		if k == "user_id" {
			found = true
		}
	}
	if !found {
		t.Error("user_id should appear in operator input")
	}
}

func TestRunSkipMultipleAllFalse(t *testing.T) {
	// Two skip fields both false → operator should execute
	ctrl1 := &CompiledOperator{
		Name:     "ctrl1",
		Instance: &setCommonOp{field: "_if_1", value: false},
		Config: config.OperatorConfig{
			TypeName:  "set",
			Meta:      config.Metadata{CommonOutput: []string{"_if_1"}},
			InputSpec: &config.InputFieldSpec{},
		},
	}
	ctrl2 := &CompiledOperator{
		Name:     "ctrl2",
		Instance: &setCommonOp{field: "_if_2", value: false},
		Config: config.OperatorConfig{
			TypeName:  "set",
			Meta:      config.Metadata{CommonInput: []string{"_if_1"}, CommonOutput: []string{"_if_2"}},
			InputSpec: config.ComputeInputFieldSpec(config.Metadata{CommonInput: []string{"_if_1"}, CommonOutput: []string{"_if_2"}}, nil, nil, nil, nil, nil),
		},
	}
	branch := &CompiledOperator{
		Name:     "branch",
		Instance: &setCommonOp{field: "executed", value: true},
		Config: config.OperatorConfig{
			TypeName:  "set",
			Meta:      config.Metadata{CommonInput: []string{"_if_1", "_if_2"}, CommonOutput: []string{"executed"}},
			Skip:      []string{"_if_1", "_if_2"},
			InputSpec: config.ComputeInputFieldSpec(config.Metadata{CommonInput: []string{"_if_1", "_if_2"}, CommonOutput: []string{"executed"}}, nil, nil, nil, nil, []string{"_if_1", "_if_2"}),
		},
	}

	plan := buildPlan(t, []string{"ctrl1", "ctrl2", "branch"}, map[string]*CompiledOperator{
		"ctrl1": ctrl1, "ctrl2": ctrl2, "branch": branch,
	})
	frame := dataframe.New(map[string]any{}, nil)
	_, traces, err := Run(context.Background(), plan, frame, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Common("executed") != true {
		t.Error("branch should have executed when all skip fields are false")
	}
	var branchTrace *types.OpTrace
	for i := range traces {
		if traces[i].Name == "branch" {
			branchTrace = &traces[i]
			break
		}
	}
	if branchTrace == nil {
		t.Fatal("no trace for branch operator")
		return // unreachable, but staticcheck SA5011 needs an explicit terminator
	}
	if branchTrace.Skipped {
		t.Error("branch trace should not be marked skipped")
	}
}

func TestRunSkipMultipleFieldsFilteredFromInput(t *testing.T) {
	// Multiple skip fields should all be filtered from operator input
	ctrl1 := &CompiledOperator{
		Name:     "ctrl1",
		Instance: &setCommonOp{field: "_if_1", value: false},
		Config: config.OperatorConfig{
			TypeName:         "set",
			ForBranchControl: true,
			Meta:             config.Metadata{CommonOutput: []string{"_if_1"}},
			InputSpec:        &config.InputFieldSpec{},
		},
	}
	ctrl2 := &CompiledOperator{
		Name:     "ctrl2",
		Instance: &setCommonOp{field: "_if_2", value: false},
		Config: config.OperatorConfig{
			TypeName:         "set",
			ForBranchControl: true,
			Meta:             config.Metadata{CommonInput: []string{"_if_1"}, CommonOutput: []string{"_if_2"}},
			Skip:             []string{"_if_1"},
			InputSpec:        config.ComputeInputFieldSpec(config.Metadata{CommonInput: []string{"_if_1"}, CommonOutput: []string{"_if_2"}}, nil, nil, nil, nil, []string{"_if_1"}),
		},
	}
	capture := &captureKeysOp{}
	branch := &CompiledOperator{
		Name:     "branch",
		Instance: capture,
		Config: config.OperatorConfig{
			TypeName:  "capture",
			Skip:      []string{"_if_1", "_if_2"},
			Meta:      config.Metadata{CommonInput: []string{"_if_1", "_if_2", "user_id"}, CommonOutput: []string{"captured"}},
			InputSpec: config.ComputeInputFieldSpec(config.Metadata{CommonInput: []string{"_if_1", "_if_2", "user_id"}, CommonOutput: []string{"captured"}}, nil, nil, nil, nil, []string{"_if_1", "_if_2"}),
		},
	}

	plan := buildPlan(t, []string{"ctrl1", "ctrl2", "branch"}, map[string]*CompiledOperator{
		"ctrl1": ctrl1, "ctrl2": ctrl2, "branch": branch,
	})
	frame := dataframe.New(map[string]any{"user_id": "u123"}, nil)
	_, _, err := Run(context.Background(), plan, frame, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Common("captured") != true {
		t.Error("branch should have executed")
	}
	capture.mu.Lock()
	keys := capture.keys
	capture.mu.Unlock()
	for _, k := range keys {
		if k == "_if_1" || k == "_if_2" {
			t.Errorf("skip field %q should not appear in operator input", k)
		}
	}
	found := false
	for _, k := range keys {
		if k == "user_id" {
			found = true
		}
	}
	if !found {
		t.Error("user_id should appear in operator input")
	}
}

func TestRunPanicRecovery(t *testing.T) {
	opA := &CompiledOperator{
		Name:     "op_a",
		Instance: &panicOp{},
		Config: config.OperatorConfig{
			TypeName:  "panic",
			Meta:      config.Metadata{CommonOutput: []string{"x"}},
			InputSpec: &config.InputFieldSpec{},
		},
	}

	plan := buildPlan(t, []string{"op_a"}, map[string]*CompiledOperator{
		"op_a": opA,
	})
	frame := dataframe.New(map[string]any{}, nil)
	_, _, err := Run(context.Background(), plan, frame, nil, nil)
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
			TypeName:  "warn",
			Meta:      config.Metadata{CommonOutput: []string{"fallback"}},
			InputSpec: &config.InputFieldSpec{},
		},
	}
	opB := &CompiledOperator{
		Name:     "op_b",
		Instance: &setCommonOp{field: "after_warning", value: true},
		Config: config.OperatorConfig{
			TypeName:  "set",
			Meta:      config.Metadata{CommonInput: []string{"fallback"}, CommonOutput: []string{"after_warning"}},
			InputSpec: config.ComputeInputFieldSpec(config.Metadata{CommonInput: []string{"fallback"}, CommonOutput: []string{"after_warning"}}, nil, nil, nil, nil, nil),
		},
	}

	plan := buildPlan(t, []string{"op_a", "op_b"}, map[string]*CompiledOperator{
		"op_a": opA, "op_b": opB,
	})
	frame := dataframe.New(map[string]any{}, nil)
	warnings, _, err := Run(context.Background(), plan, frame, nil, nil)
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
			Meta:      config.Metadata{ItemOutput: []string{"item_id"}},
			InputSpec: &config.InputFieldSpec{},
		},
	}

	plan := buildPlan(t, []string{"recall_a"}, map[string]*CompiledOperator{
		"recall_a": opA,
	})
	frame := dataframe.New(map[string]any{}, nil)
	_, _, err := Run(context.Background(), plan, frame, nil, nil)
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
			TypeName:  "filter",
			Meta:      config.Metadata{ItemInput: []string{"id", "remove"}},
			InputSpec: config.ComputeInputFieldSpec(config.Metadata{ItemInput: []string{"id", "remove"}}, nil, nil, nil, nil, nil),
		},
	}

	plan := buildPlan(t, []string{"filter"}, map[string]*CompiledOperator{
		"filter": opA,
	})
	frame := dataframe.New(map[string]any{}, items)
	_, _, err := Run(context.Background(), plan, frame, nil, nil)
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
			TypeName:  "reorder",
			Meta:      config.Metadata{ItemInput: []string{"id"}},
			InputSpec: config.ComputeInputFieldSpec(config.Metadata{ItemInput: []string{"id"}}, nil, nil, nil, nil, nil),
		},
	}

	plan := buildPlan(t, []string{"reorder"}, map[string]*CompiledOperator{
		"reorder": opA,
	})
	frame := dataframe.New(map[string]any{}, items)
	_, _, err := Run(context.Background(), plan, frame, nil, nil)
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
			TypeName:  "set",
			Meta:      config.Metadata{CommonOutput: []string{"x"}},
			InputSpec: &config.InputFieldSpec{},
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
			_, _, err := Run(context.Background(), plan, frame, nil, nil)
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
			TypeName:  "set",
			Meta:      config.Metadata{CommonOutput: []string{"x"}},
			InputSpec: &config.InputFieldSpec{},
		},
	}

	plan := buildPlan(t, []string{"op_a"}, map[string]*CompiledOperator{
		"op_a": opA,
	})
	frame := dataframe.New(map[string]any{}, nil)
	_, _, err := Run(ctx, plan, frame, nil, nil)
	// With immediate cancellation, op may or may not execute.
	// The key is: no panic, no hang.
	_ = err
}

// inspectOutputOp asserts the OperatorOutput it receives from the scheduler
// is clean (no leftover writes from a previous request). Used by
// TestRun_OutputPool_NoStateLeakageAcrossRuns to lock the contract that
// outputPool's Reset removes all per-request state before handing the
// output to the next Execute.
type inspectOutputOp struct {
	t           *testing.T
	writeField  string
	writeValue  any
	saw         []string // names of fields the output already contained on entry
	sawAddedLen int
}

func (o *inspectOutputOp) Init(params map[string]any) error { return nil }
func (o *inspectOutputOp) Execute(_ context.Context, _ *types.OperatorInput, out *types.OperatorOutput) error {
	// On every Execute, the scheduler must hand us a freshly-Reset output.
	// If outputPool leaks state across requests, GetCommonWrites would be
	// non-empty here on the second-and-later calls.
	for k := range out.GetCommonWrites() {
		o.saw = append(o.saw, k)
	}
	if added := out.GetAddedItems(); len(added) > 0 {
		o.sawAddedLen += len(added)
	}
	out.SetCommon(o.writeField, o.writeValue)
	return nil
}

// TestRun_OutputPool_NoStateLeakageAcrossRuns drives the same Plan many
// times. The outputPool will hand the same *OperatorOutput back across
// requests (sync.Pool reuse is best-effort but extremely likely under
// sequential single-goroutine execution), and the inspectOutputOp asserts
// at the top of every Execute that the output looks freshly-constructed.
// Any leak from Reset would surface as `sawXX > 0` on the second iteration.
func TestRun_OutputPool_NoStateLeakageAcrossRuns(t *testing.T) {
	op := &inspectOutputOp{
		t:          t,
		writeField: "x",
		writeValue: 1.0,
	}
	cop := &CompiledOperator{
		Name:     "op_a",
		Instance: op,
		Config: config.OperatorConfig{
			TypeName:  "inspect",
			Meta:      config.Metadata{CommonOutput: []string{"x"}},
			InputSpec: &config.InputFieldSpec{},
		},
	}
	plan := buildPlan(t, []string{"op_a"}, map[string]*CompiledOperator{
		"op_a": cop,
	})

	const runs = 32
	for i := 0; i < runs; i++ {
		frame := dataframe.New(map[string]any{}, nil)
		_, _, err := Run(context.Background(), plan, frame, nil, nil)
		if err != nil {
			t.Fatalf("Run #%d: %v", i, err)
		}
		if got := frame.Common("x"); got != 1.0 {
			t.Fatalf("Run #%d: x = %v, want 1.0", i, got)
		}
	}

	if len(op.saw) != 0 {
		t.Errorf("inspectOutputOp observed leaked commonWrites across %d runs: %v",
			runs, op.saw)
	}
	if op.sawAddedLen != 0 {
		t.Errorf("inspectOutputOp observed leaked addedItems across %d runs: total len = %d",
			runs, op.sawAddedLen)
	}
}

// TestRun_OutputPool_NoLeakageWithAdditions extends the leakage assertion
// to addedItems — a separate Reset code path (slice nil-out, not
// delete-map). The recall op adds N items per request; if Reset failed to
// nil out the slice slots, the second request would see them surface
// either via GetAddedItems or as ghost items appended again to the frame.
func TestRun_OutputPool_NoLeakageWithAdditions(t *testing.T) {
	inspect := &inspectOutputOp{
		t:          t,
		writeField: "ok",
		writeValue: true,
	}
	recall := &recallTestOp{
		items: []map[string]any{
			{"id": "a"}, {"id": "b"}, {"id": "c"},
		},
	}
	recallCop := &CompiledOperator{
		Name:     "recall",
		Instance: recall,
		Config: config.OperatorConfig{
			TypeName:     "recall",
			Recall:       true,
			Meta:         config.Metadata{},
			InputSpec:    &config.InputFieldSpec{},
			OperatorType: "Recall",
		},
	}
	inspectCop := &CompiledOperator{
		Name:     "after",
		Instance: inspect,
		Config: config.OperatorConfig{
			TypeName:  "inspect",
			Meta:      config.Metadata{CommonOutput: []string{"ok"}},
			InputSpec: &config.InputFieldSpec{},
		},
	}
	plan := buildPlan(t, []string{"recall", "after"}, map[string]*CompiledOperator{
		"recall": recallCop,
		"after":  inspectCop,
	})

	const runs = 16
	for i := 0; i < runs; i++ {
		frame := dataframe.New(map[string]any{}, nil)
		_, _, err := Run(context.Background(), plan, frame, nil, nil)
		if err != nil {
			t.Fatalf("Run #%d: %v", i, err)
		}
		// Each request adds exactly the 3 items recall emits — no ghosts
		// from a previous request's pooled output.
		if got := frame.ItemCount(); got != 3 {
			t.Fatalf("Run #%d: ItemCount = %d, want 3 (ghost items from pooled state?)",
				i, got)
		}
	}

	// The inspect op runs after recall in the DAG. recall's added items are
	// transferred into the frame, then recall's output is Reset+Put before
	// inspect sees its own (separately pooled) output. inspect therefore
	// expects its own output clean at entry — leak would show as nonzero.
	if op := inspect; len(op.saw) != 0 || op.sawAddedLen != 0 {
		t.Errorf("inspect saw leaked state across %d runs: commonWrites=%v sawAddedLen=%d",
			runs, op.saw, op.sawAddedLen)
	}
}
