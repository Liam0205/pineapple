// Package debug provides an optional, request-local event interface for operators.
// It owns no session, buffer, serializer, worker, or external client. Applications
// supply an Emitter through WithEmitter; without one FromContext returns nil.
// Build debug-only data inside that nil guard, outside the candidate loop.
//
// The runtime binds immutable node metadata before calling each operator. An
// injected recorder preserves normal execution, including data_parallel shards
// and independent DAG nodes. Shards of a node share its immutable metadata and
// may emit separate events concurrently; Recorder does not merge their data.
// This is intended for trusted debug/test requests. It does not enable the static
// WithDebug snapshots or suppress business writes.
//
// Implementer's contract: Emit may be called concurrently. It must synchronously
// consume or freeze data before returning, because input views, nested containers,
// and pooled outputs may subsequently change. Capacity limits, supported data
// types, serialization, asynchronous delivery and session ownership belong to the
// implementation. Emit must return promptly without waiting for network I/O and
// must handle capture failures without affecting the business result. Recorder
// isolates emitter panics, but cannot isolate argument evaluation by the caller.
package debug

import "context"

// NodeMeta identifies the current operator invocation's position in a plan.
// Session/request identity is supplied by the application's emitter. SubFlow is
// empty for a top-level node; Type is the configured operator type_name.
type NodeMeta struct {
	Operator string
	Type     string
	SubFlow  string
}

// Emitter receives application-supplied data and framework-supplied metadata.
// See the package's implementer's contract for concurrency and data ownership.
type Emitter interface {
	Emit(meta NodeMeta, kind string, data any)
}

// Recorder is an immutable, lightweight view of an emitter at a node.
// Its zero value and a nil *Recorder both discard events.
type Recorder struct {
	emitter Emitter
	meta    NodeMeta
}

type contextKey struct{}

// WithEmitter attaches an application implementation to this request. A nil
// interface returns ctx unchanged, including any existing parent recorder.
// Callers must not supply an interface containing a typed nil pointer.
// Emitter/session shutdown remains the application's responsibility.
func WithEmitter(ctx context.Context, emitter Emitter) context.Context {
	if emitter == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, &Recorder{emitter: emitter})
}

// FromContext returns the optional recorder. Absence is a real nil pointer, so
// callers can skip payload construction as well as the actual Emit call.
func FromContext(ctx context.Context) *Recorder {
	r, _ := ctx.Value(contextKey{}).(*Recorder)
	return r
}

// BindNode derives a recorder without mutating the parent or shared emitter.
// It preserves ctx's values, deadline and cancellation. A disabled recorder
// returns ctx unchanged. The runtime calls this only when debug is enabled.
func (r *Recorder) BindNode(ctx context.Context, meta NodeMeta) context.Context {
	if r == nil || r.emitter == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, &Recorder{emitter: r.emitter, meta: meta})
}

// Emit synchronously forwards data, with no copying or encoding in Pineapple.
// A disabled recorder returns immediately; a failing implementation cannot panic
// into the business operator. Go evaluates arguments before entering this method,
// so guard debug-only payload construction with a nil check at the call site.
func (r *Recorder) Emit(kind string, data any) {
	if r == nil || r.emitter == nil {
		return
	}
	defer func() { _ = recover() }()
	r.emitter.Emit(r.meta, kind, data)
}
