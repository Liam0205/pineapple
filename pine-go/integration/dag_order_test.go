package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pine "github.com/Liam0205/pineapple/pine-go"
	"github.com/Liam0205/pineapple/pine-go/internal/registry"
	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

// ---------------------------------------------------------------------------
// Shared execution recorder
// ---------------------------------------------------------------------------

var (
	execCounter int64
	execLog     []execEvent
	execLogMu   sync.Mutex
)

type execEvent struct {
	Name  string
	Seq   int64
	Start time.Time
	End   time.Time
}

func resetExecLog() {
	atomic.StoreInt64(&execCounter, 0)
	execLogMu.Lock()
	execLog = nil
	execLogMu.Unlock()
}

func getEvents() []execEvent {
	execLogMu.Lock()
	defer execLogMu.Unlock()
	cp := make([]execEvent, len(execLog))
	copy(cp, execLog)
	return cp
}

func eventByName(events []execEvent, name string) (execEvent, bool) {
	for _, e := range events {
		if e.Name == name {
			return e, true
		}
	}
	return execEvent{}, false
}

// ---------------------------------------------------------------------------
// Test operators — each records execution timing to the global event log
// ---------------------------------------------------------------------------

// testTransformOp is a Transform operator that records its execution order.
// It writes sentinel values for any declared common_output fields (passed via
// "_produce" param) and item_output fields (via "_produce_item" param) so
// downstream strict field checks succeed.
type testTransformOp struct {
	name         string
	delay        time.Duration
	commonOutput []string
	itemOutput   []string
}

func (o *testTransformOp) Init(params map[string]any) error {
	if n, ok := params["name"]; ok {
		o.name = n.(string)
	}
	if d, ok := params["delay_ms"]; ok {
		o.delay = time.Duration(d.(float64)) * time.Millisecond
	}
	if p, ok := params["_produce"]; ok {
		if arr, ok := p.([]any); ok {
			for _, v := range arr {
				o.commonOutput = append(o.commonOutput, v.(string))
			}
		}
	}
	if p, ok := params["_produce_item"]; ok {
		if arr, ok := p.([]any); ok {
			for _, v := range arr {
				o.itemOutput = append(o.itemOutput, v.(string))
			}
		}
	}
	return nil
}

func (o *testTransformOp) Execute(_ context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	seq := atomic.AddInt64(&execCounter, 1)
	start := time.Now()
	if o.delay > 0 {
		time.Sleep(o.delay)
	}
	out.SetCommon("_seq_"+o.name, seq)
	for _, field := range o.commonOutput {
		out.SetCommon(field, fmt.Sprintf("_produced_by_%s", o.name))
	}
	for i := 0; i < in.ItemCount(); i++ {
		for _, field := range o.itemOutput {
			out.SetItem(i, field, fmt.Sprintf("_item_produced_by_%s", o.name))
		}
	}
	end := time.Now()
	execLogMu.Lock()
	execLog = append(execLog, execEvent{Name: o.name, Seq: seq, Start: start, End: end})
	execLogMu.Unlock()
	return nil
}

// testRecallOp is a Recall operator that records execution order and adds items.
type testRecallOp struct {
	types.AdditiveWritesRowSetMarker
	name  string
	delay time.Duration
	items []map[string]any
}

func (o *testRecallOp) Init(params map[string]any) error {
	if n, ok := params["name"]; ok {
		o.name = n.(string)
	}
	if d, ok := params["delay_ms"]; ok {
		o.delay = time.Duration(d.(float64)) * time.Millisecond
	}
	if raw, ok := params["items"].([]any); ok {
		for _, r := range raw {
			if m, ok := r.(map[string]any); ok {
				o.items = append(o.items, m)
			}
		}
	}
	return nil
}

func (o *testRecallOp) Execute(_ context.Context, _ *types.OperatorInput, out *types.OperatorOutput) error {
	seq := atomic.AddInt64(&execCounter, 1)
	start := time.Now()
	if o.delay > 0 {
		time.Sleep(o.delay)
	}
	for _, item := range o.items {
		out.AddItem(item)
	}
	end := time.Now()
	execLogMu.Lock()
	execLog = append(execLog, execEvent{Name: o.name, Seq: seq, Start: start, End: end})
	execLogMu.Unlock()
	return nil
}

// testFilterOp is a Filter operator that records execution order.
// It consumes and mutates the row set.
type testFilterOp struct {
	pine.ConsumesRowSetMarker
	pine.MutatesRowSetMarker
	name  string
	delay time.Duration
}

func (o *testFilterOp) Init(params map[string]any) error {
	if n, ok := params["name"]; ok {
		o.name = n.(string)
	}
	if d, ok := params["delay_ms"]; ok {
		o.delay = time.Duration(d.(float64)) * time.Millisecond
	}
	return nil
}

func (o *testFilterOp) Execute(_ context.Context, _ *types.OperatorInput, _ *types.OperatorOutput) error {
	seq := atomic.AddInt64(&execCounter, 1)
	start := time.Now()
	if o.delay > 0 {
		time.Sleep(o.delay)
	}
	end := time.Now()
	execLogMu.Lock()
	execLog = append(execLog, execEvent{Name: o.name, Seq: seq, Start: start, End: end})
	execLogMu.Unlock()
	return nil
}

// testMergeOp is a Merge operator that records execution order.
// It consumes and mutates the row set.
type testMergeOp struct {
	pine.ConsumesRowSetMarker
	pine.MutatesRowSetMarker
	name  string
	delay time.Duration
}

func (o *testMergeOp) Init(params map[string]any) error {
	if n, ok := params["name"]; ok {
		o.name = n.(string)
	}
	if d, ok := params["delay_ms"]; ok {
		o.delay = time.Duration(d.(float64)) * time.Millisecond
	}
	return nil
}

func (o *testMergeOp) Execute(_ context.Context, _ *types.OperatorInput, _ *types.OperatorOutput) error {
	seq := atomic.AddInt64(&execCounter, 1)
	start := time.Now()
	if o.delay > 0 {
		time.Sleep(o.delay)
	}
	end := time.Now()
	execLogMu.Lock()
	execLog = append(execLog, execEvent{Name: o.name, Seq: seq, Start: start, End: end})
	execLogMu.Unlock()
	return nil
}

