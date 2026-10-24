package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Liam0205/pineapple/pine-go/internal/config"
	"github.com/Liam0205/pineapple/pine-go/internal/dataframe"
	"github.com/Liam0205/pineapple/pine-go/internal/types"
	pinedebug "github.com/Liam0205/pineapple/pine-go/pkg/debug"
)

type debugEvent struct {
	meta pinedebug.NodeMeta
	n    int
}

type debugEvents struct {
	mu     sync.Mutex
	events []debugEvent
	items  []debugEvent
}

func (c *debugEvents) Emit(meta pinedebug.NodeMeta, stage string, data any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if stage == "item" {
		c.items = append(c.items, debugEvent{meta, data.(int)})
		return
	}
	c.events = append(c.events, debugEvent{meta, data.(int)})
}

type debugRequestKey struct{}

type debugTestOp struct {
	mu        sync.Mutex
	sizes     []int
	field     string
	onExecute func(context.Context, int, bool) error
}

func (*debugTestOp) Init(map[string]any) error { return nil }
func (o *debugTestOp) Execute(ctx context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	if ctx.Value(debugRequestKey{}) != "kept" || ctx.Done() == nil {
		return fmt.Errorf("node context lost request values or cancellation")
	}
	n := in.ItemCount()
	o.mu.Lock()
	o.sizes = append(o.sizes, n)
	o.mu.Unlock()
	r := pinedebug.FromContext(ctx)
	if o.onExecute != nil {
		if err := o.onExecute(ctx, n, r != nil); err != nil {
			return err
		}
	}
	if r != nil {
		r.Emit("items", n)
	}
	for i := range n {
		field := o.field
		if field == "" {
			field = "result"
		}
		value := in.Item(i, "value")
		if r != nil {
			r.Emit("item", value)
		}
		out.SetItem(i, field, value)
	}
	return nil
}

