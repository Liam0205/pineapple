// Package lua's backend layer abstracts the underlying Lua VM so the operator
// (LuaOp in lua.go) is VM-agnostic. Two implementations live under build tags:
//
//   - default(!lua_gopher): wangshu via *_wangshu.go, see
//     https://github.com/Liam0205/wangshu
//   - opt-in(lua_gopher):   gopher-lua via *_gopher_lua.go
//
// Selection happens at compile time so a single binary embeds exactly one
// backend, with zero runtime dispatch cost. The active backend exposes a
// package-level `backend` variable consumed by LuaOp.
package lua

import (
	"context"

	"github.com/Liam0205/pineapple/pine-go/pkg/metrics"
)

// Backend is the factory abstraction for a Lua VM family. It is selected at
// build time via build tags; only one implementation is linked into the final
// binary. The factory itself is stateless — each NewPool yields an independent
// state pool tied to a specific Lua script.
type Backend interface {
	// NewPool compiles `script` and returns a Pool that hands out runnable
	// states sharing that compiled script. Returns an error if the script
	// fails to compile.
	NewPool(script string) (Pool, error)
}

// Pool manages a set of warm states sharing a compiled script. Implementations
// must be safe for concurrent Borrow / Return / Close.
//
// The pool exposes both an always-on stats snapshot (powers /stats) and a
// nil-safe metrics hook (powers Prometheus). Operator metrics injection routes
// through SetMetrics; stats reads route through StatsSnapshot.
type Pool interface {
	// Borrow returns a state ready for use, or nil if the pool is closed.
	// The state is bound to the caller's goroutine until Return.
	Borrow() Engine

	// Return puts the state back into the pool. Must be paired with a
	// non-nil Borrow result. After Return the engine handle must not be
	// reused by the caller.
	Return(Engine)

	// Close releases warm-tier states immediately and stops handing out
	// new ones. Idempotent.
	Close()

	// StatsSnapshot returns the always-on counter snapshot:
	// borrow/return/create/reuse/active counts.
	StatsSnapshot() map[string]int64

	// SetMetrics installs Prometheus counters/gauges. Optional; any nil
	// argument is a no-op. Backends must be tolerant of being called once
	// after construction (before any Borrow).
	SetMetrics(borrow, ret, create metrics.Counter, active metrics.Gauge)
}

// Engine is a per-borrow state handle. Implementations need not be safe for
// concurrent use; LuaOp.Execute holds one Engine per goroutine via Borrow.
//
// The Engine abstracts the two patterns LuaOp depends on:
//
//   - Per-item: SetGlobal(field, item_value...) then Call(funcName, nret)
//   - Per-common: SetGlobal(field, table_for_each_field) then Call(funcName, nret)
//
// Calls return a flat []any of length nret. Type mapping for both input (Go any
// → backend native) and output (backend native → Go any) is the backend's
// responsibility — LuaOp does not see backend types.
type Engine interface {
	// SetContext installs ctx so the backend can honor cancellation /
	// deadlines mid-execution. Paired with RemoveContext on Return.
	SetContext(ctx context.Context)

	// RemoveContext drops the previously installed context.
	RemoveContext()

	// HasFunction reports whether the script defines a top-level function
	// of the given name. Used by LuaOp.Init to fail fast on missing entry
	// points before any Execute attempt.
	HasFunction(name string) bool

	// SetGlobal binds value to a global variable. value uses Go-side types
	// (bool/float64/int64/int/string/[]any/map[string]any/nil); the backend
	// converts to its native value representation. An error is returned
	// when the backend cannot represent the value.
	//
	// Retention contract: backends MUST NOT retain composite values
	// ([]any, map[string]any, or their reflect-walked equivalents) past
	// SetGlobal's return — every element is lifted into the backend's own
	// native value representation before SetGlobal returns. Callers may
	// therefore reuse or pool the caller-side buffer for the next field /
	// the next request, knowing the backend holds no references to it.
	// LuaOp's executeForCommon relies on this to pool the per-ItemInput
	// []any buffer (#112 finding #3); a backend that aliases the caller's
	// slice into its global table would break that reuse and silently
	// corrupt the second field's data.
	SetGlobal(name string, value any) error

	// Call invokes the top-level function named fnName with no arguments
	// (all data passes via globals) and returns nret return values lifted
	// to Go-side types. Runtime errors from the script are propagated as
	// Go errors; the backend is responsible for keeping its internal stack
	// balanced regardless of error outcome.
	//
	// Allocates the result slice fresh on each call; use CallInto on the hot
	// per-item path to avoid this.
	Call(fnName string, nret int) ([]any, error)

	// CallInto is the per-item hot-path variant of Call: instead of allocating
	// a fresh []any of length nret on every invocation, the caller provides a
	// destination slice (typically reused across an N-item loop). All
	// len(dst) slots are always written before return — excess slots (when
	// the script returns fewer values than len(dst)) are zeroed with nil.
	// The returned int is len(dst), retained in the signature for symmetry
	// with stdlib io.ReaderAt-style "n int, err error" conventions; callers
	// rarely need to consume it.
	//
	// Lifetime: dst entries hold caller-owned Go values after return (strings
	// are copies, composites are independent Go maps/slices), so callers may
	// retain them past the next Engine call. Backend-internal scratch buffers
	// (e.g. wangshu's dst []wangshu.Value) are reused across CallInto calls
	// and must NOT be aliased into dst.
	CallInto(fnName string, dst []any) (int, error)
}

// backend is the active backend factory. Exactly one of the *_gopher_lua.go /
// *_wangshu.go files (selected by build tag) assigns this in its init.
var backend Backend