// testReorderOp is a Reorder operator that records execution order.
// It consumes and mutates the row set.
type testReorderOp struct {
	pine.ConsumesRowSetMarker
	pine.MutatesRowSetMarker
	name  string
	delay time.Duration
}

func (o *testReorderOp) Init(params map[string]any) error {
	if n, ok := params["name"]; ok {
		o.name = n.(string)
	}
	if d, ok := params["delay_ms"]; ok {
		o.delay = time.Duration(d.(float64)) * time.Millisecond
	}
	return nil
}

func (o *testReorderOp) Execute(_ context.Context, _ *types.OperatorInput, _ *types.OperatorOutput) error {
	seq := atomic.AddInt64(&execCounter, 1)
	start := time.Now()
	if o.delay > 0 {
		time.Sleep(o.delay)
	}
	end := time.Now()
	execLogMu.Lock()
	execLog = append(execLog, execEvent{Name: o.name, Seq: seq, Start: start, End: end})
	execLogMu.Unlock()
	return nil
}

// testObserveOp is an Observe (non-blocking) operator that records execution order.
type testObserveOp struct {
	name  string
	delay time.Duration
}

func (o *testObserveOp) Init(params map[string]any) error {
	if n, ok := params["name"]; ok {
		o.name = n.(string)
	}
	if d, ok := params["delay_ms"]; ok {
		o.delay = time.Duration(d.(float64)) * time.Millisecond
	}
	return nil
}

func (o *testObserveOp) Execute(_ context.Context, _ *types.OperatorInput, _ *types.OperatorOutput) error {
	seq := atomic.AddInt64(&execCounter, 1)
	start := time.Now()
	if o.delay > 0 {
		time.Sleep(o.delay)
	}
	end := time.Now()
	execLogMu.Lock()
	execLog = append(execLog, execEvent{Name: o.name, Seq: seq, Start: start, End: end})
	execLogMu.Unlock()
	return nil
}

// ---------------------------------------------------------------------------
// Register test operators
// ---------------------------------------------------------------------------

func init() {
	registry.Register(types.OperatorSchema{
		Name:        "_test_transform",
		Type:        types.OpTypeTransform,
		Description: "Test transform that records execution order.",
		Params: map[string]types.ParamSpec{
			"name":          {Type: "string", Required: true, Description: "Instance name for event log."},
			"delay_ms":      {Type: "float64", Description: "Execution delay in milliseconds."},
			"_produce":      {Type: "any", Description: "Common fields to write sentinel values for."},
			"_produce_item": {Type: "any", Description: "Item fields to write sentinel values for."},
		},
	}, func() types.Operator { return &testTransformOp{} })

	registry.Register(types.OperatorSchema{
		Name:        "_test_recall",
		Type:        types.OpTypeRecall,
		Description: "Test recall that records execution order and adds items.",
		Params: map[string]types.ParamSpec{
			"name":     {Type: "string", Required: true, Description: "Instance name for event log."},
			"delay_ms": {Type: "float64", Description: "Execution delay in milliseconds."},
			"items":    {Type: "any", Description: "Items to recall."},
		},
	}, func() types.Operator { return &testRecallOp{} })

	registry.Register(types.OperatorSchema{
		Name:        "_test_filter",
		Type:        types.OpTypeFilter,
		Description: "Test filter (barrier) that records execution order.",
		Params: map[string]types.ParamSpec{
			"name":     {Type: "string", Required: true, Description: "Instance name for event log."},
			"delay_ms": {Type: "float64", Description: "Execution delay in milliseconds."},
		},
	}, func() types.Operator { return &testFilterOp{} })

	registry.Register(types.OperatorSchema{
		Name:        "_test_merge",
		Type:        types.OpTypeMerge,
		Description: "Test merge (barrier) that records execution order.",
		Params: map[string]types.ParamSpec{
			"name":     {Type: "string", Required: true, Description: "Instance name for event log."},
			"delay_ms": {Type: "float64", Description: "Execution delay in milliseconds."},
		},
	}, func() types.Operator { return &testMergeOp{} })

	registry.Register(types.OperatorSchema{
		Name:        "_test_reorder",
		Type:        types.OpTypeReorder,
		Description: "Test reorder (barrier) that records execution order.",
		Params: map[string]types.ParamSpec{
			"name":     {Type: "string", Required: true, Description: "Instance name for event log."},
			"delay_ms": {Type: "float64", Description: "Execution delay in milliseconds."},
		},
	}, func() types.Operator { return &testReorderOp{} })

	registry.Register(types.OperatorSchema{
		Name:        "_test_observe",
		Type:        types.OpTypeObserve,
		Description: "Test observe (non-blocking) that records execution order.",
		Params: map[string]types.ParamSpec{
			"name":     {Type: "string", Required: true, Description: "Instance name for event log."},
			"delay_ms": {Type: "float64", Description: "Execution delay in milliseconds."},
		},
	}, func() types.Operator { return &testObserveOp{} })
}

// ---------------------------------------------------------------------------
// Config builder helpers
// ---------------------------------------------------------------------------

func dagTestConfig(operators map[string]any, pipeline []string) map[string]any {
	return map[string]any{
		"_PINEAPPLE_VERSION": pine.Version,
		"pipeline_config": map[string]any{
			"operators": operators,
			"pipeline_map": map[string]any{
				"stage1": map[string]any{"pipeline": pipeline},
			},
		},
		"pipeline_group": map[string]any{
			"main": map[string]any{"pipeline": []string{"stage1"}},
		},
		"flow_contract": map[string]any{},
	}
}