func TestRequestDebugPreservesParallelShardsAndNodeMetadata(t *testing.T) {
	op := &debugTestOp{}
	meta := config.Metadata{ItemInput: []string{"value"}, ItemOutput: []string{"result"}}
	cop := &CompiledOperator{
		Name: "rank", Instance: op,
		Config: config.OperatorConfig{
			TypeName: "transform_debug_test", OperatorType: "Transform", DataParallel: 3,
			Meta: meta, InputSpec: config.ComputeInputFieldSpec(meta, nil, nil, nil, nil, nil),
		},
	}
	plan := buildPlan(t, []string{"rank"}, map[string]*CompiledOperator{"rank": cop})
	plan.Graph.Nodes[0].SubFlow = "scoring"
	for _, enabled := range []bool{false, true, false} {
		ctx := context.WithValue(context.Background(), debugRequestKey{}, "kept")
		capture := &debugEvents{}
		if enabled {
			ctx = pinedebug.WithEmitter(ctx, capture)
		}
		items := []map[string]any{{"value": 1}, {"value": 2}, {"value": 3}, {"value": 4}, {"value": 5}, {"value": 6}}
		frame := dataframe.New(nil, items)
		op.sizes = nil
		_, traces, err := Run(ctx, plan, frame, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		for i := range 6 {
			if got := frame.Item(i, "result"); got != i+1 {
				t.Fatalf("result[%d]=%v", i, got)
			}
		}
		if len(traces) != 1 || traces[0].InputSnapshot != nil || traces[0].OutputSnapshot != nil {
			t.Fatal("request recorder must not turn on static snapshots")
		}
		if len(op.sizes) != 3 {
			t.Fatalf("debug=%v: execution lost parallel shards: %v", enabled, op.sizes)
		}
		for _, size := range op.sizes {
			if size != 2 {
				t.Fatalf("debug=%v: shard size=%d, want 2", enabled, size)
			}
		}
		if enabled {
			want := debugEvent{pinedebug.NodeMeta{Operator: "rank", Type: "transform_debug_test", SubFlow: "scoring"}, 2}
			if len(capture.events) != 3 {
				t.Fatalf("shard events: got %+v, want 3 events", capture.events)
			}
			for _, event := range capture.events {
				if event != want {
					t.Fatalf("shard event: got %+v, want %+v", event, want)
				}
			}
			if len(capture.items) != 6 {
				t.Fatalf("captured items: got %+v, want 6 items", capture.items)
			}
			seen := make(map[int]bool)
			for _, item := range capture.items {
				if item.meta != want.meta || item.n < 1 || item.n > 6 || seen[item.n] {
					t.Fatalf("lost, duplicated, or incorrectly scoped item: %+v", capture.items)
				}
				seen[item.n] = true
			}
		} else if len(capture.events) != 0 || len(capture.items) != 0 {
			t.Fatalf("ordinary request captured events: %+v", capture.events)
		}
		if cop.Config.DataParallel != 3 {
			t.Fatal("request changed shared execution plan")
		}
	}
}

func TestRequestDebugSeparatesConcurrentNodesAndRequests(t *testing.T) {
	const requests = 8
	started := make(chan struct{}, 2*requests)
	release := make(chan struct{})
	parent, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	onExecute := func(ctx context.Context, _ int, _ bool) error {
		started <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ops := map[string]*CompiledOperator{}
	for _, name := range []string{"first", "second"} {
		meta := config.Metadata{ItemInput: []string{"value"}, ItemOutput: []string{name}}
		ops[name] = &CompiledOperator{
			Name: name, Instance: &debugTestOp{field: name, onExecute: onExecute},
			Config: config.OperatorConfig{TypeName: "transform_debug_test", OperatorType: "Transform", Meta: meta,
				InputSpec: config.ComputeInputFieldSpec(meta, nil, nil, nil, nil, nil)},
		}
	}
	plan := buildPlan(t, []string{"first", "second"}, ops)
	var wg sync.WaitGroup
	for count := 1; count <= requests; count++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			capture := &debugEvents{}
			ctx := pinedebug.WithEmitter(context.WithValue(parent, debugRequestKey{}, "kept"), capture)
			items := make([]map[string]any, n)
			for i := range items {
				items[i] = map[string]any{"value": i}
			}
			_, _, err := Run(ctx, plan, dataframe.New(nil, items), nil, nil)
			if err != nil {
				t.Error(err)
				return
			}
			seen := map[string]bool{}
			for _, event := range capture.events {
				if event.n != n || event.meta.Type != "transform_debug_test" {
					t.Errorf("cross-request data/meta: %+v for request with %d items", event, n)
				}
				seen[event.meta.Operator] = true
			}
			if len(capture.events) != 2 || !seen["first"] || !seen["second"] {
				t.Errorf("node scopes not isolated: %+v", capture.events)
			}
		}(count)
	}
	for range 2 * requests {
		select {
		case <-started:
		case <-parent.Done():
			t.Error("independent nodes did not overlap before deadline")
		}
	}
	close(release)
	wg.Wait()
}

