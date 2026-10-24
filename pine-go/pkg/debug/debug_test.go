package debug_test

import (
	"context"
	"testing"

	pinedebug "github.com/Liam0205/pineapple/pine-go/pkg/debug"
)

type event struct {
	meta pinedebug.NodeMeta
	kind string
	data any
}

type collector struct{ events []event }

func (c *collector) Emit(meta pinedebug.NodeMeta, kind string, data any) {
	c.events = append(c.events, event{meta, kind, data})
}

type panicEmitter struct{}

func (panicEmitter) Emit(pinedebug.NodeMeta, string, any) { panic("broken exporter") }

func TestDisabledDoesNotBuildPayloadOrAllocate(t *testing.T) {
	ctx := context.Background()
	if got := pinedebug.WithEmitter(ctx, nil); got != ctx {
		t.Fatal("nil emitter must preserve the original context")
	}
	builds := 0
	allocs := testing.AllocsPerRun(100, func() {
		if r := pinedebug.FromContext(ctx); r != nil {
			builds++
			r.Emit("unexpected", map[string]any{"items": []int{1, 2, 3}})
		}
	})
	if builds != 0 || allocs != 0 {
		t.Fatalf("disabled capture built %d payloads and allocated %g times", builds, allocs)
	}
	var nilRecorder *pinedebug.Recorder
	nilRecorder.Emit("ignored", nil)
	if got := nilRecorder.BindNode(ctx, pinedebug.NodeMeta{Operator: "ignored"}); got != ctx {
		t.Fatal("disabled node binding must preserve context")
	}
	var zero pinedebug.Recorder
	zero.Emit("ignored", nil)
	if got := zero.BindNode(ctx, pinedebug.NodeMeta{}); got != ctx {
		t.Fatal("zero recorder must not enable capture")
	}
}

func TestNodeBindingPreservesContextAndDoesNotMutateParent(t *testing.T) {
	type key struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "request"))
	defer cancel()
	c := &collector{}
	ctx := pinedebug.WithEmitter(parent, c)
	r := pinedebug.FromContext(ctx)
	first := r.BindNode(ctx, pinedebug.NodeMeta{Operator: "recall", Type: "recall_test", SubFlow: "candidates"})
	second := r.BindNode(ctx, pinedebug.NodeMeta{Operator: "rank", Type: "transform_test"})
	pinedebug.FromContext(first).Emit("hits", 7)
	pinedebug.FromContext(second).Emit("scores", 11)
	r.Emit("request", 13)
	if len(c.events) != 3 {
		t.Fatalf("synchronous delivery: got %d events", len(c.events))
	}
	want := []event{
		{pinedebug.NodeMeta{Operator: "recall", Type: "recall_test", SubFlow: "candidates"}, "hits", 7},
		{pinedebug.NodeMeta{Operator: "rank", Type: "transform_test"}, "scores", 11},
		{pinedebug.NodeMeta{}, "request", 13},
	}
	for i := range want {
		if c.events[i] != want[i] {
			t.Errorf("event %d: got %+v, want %+v", i, c.events[i], want[i])
		}
	}
	cancel()
	if first.Err() != context.Canceled || first.Value(key{}) != "request" {
		t.Fatal("binding lost request cancellation or values")
	}
}

func TestEmitterPanicDoesNotEscapeIntoBusinessCode(t *testing.T) {
	ctx := pinedebug.WithEmitter(context.Background(), panicEmitter{})
	pinedebug.FromContext(ctx).Emit("payload", "test")
}

func BenchmarkDisabled(b *testing.B) {
	type key int
	ctx := context.Background()
	for i := range 8 {
		ctx = context.WithValue(ctx, key(i), i)
	}
	b.ReportAllocs()
	for b.Loop() {
		if r := pinedebug.FromContext(ctx); r != nil {
			r.Emit("unused", "unused")
		}
	}
}