func mustBuildDAGEngine(t *testing.T, cfg map[string]any) *pine.Engine {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := pine.NewEngine(data)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func mustExecute(t *testing.T, engine *pine.Engine, req *pine.Request) *pine.Result {
	t.Helper()
	result, err := engine.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// assertSeqBefore checks that operator 'before' started execution before 'after'.
func assertSeqBefore(t *testing.T, events []execEvent, before, after string) {
	t.Helper()
	evB, okB := eventByName(events, before)
	evA, okA := eventByName(events, after)
	if !okB {
		t.Fatalf("event %q not found in exec log", before)
	}
	if !okA {
		t.Fatalf("event %q not found in exec log", after)
	}
	if evB.Seq >= evA.Seq {
		t.Errorf("expected %q (seq=%d) before %q (seq=%d)", before, evB.Seq, after, evA.Seq)
	}
}

// assertTimesOverlap checks that two operators' execution times overlap (parallel).
func assertTimesOverlap(t *testing.T, events []execEvent, nameA, nameB string) {
	t.Helper()
	evA, okA := eventByName(events, nameA)
	evB, okB := eventByName(events, nameB)
	if !okA {
		t.Fatalf("event %q not found", nameA)
	}
	if !okB {
		t.Fatalf("event %q not found", nameB)
	}
	overlap := evA.Start.Before(evB.End) && evB.Start.Before(evA.End)
	if !overlap {
		t.Errorf("expected %q [%v, %v] and %q [%v, %v] to overlap in time",
			nameA, evA.Start, evA.End, nameB, evB.Start, evB.End)
	}
}

// assertFinishedBefore checks that operator 'a' finished before 'b' started.
func assertFinishedBefore(t *testing.T, events []execEvent, a, b string) {
	t.Helper()
	evA, okA := eventByName(events, a)
	evB, okB := eventByName(events, b)
	if !okA {
		t.Fatalf("event %q not found", a)
	}
	if !okB {
		t.Fatalf("event %q not found", b)
	}
	if !evA.End.Before(evB.Start) || evA.End.Equal(evB.Start) {
		t.Errorf("expected %q (end=%v) to finish before %q (start=%v)",
			a, evA.End, b, evB.Start)
	}
}

// ===========================================================================
// Test 1: Linear chain — A → B → C (strict sequential ordering)
// ===========================================================================

func TestDAGOrder_LinearChain(t *testing.T) {
	resetExecLog()

	cfg := dagTestConfig(
		map[string]any{
			"op_a": map[string]any{
				"type_name": "_test_transform",
				"name":      "op_a",
				"_produce":  []string{"x"},
				"$metadata": map[string]any{
					"common_output": []string{"x"},
				},
			},
			"op_b": map[string]any{
				"type_name": "_test_transform",
				"name":      "op_b",
				"_produce":  []string{"y"},
				"$metadata": map[string]any{
					"common_input":  []string{"x"},
					"common_output": []string{"y"},
				},
			},
			"op_c": map[string]any{
				"type_name": "_test_transform",
				"name":      "op_c",
				"$metadata": map[string]any{
					"common_input": []string{"y"},
				},
			},
		},
		[]string{"op_a", "op_b", "op_c"},
	)
	cfg["flow_contract"] = map[string]any{
		"common_output": []string{"_seq_op_a", "_seq_op_b", "_seq_op_c"},
	}

	engine := mustBuildDAGEngine(t, cfg)
	result := mustExecute(t, engine, &pine.Request{Common: map[string]any{}})
	events := getEvents()

	// Verify strict sequential order via sequence numbers
	assertSeqBefore(t, events, "op_a", "op_b")
	assertSeqBefore(t, events, "op_b", "op_c")

	// Verify seq values are recorded in result
	seqA := result.Common["_seq_op_a"]
	seqB := result.Common["_seq_op_b"]
	seqC := result.Common["_seq_op_c"]
	if seqA == nil || seqB == nil || seqC == nil {
		t.Fatal("missing seq values in result")
	}
	if seqA.(int64) >= seqB.(int64) || seqB.(int64) >= seqC.(int64) {
		t.Errorf("result seq: A=%v B=%v C=%v, expected strict ascending", seqA, seqB, seqC)
	}

	t.Logf("Linear chain order: A(seq=%v) -> B(seq=%v) -> C(seq=%v)", seqA, seqB, seqC)
}

// ===========================================================================
// Test 2: Diamond — A → {B, C} → D (parallel branches)
// ===========================================================================

func TestDAGOrder_DiamondParallel(t *testing.T) {
	resetExecLog()

	cfg := dagTestConfig(
		map[string]any{
			"op_a": map[string]any{
				"type_name": "_test_transform",
				"name":      "op_a",
				"_produce":  []string{"foo"},
				"$metadata": map[string]any{
					"common_output": []string{"foo"},
				},
			},
			"op_b": map[string]any{
				"type_name": "_test_transform",
				"name":      "op_b",
				"delay_ms":  50.0,
				"_produce":  []string{"bar"},
				"$metadata": map[string]any{
					"common_input":  []string{"foo"},
					"common_output": []string{"bar"},
				},
			},
			"op_c": map[string]any{
				"type_name": "_test_transform",
				"name":      "op_c",
				"delay_ms":  50.0,
				"_produce":  []string{"baz"},
				"$metadata": map[string]any{
					"common_input":  []string{"foo"},
					"common_output": []string{"baz"},
				},
			},
			"op_d": map[string]any{
				"type_name": "_test_transform",
				"name":      "op_d",
				"$metadata": map[string]any{
					"common_input": []string{"bar", "baz"},
				},
			},
		},
		[]string{"op_a", "op_b", "op_c", "op_d"},
	)

	engine := mustBuildDAGEngine(t, cfg)
	mustExecute(t, engine, &pine.Request{Common: map[string]any{}})
	events := getEvents()

	// A before B, A before C
	assertSeqBefore(t, events, "op_a", "op_b")
	assertSeqBefore(t, events, "op_a", "op_c")
	// B and C before D
	assertSeqBefore(t, events, "op_b", "op_d")
	assertSeqBefore(t, events, "op_c", "op_d")
	// B and C should run in parallel (time overlap)
	assertTimesOverlap(t, events, "op_b", "op_c")

	evB, _ := eventByName(events, "op_b")
	evC, _ := eventByName(events, "op_c")
	t.Logf("Diamond: B(seq=%d, %v-%v) C(seq=%d, %v-%v) — parallel confirmed",
		evB.Seq, evB.Start.Sub(evB.Start), evB.End.Sub(evB.Start),
		evC.Seq, evC.Start.Sub(evB.Start), evC.End.Sub(evB.Start))
}

// ===========================================================================
// Test 3: Recall parallel — {R1, R2} → Merge → Transform
// ===========================================================================

func TestDAGOrder_RecallParallel(t *testing.T) {
	resetExecLog()

	items1 := []any{
		map[string]any{"item_id": "r1_1", "item_score": 1.0},
		map[string]any{"item_id": "r1_2", "item_score": 2.0},
	}
	items2 := []any{
		map[string]any{"item_id": "r2_1", "item_score": 3.0},
		map[string]any{"item_id": "r2_2", "item_score": 4.0},
	}

	cfg := dagTestConfig(
		map[string]any{
			"recall_1": map[string]any{
				"type_name": "_test_recall",
				"name":      "recall_1",
				"delay_ms":  50.0,
				"items":     items1,
				"$metadata": map[string]any{
					"item_output": []string{"item_id", "item_score"},
				},
			},
			"recall_2": map[string]any{
				"type_name": "_test_recall",
				"name":      "recall_2",
				"delay_ms":  50.0,
				"items":     items2,
				"$metadata": map[string]any{
					"item_output": []string{"item_id", "item_score"},
				},
			},
			"merge": map[string]any{
				"type_name": "_test_merge",
				"name":      "merge",
				"sources":   []string{"recall_1", "recall_2"},
				"$metadata": map[string]any{
					"item_input":  []string{"item_id"},
					"item_output": []string{"item_id", "item_score"},
				},
			},
			"post_transform": map[string]any{
				"type_name": "_test_transform",
				"name":      "post_transform",
				"$metadata": map[string]any{
					"item_input": []string{"item_score"},
				},
			},
		},
		[]string{"recall_1", "recall_2", "merge", "post_transform"},
	)

	engine := mustBuildDAGEngine(t, cfg)
	result := mustExecute(t, engine, &pine.Request{Common: map[string]any{}})
	events := getEvents()

	// Recalls run in parallel
	assertTimesOverlap(t, events, "recall_1", "recall_2")
	// Merge after both recalls
	assertSeqBefore(t, events, "recall_1", "merge")
	assertSeqBefore(t, events, "recall_2", "merge")
	// Transform after merge
	assertSeqBefore(t, events, "merge", "post_transform")

	// Verify items were recalled (4 total from 2 recalls)
	if len(result.Items) != 4 {
		t.Errorf("expected 4 items, got %d", len(result.Items))
	}

	t.Logf("Recall parallel: R1 and R2 overlapped, merge after both, %d items recalled", len(result.Items))
}

// ===========================================================================
// Test 4: Barrier fence — {T1, T2} → Filter → {T3, T4}
// ===========================================================================

func TestDAGOrder_BarrierFence(t *testing.T) {
	resetExecLog()

	// In the ConsumesRowSet/MutatesRowSet model, filter (ConsumesRowSet) waits for
	// row-set writers (recalls), and subsequent ConsumesRowSet ops wait for
	// MutatesRowSet ops. A recall is needed to exercise the fence.
	cfg := dagTestConfig(
		map[string]any{
			"recall": map[string]any{
				"type_name": "_test_recall",
				"name":      "recall",
				"delay_ms":  50.0,
				"items":     []any{map[string]any{"item_id": "x"}},
				"$metadata": map[string]any{
					"item_output": []string{"item_id"},
				},
			},
			"t1": map[string]any{
				"type_name": "_test_transform",
				"name":      "t1",
				"delay_ms":  50.0,
				"_produce":  []string{"score"},
				"$metadata": map[string]any{
					"common_output": []string{"score"},
				},
			},
			"filter": map[string]any{
				"type_name": "_test_filter",
				"name":      "filter",
				"$metadata": map[string]any{
					"item_input": []string{"item_id"},
				},
			},
			"t2": map[string]any{
				"type_name": "_test_transform",
				"name":      "t2",
				"delay_ms":  50.0,
				"_produce":  []string{"score_out"},
				"$metadata": map[string]any{
					"common_input":  []string{"score"},
					"common_output": []string{"score_out"},
				},
			},
		},
		[]string{"recall", "t1", "filter", "t2"},
	)

	engine := mustBuildDAGEngine(t, cfg)
	mustExecute(t, engine, &pine.Request{
		Common: map[string]any{},
		Items:  []map[string]any{},
	})
	events := getEvents()

	// recall || t1 (parallel — t1 is common-only, no row-set interaction)
	assertTimesOverlap(t, events, "recall", "t1")
	// recall before filter (ConsumesRowSet waits for additive writers)
	assertSeqBefore(t, events, "recall", "filter")
	// t1 before t2 (field dependency: score)
	assertSeqBefore(t, events, "t1", "t2")

	t.Logf("BarrierFence: recall||t1 -> filter -> t2 (field dep on t1)")
}

// ===========================================================================
// Test 5: Observe non-blocking — T_writer → {Observe(slow), T_reader}
// ===========================================================================

func TestDAGOrder_ObserveBlocksWriter(t *testing.T) {
	resetExecLog()

	cfg := dagTestConfig(
		map[string]any{
			"writer": map[string]any{
				"type_name": "_test_transform",
				"name":      "writer",
				"$metadata": map[string]any{
					"item_output": []string{"item_score"},
				},
			},
			"observe": map[string]any{
				"type_name": "_test_observe",
				"name":      "observe",
				"delay_ms":  100.0,
				"$metadata": map[string]any{
					"item_input": []string{"item_score"},
				},
			},
			"reader": map[string]any{
				"type_name": "_test_transform",
				"name":      "reader",
				"$metadata": map[string]any{
					"item_input":  []string{"item_score"},
					"item_output": []string{"item_score"},
				},
			},
		},
		[]string{"writer", "observe", "reader"},
	)

	engine := mustBuildDAGEngine(t, cfg)
	mustExecute(t, engine, &pine.Request{
		Common: map[string]any{},
		Items:  []map[string]any{{"item_score": 1.0}},
	})
	events := getEvents()

	// Both observe and reader depend on writer
	assertSeqBefore(t, events, "writer", "observe")
	assertSeqBefore(t, events, "writer", "reader")

	// reader writes item_score, observe reads item_score → WAR edge
	// reader must wait for observe to finish
	assertSeqBefore(t, events, "observe", "reader")

	t.Logf("Observe blocks downstream writer: reader started after observe ended")
}

// ===========================================================================
// Test 6: Multi-barrier — T_a → Filter → T_b → Reorder → T_c
// ===========================================================================

func TestDAGOrder_MultiBarrier(t *testing.T) {
	resetExecLog()

	cfg := dagTestConfig(
		map[string]any{
			"t_a": map[string]any{
				"type_name": "_test_transform",
				"name":      "t_a",
				"_produce":  []string{"score"},
				"$metadata": map[string]any{
					"common_output": []string{"score"},
				},
			},
			"filter": map[string]any{
				"type_name": "_test_filter",
				"name":      "filter",
				"$metadata": map[string]any{
					"item_input": []string{"item_id"},
				},
			},
			"t_b": map[string]any{
				"type_name": "_test_transform",
				"name":      "t_b",
				"_produce":  []string{"rank"},
				"$metadata": map[string]any{
					"common_input":  []string{"score"},
					"common_output": []string{"rank"},
				},
			},
			"reorder": map[string]any{
				"type_name": "_test_reorder",
				"name":      "reorder",
				"$metadata": map[string]any{
					"item_input": []string{"item_id"},
				},
			},
			"t_c": map[string]any{
				"type_name": "_test_transform",
				"name":      "t_c",
				"$metadata": map[string]any{
					"common_input": []string{"rank"},
				},
			},
		},
		[]string{"t_a", "filter", "t_b", "reorder", "t_c"},
	)

	engine := mustBuildDAGEngine(t, cfg)
	mustExecute(t, engine, &pine.Request{
		Common: map[string]any{},
		Items:  []map[string]any{{"item_id": "x"}},
	})
	events := getEvents()

	// Under new row-set semantics:
	// - t_a writes score (common). filter reads item_id (item) + ConsumesRowSet.
	// - t_b reads score (common), writes rank. reorder reads item_id (item) + ConsumesRowSet + MutatesRowSet.
	// - t_c reads rank (common).
	// DAG edges:
	// - t_a → t_b (RAW on score)
	// - filter → reorder (MutatesRowSet serialization: filter is lastMutWriter of _row_set_)
	// - t_b → t_c (RAW on rank)
	// So the invariants are:
	assertSeqBefore(t, events, "t_a", "t_b")
	assertSeqBefore(t, events, "filter", "reorder")
	assertSeqBefore(t, events, "t_b", "t_c")

	t.Logf("Multi row-set mutators: T_a → T_b → T_c (data flow), Filter → Reorder (_row_set_ serialization)")
}

// ===========================================================================
// Test 7: Complex pipeline — all operator types combined
//
// DAG:  recall_1 ──┐
//                  ├── merge ── {t_norm, t_tag} ── filter ── {t_final, t_label} ── reorder ── observe
//       recall_2 ──┘
// ===========================================================================

func TestDAGOrder_ComplexPipeline(t *testing.T) {
	resetExecLog()

	items1 := []any{
		map[string]any{"item_id": "a", "item_score": 10.0},
		map[string]any{"item_id": "b", "item_score": 20.0},
	}
	items2 := []any{
		map[string]any{"item_id": "c", "item_score": 30.0},
		map[string]any{"item_id": "d", "item_score": 40.0},
	}

	cfg := dagTestConfig(
		map[string]any{
			"recall_1": map[string]any{
				"type_name": "_test_recall",
				"name":      "recall_1",
				"delay_ms":  30.0,
				"items":     items1,
				"$metadata": map[string]any{
					"item_output": []string{"item_id", "item_score"},
				},
			},
			"recall_2": map[string]any{
				"type_name": "_test_recall",
				"name":      "recall_2",
				"delay_ms":  30.0,
				"items":     items2,
				"$metadata": map[string]any{
					"item_output": []string{"item_id", "item_score"},
				},
			},
			"merge": map[string]any{
				"type_name": "_test_merge",
				"name":      "merge",
				"sources":   []string{"recall_1", "recall_2"},
				"$metadata": map[string]any{
					"item_input":  []string{"item_id"},
					"item_output": []string{"item_id", "item_score"},
				},
			},
			"t_norm": map[string]any{
				"type_name":     "_test_transform",
				"name":          "t_norm",
				"delay_ms":      30.0,
				"_produce_item": []string{"item_norm"},
				"$metadata": map[string]any{
					"item_input":  []string{"item_score"},
					"item_output": []string{"item_norm"},
				},
			},
			"t_tag": map[string]any{
				"type_name":     "_test_transform",
				"name":          "t_tag",
				"delay_ms":      30.0,
				"_produce_item": []string{"item_tag"},
				"$metadata": map[string]any{
					"item_input":  []string{"item_id"},
					"item_output": []string{"item_tag"},
				},
			},
			"filter": map[string]any{
				"type_name": "_test_filter",
				"name":      "filter",
				"$metadata": map[string]any{
					"item_input": []string{"item_norm"},
				},
			},
			"t_final": map[string]any{
				"type_name":     "_test_transform",
				"name":          "t_final",
				"delay_ms":      30.0,
				"_produce_item": []string{"item_final"},
				"$metadata": map[string]any{
					"item_input":  []string{"item_norm"},
					"item_output": []string{"item_final"},
				},
			},
			"t_label": map[string]any{
				"type_name":     "_test_transform",
				"name":          "t_label",
				"delay_ms":      30.0,
				"_produce_item": []string{"item_label"},
				"$metadata": map[string]any{
					"item_input":  []string{"item_tag"},
					"item_output": []string{"item_label"},
				},
			},
			"reorder": map[string]any{
				"type_name": "_test_reorder",
				"name":      "reorder",
				"$metadata": map[string]any{
					"item_input": []string{"item_final"},
				},
			},
			"observe": map[string]any{
				"type_name": "_test_observe",
				"name":      "observe",
				"$metadata": map[string]any{
					"item_input": []string{"item_final"},
				},
			},
		},
		[]string{"recall_1", "recall_2", "merge", "t_norm", "t_tag", "filter", "t_final", "t_label", "reorder", "observe"},
	)

	engine := mustBuildDAGEngine(t, cfg)
	result := mustExecute(t, engine, &pine.Request{Common: map[string]any{}})
	events := getEvents()

	if len(events) != 10 {
		t.Fatalf("expected 10 events, got %d", len(events))
	}

	// Phase 1: Recalls parallel
	assertTimesOverlap(t, events, "recall_1", "recall_2")

	// Phase 2: Merge — after both recalls (sources + _row_set_ consumers)
	assertSeqBefore(t, events, "recall_1", "merge")
	assertSeqBefore(t, events, "recall_2", "merge")

	// Phase 3: t_norm and t_tag parallel — after merge (RAW on item_score/item_id)
	assertSeqBefore(t, events, "merge", "t_norm")
	assertSeqBefore(t, events, "merge", "t_tag")
	assertTimesOverlap(t, events, "t_norm", "t_tag")

	// Phase 4: Under new semantics, filter depends on t_norm (RAW item_norm)
	// and merge (_row_set_ mut writer), NOT on t_tag.
	assertSeqBefore(t, events, "t_norm", "filter")

	// Phase 5: t_final depends on t_norm (RAW item_norm), not on filter.
	// t_label depends on t_tag (RAW item_tag), not on filter.
	assertSeqBefore(t, events, "t_norm", "t_final")
	assertSeqBefore(t, events, "t_tag", "t_label")

	// Phase 6: Reorder depends on t_final (RAW item_final) + filter (_row_set_ serialization)
	assertSeqBefore(t, events, "t_final", "reorder")
	assertSeqBefore(t, events, "filter", "reorder")

	// Phase 7: Observe depends on t_final (RAW on item_final), non-blocking
	assertSeqBefore(t, events, "t_final", "observe")

	// Verify 4 items survived (no actual filtering)
	if len(result.Items) != 4 {
		t.Errorf("expected 4 items, got %d", len(result.Items))
	}

	// Print execution timeline
	t.Logf("Complex pipeline execution timeline:")
	for _, ev := range events {
		t.Logf("  seq=%2d  %-15s  duration=%v", ev.Seq, ev.Name, ev.End.Sub(ev.Start))
	}
}

// ===========================================================================
// Test 8: Repeat stability — run complex pipeline 100 times
// ===========================================================================

func TestDAGOrder_RepeatStability(t *testing.T) {
	items1 := []any{
		map[string]any{"item_id": "a", "item_score": 1.0},
	}
	items2 := []any{
		map[string]any{"item_id": "b", "item_score": 2.0},
	}

	cfg := dagTestConfig(
		map[string]any{
			"r1": map[string]any{
				"type_name": "_test_recall",
				"name":      "r1",
				"items":     items1,
				"$metadata": map[string]any{
					"item_output": []string{"item_id", "item_score"},
				},
			},
			"r2": map[string]any{
				"type_name": "_test_recall",
				"name":      "r2",
				"items":     items2,
				"$metadata": map[string]any{
					"item_output": []string{"item_id", "item_score"},
				},
			},
			"merge": map[string]any{
				"type_name": "_test_merge",
				"name":      "merge",
				"sources":   []string{"r1", "r2"},
				"$metadata": map[string]any{
					"item_input":  []string{"item_id"},
					"item_output": []string{"item_id", "item_score"},
				},
			},
			"t_a": map[string]any{
				"type_name":     "_test_transform",
				"name":          "t_a",
				"_produce_item": []string{"item_norm"},
				"$metadata": map[string]any{
					"item_input":  []string{"item_score"},
					"item_output": []string{"item_norm"},
				},
			},
			"t_b": map[string]any{
				"type_name":     "_test_transform",
				"name":          "t_b",
				"_produce_item": []string{"item_tag"},
				"$metadata": map[string]any{
					"item_input":  []string{"item_id"},
					"item_output": []string{"item_tag"},
				},
			},
			"filter": map[string]any{
				"type_name": "_test_filter",
				"name":      "filter",
				"$metadata": map[string]any{
					"item_input": []string{"item_norm"},
				},
			},
			"reorder": map[string]any{
				"type_name": "_test_reorder",
				"name":      "reorder",
				"$metadata": map[string]any{
					"item_input": []string{"item_norm"},
				},
			},
		},
		[]string{"r1", "r2", "merge", "t_a", "t_b", "filter", "reorder"},
	)

	engine := mustBuildDAGEngine(t, cfg)
	req := &pine.Request{Common: map[string]any{}}

	const iterations = 100
	for i := 0; i < iterations; i++ {
		resetExecLog()
		mustExecute(t, engine, req)
		events := getEvents()

		if len(events) != 7 {
			t.Fatalf("iteration %d: expected 7 events, got %d", i, len(events))
		}

		// Verify core ordering invariants
		evR1, _ := eventByName(events, "r1")
		evR2, _ := eventByName(events, "r2")
		evMerge, _ := eventByName(events, "merge")
		evTA, _ := eventByName(events, "t_a")
		_, _ = eventByName(events, "t_b")
		evFilter, _ := eventByName(events, "filter")
		evReorder, _ := eventByName(events, "reorder")

		// Recalls before merge
		if evR1.Seq >= evMerge.Seq {
			t.Errorf("iteration %d: r1(seq=%d) not before merge(seq=%d)", i, evR1.Seq, evMerge.Seq)
		}
		if evR2.Seq >= evMerge.Seq {
			t.Errorf("iteration %d: r2(seq=%d) not before merge(seq=%d)", i, evR2.Seq, evMerge.Seq)
		}
		// Merge before transforms (RAW on item fields)
		if evMerge.Seq >= evTA.Seq {
			t.Errorf("iteration %d: merge(seq=%d) not before t_a(seq=%d)", i, evMerge.Seq, evTA.Seq)
		}
		// t_a before filter (filter reads item_norm which t_a writes)
		if evTA.Seq >= evFilter.Seq {
			t.Errorf("iteration %d: t_a(seq=%d) not before filter(seq=%d)", i, evTA.Seq, evFilter.Seq)
		}
		// Filter before reorder (MutatesRowSet serialization)
		if evFilter.Seq >= evReorder.Seq {
			t.Errorf("iteration %d: filter(seq=%d) not before reorder(seq=%d)", i, evFilter.Seq, evReorder.Seq)
		}
	}

	t.Logf("Repeat stability: %d iterations all passed ordering invariants", iterations)
}

// ===========================================================================
// Test 9: Transform → Recall dependency — recalls wait for transform,
//         then run in parallel with each other
// ===========================================================================

func TestDAGOrder_TransformThenRecallParallel(t *testing.T) {
	resetExecLog()

	items1 := []any{map[string]any{"item_id": "a", "item_score": 1.0}}
	items2 := []any{map[string]any{"item_id": "b", "item_score": 2.0}}

	cfg := dagTestConfig(
		map[string]any{
			"compute_vec": map[string]any{
				"type_name": "_test_transform",
				"name":      "compute_vec",
				"_produce":  []string{"user_vec"},
				"$metadata": map[string]any{
					"common_input":  []string{"user_id"},
					"common_output": []string{"user_vec"},
				},
			},
			"recall_hot": map[string]any{
				"type_name": "_test_recall",
				"name":      "recall_hot",
				"delay_ms":  50.0,
				"items":     items1,
				"$metadata": map[string]any{
					"common_input": []string{"user_vec"},
					"item_output":  []string{"item_id", "item_score"},
				},
			},
			"recall_ann": map[string]any{
				"type_name": "_test_recall",
				"name":      "recall_ann",
				"delay_ms":  50.0,
				"items":     items2,
				"$metadata": map[string]any{
					"common_input": []string{"user_vec"},
					"item_output":  []string{"item_id", "item_score"},
				},
			},
			"merge": map[string]any{
				"type_name": "_test_merge",
				"name":      "merge",
				"sources":   []string{"recall_hot", "recall_ann"},
				"$metadata": map[string]any{
					"item_input":  []string{"item_id"},
					"item_output": []string{"item_id", "item_score"},
				},
			},
		},
		[]string{"compute_vec", "recall_hot", "recall_ann", "merge"},
	)

	engine := mustBuildDAGEngine(t, cfg)
	mustExecute(t, engine, &pine.Request{Common: map[string]any{"user_id": "u1"}})
	events := getEvents()

	// compute_vec must finish before either recall starts
	assertSeqBefore(t, events, "compute_vec", "recall_hot")
	assertSeqBefore(t, events, "compute_vec", "recall_ann")
	assertFinishedBefore(t, events, "compute_vec", "recall_hot")
	assertFinishedBefore(t, events, "compute_vec", "recall_ann")

	// Both recalls run in parallel (each sleeps 50ms, should overlap)
	assertTimesOverlap(t, events, "recall_hot", "recall_ann")

	// Merge after both recalls
	assertSeqBefore(t, events, "recall_hot", "merge")
	assertSeqBefore(t, events, "recall_ann", "merge")

	evVec, _ := eventByName(events, "compute_vec")
	evHot, _ := eventByName(events, "recall_hot")
	evAnn, _ := eventByName(events, "recall_ann")
	t.Logf("Transform→Recall: compute_vec(seq=%d) -> {recall_hot(seq=%d), recall_ann(seq=%d)} parallel",
		evVec.Seq, evHot.Seq, evAnn.Seq)
	t.Logf("  recall_hot: %v-%v", evHot.Start.Sub(evVec.End), evHot.End.Sub(evVec.End))
	t.Logf("  recall_ann: %v-%v", evAnn.Start.Sub(evVec.End), evAnn.End.Sub(evVec.End))
}

// ===========================================================================
// Test 10: Recalls depending on DIFFERENT transforms — staggered parallelism
//
// DAG:  t_a → t_b → recall_b
//         └─────── recall_a (can start after t_a, parallel with t_b)
// ===========================================================================

func TestDAGOrder_RecallsDependOnDifferentTransforms(t *testing.T) {
	resetExecLog()

	items1 := []any{map[string]any{"item_id": "a"}}
	items2 := []any{map[string]any{"item_id": "b"}}

	cfg := dagTestConfig(
		map[string]any{
			"t_a": map[string]any{
				"type_name": "_test_transform",
				"name":      "t_a",
				"_produce":  []string{"feature_x"},
				"$metadata": map[string]any{
					"common_output": []string{"feature_x"},
				},
			},
			"t_b": map[string]any{
				"type_name": "_test_transform",
				"name":      "t_b",
				"delay_ms":  50.0,
				"_produce":  []string{"feature_y"},
				"$metadata": map[string]any{
					"common_input":  []string{"feature_x"},
					"common_output": []string{"feature_y"},
				},
			},
			"recall_a": map[string]any{
				"type_name": "_test_recall",
				"name":      "recall_a",
				"delay_ms":  50.0,
				"items":     items1,
				"$metadata": map[string]any{
					"common_input": []string{"feature_x"},
					"item_output":  []string{"item_id"},
				},
			},
			"recall_b": map[string]any{
				"type_name": "_test_recall",
				"name":      "recall_b",
				"items":     items2,
				"$metadata": map[string]any{
					"common_input": []string{"feature_y"},
					"item_output":  []string{"item_id"},
				},
			},
		},
		[]string{"t_a", "t_b", "recall_a", "recall_b"},
	)

	engine := mustBuildDAGEngine(t, cfg)
	mustExecute(t, engine, &pine.Request{Common: map[string]any{}})
	events := getEvents()

	// t_a before everything
	assertSeqBefore(t, events, "t_a", "t_b")
	assertSeqBefore(t, events, "t_a", "recall_a")

	// recall_a and t_b can run in parallel (both depend only on t_a)
	assertTimesOverlap(t, events, "t_b", "recall_a")

	// recall_b depends on t_b (RAW on feature_y)
	assertSeqBefore(t, events, "t_b", "recall_b")

	evTB, _ := eventByName(events, "t_b")
	evRA, _ := eventByName(events, "recall_a")
	t.Logf("Staggered: t_b(seq=%d) and recall_a(seq=%d) run in parallel after t_a",
		evTB.Seq, evRA.Seq)
}

// ===========================================================================
// Test 11: Independent recall parallel with transform→recall chain
//
// recall_a has no deps.
// transform_b reads request field, writes bbb.
// recall_c and recall_d both read bbb.
//
// Expected timeline:
//   recall_a(50ms) ─────────────────────────
//   transform_b(0ms) → {recall_c(50ms), recall_d(50ms)}
//   (recall_a || transform_b, then recall_a || recall_c || recall_d)
// ===========================================================================

func TestDAGOrder_IndependentRecallWithTransformRecallChain(t *testing.T) {
	resetExecLog()

	itemsA := []any{map[string]any{"item_id": "a1"}}
	itemsC := []any{map[string]any{"item_id": "c1"}}
	itemsD := []any{map[string]any{"item_id": "d1"}}

	cfg := dagTestConfig(
		map[string]any{
			"recall_a": map[string]any{
				"type_name": "_test_recall",
				"name":      "recall_a",
				"delay_ms":  50.0,
				"items":     itemsA,
				"$metadata": map[string]any{
					"item_output": []string{"item_id"},
				},
			},
			"transform_b": map[string]any{
				"type_name": "_test_transform",
				"name":      "transform_b",
				"delay_ms":  1.0,
				"_produce":  []string{"bbb"},
				"$metadata": map[string]any{
					"common_input":  []string{"req_field"},
					"common_output": []string{"bbb"},
				},
			},
			"recall_c": map[string]any{
				"type_name": "_test_recall",
				"name":      "recall_c",
				"delay_ms":  50.0,
				"items":     itemsC,
				"$metadata": map[string]any{
					"common_input": []string{"bbb"},
					"item_output":  []string{"item_id"},
				},
			},
			"recall_d": map[string]any{
				"type_name": "_test_recall",
				"name":      "recall_d",
				"delay_ms":  50.0,
				"items":     itemsD,
				"$metadata": map[string]any{
					"common_input": []string{"bbb"},
					"item_output":  []string{"item_id"},
				},
			},
		},
		[]string{"recall_a", "transform_b", "recall_c", "recall_d"},
	)

	engine := mustBuildDAGEngine(t, cfg)
	result := mustExecute(t, engine, &pine.Request{Common: map[string]any{"req_field": "hello"}})
	events := getEvents()

	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}

	evA, _ := eventByName(events, "recall_a")
	evB, _ := eventByName(events, "transform_b")
	evC, _ := eventByName(events, "recall_c")
	evD, _ := eventByName(events, "recall_d")

	// 1. recall_a and transform_b start independently (both have no preds)
	//    recall_a sleeps 50ms, transform_b is instant -> transform_b finishes first
	assertTimesOverlap(t, events, "recall_a", "transform_b")

	// 2. transform_b finishes before recall_c and recall_d start
	assertFinishedBefore(t, events, "transform_b", "recall_c")
	assertFinishedBefore(t, events, "transform_b", "recall_d")

	// 3. recall_c and recall_d run in parallel (both depend only on transform_b)
	assertTimesOverlap(t, events, "recall_c", "recall_d")

	// 4. recall_a overlaps with recall_c and recall_d
	//    (recall_a takes 50ms starting from time 0; recall_c/d start after transform_b ~0ms)
	assertTimesOverlap(t, events, "recall_a", "recall_c")
	assertTimesOverlap(t, events, "recall_a", "recall_d")

	// 5. Verify all 3 items were recalled
	if len(result.Items) != 3 {
		t.Errorf("expected 3 items (a1+c1+d1), got %d", len(result.Items))
	}

	t.Logf("Timeline (relative to recall_a start):")
	t.Logf("  recall_a(seq=%d):     [%6v, %6v]  (no deps, 50ms)",
		evA.Seq, evA.Start.Sub(evA.Start), evA.End.Sub(evA.Start))
	t.Logf("  transform_b(seq=%d):  [%6v, %6v]  (no deps, 1ms)",
		evB.Seq, evB.Start.Sub(evA.Start), evB.End.Sub(evA.Start))
	t.Logf("  recall_c(seq=%d):     [%6v, %6v]  (after transform_b, 50ms)",
		evC.Seq, evC.Start.Sub(evA.Start), evC.End.Sub(evA.Start))
	t.Logf("  recall_d(seq=%d):     [%6v, %6v]  (after transform_b, 50ms)",
		evD.Seq, evD.Start.Sub(evA.Start), evD.End.Sub(evA.Start))
}

// ===========================================================================
// Benchmark: DAG scheduling overhead with noop operators
// ===========================================================================

func BenchmarkDAGSchedulingOverhead_5ops(b *testing.B) {
	cfg := dagTestConfig(
		map[string]any{
			"op1": map[string]any{"type_name": "_test_transform", "name": "op1", "_produce": []string{"a"}, "$metadata": map[string]any{"common_output": []string{"a"}}},
			"op2": map[string]any{"type_name": "_test_transform", "name": "op2", "_produce": []string{"b"}, "$metadata": map[string]any{"common_input": []string{"a"}, "common_output": []string{"b"}}},
			"op3": map[string]any{"type_name": "_test_transform", "name": "op3", "_produce": []string{"c"}, "$metadata": map[string]any{"common_input": []string{"a"}, "common_output": []string{"c"}}},
			"op4": map[string]any{"type_name": "_test_transform", "name": "op4", "_produce": []string{"d"}, "$metadata": map[string]any{"common_input": []string{"b", "c"}, "common_output": []string{"d"}}},
			"op5": map[string]any{"type_name": "_test_transform", "name": "op5", "$metadata": map[string]any{"common_input": []string{"d"}}},
		},
		[]string{"op1", "op2", "op3", "op4", "op5"},
	)
	data, _ := json.Marshal(cfg)
	engine, _ := pine.NewEngine(data)
	req := &pine.Request{Common: map[string]any{}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resetExecLog()
		_, _ = engine.Execute(context.Background(), req)
	}
}

func BenchmarkDAGSchedulingOverhead_10ops(b *testing.B) {
	ops := make(map[string]any)
	pipeline := make([]string, 10)
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("op%d", i)
		pipeline[i] = name
		meta := map[string]any{}
		if i > 0 {
			meta["common_input"] = []string{fmt.Sprintf("f%d", i-1)}
		}
		meta["common_output"] = []string{fmt.Sprintf("f%d", i)}
		ops[name] = map[string]any{
			"type_name": "_test_transform",
			"name":      name,
			"_produce":  []string{fmt.Sprintf("f%d", i)},
			"$metadata": meta,
		}
	}
	cfg := dagTestConfig(ops, pipeline)
	data, _ := json.Marshal(cfg)
	engine, _ := pine.NewEngine(data)
	req := &pine.Request{Common: map[string]any{}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resetExecLog()
		_, _ = engine.Execute(context.Background(), req)
	}
}