func TestRequestDebugKeepsConcurrentMixedRequestsParallel(t *testing.T) {
	type requestKey struct{}
	type invocation struct {
		size     int
		recorder bool
	}
	type requestCalls struct {
		mu    sync.Mutex
		calls []invocation
	}
	const requests = 8
	started := make(chan struct{}, 3*requests)
	release := make(chan struct{})
	parent, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	op := &debugTestOp{onExecute: func(ctx context.Context, size int, recorder bool) error {
		state := ctx.Value(requestKey{}).(*requestCalls)
		state.mu.Lock()
		state.calls = append(state.calls, invocation{size, recorder})
		state.mu.Unlock()
		started <- struct{}{}
		// Keep every shard inside Execute until all shards of all requests overlap.
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	meta := config.Metadata{ItemInput: []string{"value"}, ItemOutput: []string{"result"}}
	cop := &CompiledOperator{
		Name: "rank", Instance: op,
		Config: config.OperatorConfig{
			TypeName: "transform_debug_test", OperatorType: "Transform", DataParallel: 3,
			Meta: meta, InputSpec: config.ComputeInputFieldSpec(meta, nil, nil, nil, nil, nil),
		},
	}
	plan := buildPlan(t, []string{"rank"}, map[string]*CompiledOperator{"rank": cop})
	var wg sync.WaitGroup
	for request := range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, enabled := request+6, request%2 == 0
			state := &requestCalls{}
			ctx := context.WithValue(parent, debugRequestKey{}, "kept")
			ctx = context.WithValue(ctx, requestKey{}, state)
			capture := &debugEvents{}
			if enabled {
				ctx = pinedebug.WithEmitter(ctx, capture)
			}
			items := make([]map[string]any, n)
			for i := range items {
				items[i] = map[string]any{"value": i}
			}
			frame := dataframe.New(nil, items)
			if _, _, err := Run(ctx, plan, frame, nil, nil); err != nil {
				t.Errorf("request %d: %v", request, err)
				return
			}
			const wantCalls = 3
			if len(state.calls) != wantCalls {
				t.Errorf("request %d debug=%v: calls=%v, want %d", request, enabled, state.calls, wantCalls)
			}
			total := 0
			for _, call := range state.calls {
				total += call.size
				if call.recorder != enabled || call.size == 0 {
					t.Errorf("request %d debug=%v: unexpected invocation %+v", request, enabled, call)
				}
			}
			if total != n {
				t.Errorf("request %d: processed %d items, want %d", request, total, n)
			}
			for i := range n {
				if got := frame.Item(i, "result"); got != i {
					t.Errorf("request %d result[%d]=%v, want %d", request, i, got, i)
				}
			}
			if enabled {
				wantMeta := pinedebug.NodeMeta{Operator: "rank", Type: "transform_debug_test"}
				totalItems := 0
				for _, event := range capture.events {
					totalItems += event.n
					if event.meta != wantMeta {
						t.Errorf("request %d: event meta=%+v, want %+v", request, event.meta, wantMeta)
					}
				}
				if len(capture.events) != wantCalls || totalItems != n {
					t.Errorf("request %d: events=%+v, want %d events covering %d items", request, capture.events, wantCalls, n)
				}
			} else if len(capture.events) != 0 {
				t.Errorf("ordinary request %d captured events: %+v", request, capture.events)
			}
		}()
	}
	for range 3 * requests {
		select {
		case <-started:
		case <-parent.Done():
			t.Error("request shards did not overlap before deadline")
		}
	}
	close(release)
	wg.Wait()
	if cop.Config.DataParallel != 3 {
		t.Fatal("concurrent requests changed the shared parallel plan")
	}
}

func TestRequestDebugParallelShardsKeepCallerCancellation(t *testing.T) {
	started := make(chan struct{}, 3)
	op := &debugTestOp{onExecute: func(ctx context.Context, _ int, _ bool) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}}
	meta := config.Metadata{ItemInput: []string{"value"}, ItemOutput: []string{"result"}}
	cop := &CompiledOperator{
		Name: "rank", Instance: op,
		Config: config.OperatorConfig{
			TypeName: "transform_debug_test", OperatorType: "Transform", DataParallel: 3,
			Meta: meta, InputSpec: config.ComputeInputFieldSpec(meta, nil, nil, nil, nil, nil),
		},
	}
	plan := buildPlan(t, []string{"rank"}, map[string]*CompiledOperator{"rank": cop})
	parent, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx := pinedebug.WithEmitter(context.WithValue(parent, debugRequestKey{}, "kept"), &debugEvents{})
	frame := dataframe.New(nil, []map[string]any{{"value": 1}, {"value": 2}, {"value": 3}})
	done := make(chan error, 1)
	go func() {
		_, _, err := Run(ctx, plan, frame, nil, nil)
		done <- err
	}()
	for range 3 {
		select {
		case <-started:
		case <-parent.Done():
			t.Fatal("parallel shards did not start before deadline")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error=%v, want caller cancellation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("parallel shards did not stop after caller cancellation")
	}
}
