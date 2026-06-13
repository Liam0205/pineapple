//go:build !lua_gopher

package lua

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/Liam0205/pineapple/pine-go/pkg/metrics"
	"github.com/Liam0205/wangshu"
)

// defaultMinIdleWangshu mirrors defaultMinIdleStates from the gopher-lua side
// so steady-state borrow latency stays comparable across backends.
const defaultMinIdleWangshu = 100

var errWangshuPoolClosed = errors.New("lua wangshuPool: pool is closed")

// wangshuPool holds a compiled Program + a two-tier idle State pool, the same
// shape as the gopher-lua side: a bounded warm set held by strong reference
// (survives GC) plus an overflow sync.Pool tier the GC may reclaim. Memory is
// therefore bounded by minIdle + in-flight borrows.
//
// /stats output and benchmark noise patterns are deliberately kept in lockstep
// with the gopher-lua pool — same counter names, same warm semantics — so
// cross-backend comparisons see the same overhead profile.
type wangshuPool struct {
	program *wangshu.Program

	pool sync.Pool
	mu   sync.Mutex

	minIdle int
	warm    []*wangshu.State
	closed  bool

	// always-on counters (powers /stats)
	borrowCount int64
	returnCount int64
	createCount int64
	reuseCount  int64
	activeCount int64

	// external metrics (nil-safe, powers Prometheus)
	mBorrow metrics.Counter
	mReturn metrics.Counter
	mCreate metrics.Counter
	mActive metrics.Gauge
}

func newWangshuPool(script string) (*wangshuPool, error) {
	prog, err := wangshu.Compile([]byte(script), "transform_by_lua")
	if err != nil {
		return nil, fmt.Errorf("lua: failed to compile: %w", err)
	}
	wp := &wangshuPool{
		program: prog,
		minIdle: defaultMinIdleWangshu,
	}
	// Build the first state so compile-time syntax + script-level top-chunk
	// failures (arithmetic against a missing global, runtime error inside the
	// chunk body) surface at Init. Hold it as warm to skip rebuild on the
	// first Borrow.
	st, err := wp.newState()
	if err != nil {
		return nil, err
	}
	wp.warm = append(wp.warm, st)
	atomic.AddInt64(&wp.createCount, 1)
	return wp, nil
}

// newState constructs a fresh wangshu State with the pool's Program loaded.
// HideFileLoaders=true keeps loadfile/dofile/loadstring/load nil in globals so
// the sandbox matches gopher-lua's "fatal on attempt" semantics.
func (wp *wangshuPool) newState() (*wangshu.State, error) {
	wp.mu.Lock()
	if wp.closed {
		wp.mu.Unlock()
		return nil, errWangshuPoolClosed
	}
	wp.mu.Unlock()

	st := wangshu.NewState(wangshuOptions())
	// Run the chunk top-level so any function declarations and constants land
	// in the globals table. Per-borrow this is repeated, which would be
	// wasteful — but warm-tier reuse keeps the steady state Run-free.
	if _, err := wp.program.Run(st); err != nil {
		return nil, fmt.Errorf("lua: script load failed: %w", err)
	}
	// Mark the post-load _G as the reset baseline (wangshu issue #6). Borrow
	// hands states back through Return, which calls ResetGlobalsToBaseline so
	// script-level hijack (`tostring = ...`) and leak (`new_global = ...`) do
	// not survive into the next borrow. Baseline is taken AFTER program load so
	// the script's own top-level function defs are part of the clean state.
	st.MarkGlobalsBaseline()
	return st, nil
}

// Borrow returns an Engine adapter wrapping a wangshu State, or nil if the
// pool is closed.
func (wp *wangshuPool) Borrow() Engine {
	atomic.AddInt64(&wp.borrowCount, 1)
	atomic.AddInt64(&wp.activeCount, 1)
	if wp.mBorrow != nil {
		wp.mBorrow.Inc()
	}
	if wp.mActive != nil {
		wp.mActive.Add(1)
	}

	var st *wangshu.State
	if w := wp.takeWarm(); w != nil {
		st = w
		atomic.AddInt64(&wp.reuseCount, 1)
	} else if v := wp.pool.Get(); v != nil {
		st = v.(*wangshu.State)
		atomic.AddInt64(&wp.reuseCount, 1)
	} else {
		fresh, err := wp.newState()
		if err != nil {
			atomic.AddInt64(&wp.borrowCount, -1)
			atomic.AddInt64(&wp.activeCount, -1)
			return nil
		}
		atomic.AddInt64(&wp.createCount, 1)
		if wp.mCreate != nil {
			wp.mCreate.Inc()
		}
		st = fresh
	}
	return &wangshuEngine{st: st}
}

func (wp *wangshuPool) takeWarm() *wangshu.State {
	wp.mu.Lock()
	defer wp.mu.Unlock()
	n := len(wp.warm)
	if n == 0 {
		return nil
	}
	st := wp.warm[n-1]
	wp.warm[n-1] = nil
	wp.warm = wp.warm[:n-1]
	return st
}

// Return puts the state back. ResetGlobalsToBaseline (wangshu issue #6) wipes
// any script-level scratch globals so the next borrower sees a clean _G — the
// same isolation the gopher-lua side gets via snapshotGlobals/resetToBaseline.
//
// When the pool is already closed, the state is being discarded so the reset +
// RemoveContext are skipped (mirrors gopher-lua's pool_gopher_lua.go:262-285
// closed-branch behavior, which only drops the snapshot and skips
// resetToBaseline).
func (wp *wangshuPool) Return(eng Engine) {
	we, ok := eng.(*wangshuEngine)
	if !ok || we == nil {
		return
	}
	wp.mu.Lock()
	closed := wp.closed
	wp.mu.Unlock()
	if !closed {
		// Wipe script-level globals back to the post-load baseline, then drop
		// any ctx so it does not leak into the next borrow.
		we.st.ResetGlobalsToBaseline()
		we.st.RemoveContext()
	}
	wp.returnState(we.st)
}

func (wp *wangshuPool) returnState(st *wangshu.State) {
	wp.mu.Lock()
	closed := wp.closed
	wp.mu.Unlock()

	if !closed {
		wp.mu.Lock()
		if !wp.closed && len(wp.warm) < wp.minIdle {
			wp.warm = append(wp.warm, st)
			wp.mu.Unlock()
		} else {
			wp.mu.Unlock()
			wp.pool.Put(st)
		}
	}

	atomic.AddInt64(&wp.returnCount, 1)
	atomic.AddInt64(&wp.activeCount, -1)
	if wp.mReturn != nil {
		wp.mReturn.Inc()
	}
	if wp.mActive != nil {
		wp.mActive.Add(-1)
	}
}

// Close marks the pool closed and drops warm tier. wangshu has no State.Close
// today; the runtime collects States via GC once the last reference goes away.
func (wp *wangshuPool) Close() {
	wp.mu.Lock()
	defer wp.mu.Unlock()
	if wp.closed {
		return
	}
	wp.closed = true
	for i := range wp.warm {
		wp.warm[i] = nil
	}
	wp.warm = nil
}

func (wp *wangshuPool) StatsSnapshot() map[string]int64 {
	return map[string]int64{
		"borrow_count": atomic.LoadInt64(&wp.borrowCount),
		"return_count": atomic.LoadInt64(&wp.returnCount),
		"create_count": atomic.LoadInt64(&wp.createCount),
		"reuse_count":  atomic.LoadInt64(&wp.reuseCount),
		"active_count": atomic.LoadInt64(&wp.activeCount),
	}
}

func (wp *wangshuPool) SetMetrics(borrow, ret, create metrics.Counter, active metrics.Gauge) {
	wp.mBorrow = borrow
	wp.mReturn = ret
	wp.mCreate = create
	wp.mActive = active
}

// wangshuEngine adapts a *wangshu.State to backend.Engine. Single-borrow scoped.
type wangshuEngine struct {
	st *wangshu.State
	// dst is the caller-owned results buffer reused across Call invocations so
	// the zero-alloc CallInto path (wangshu v0.1.4, issue #8) stays alloc-free.
	// Grown on demand to fit the largest nret seen; never shrinks.
	dst []wangshu.Value
}

// SetContext forwards ctx to wangshu's internal cancellation hook so deadline
// / cancel events propagate from the host into the VM at the next step
// budget checkpoint.
func (e *wangshuEngine) SetContext(ctx context.Context) { e.st.SetContext(ctx) }

// RemoveContext clears the cancellation hook so a returned state doesn't keep
// a stale ctx live for the next borrower.
func (e *wangshuEngine) RemoveContext() { e.st.RemoveContext() }

// HasFunction reports whether the script defined a function-typed global of
// the given name. The temporary Value is Release()'d so the pin table stays
// flat.
func (e *wangshuEngine) HasFunction(name string) bool {
	v := e.st.GetGlobal(name)
	defer v.Release()
	return v.IsFunction()
}

// SetGlobal binds value to a global, with []any / map[string]any lifted to
// wangshu Tables on the fly. Anything left over (chan, func, struct...) is
// stringified to keep parity with the gopher-lua side's reflect fallback.
//
// LuaOp owns Table release: SetGlobal pins the table to the pool's State via
// NewTable; SetGlobal itself overwrites the global slot with the table Value,
// and the state's pin table holds the strong ref until the slot is overwritten
// or the state is reset. Per-Execute the same global names get reused, so the
// pin table churns one slot per global instead of growing.
func (e *wangshuEngine) SetGlobal(name string, value any) error {
	wv, err := e.toValue(value)
	if err != nil {
		return fmt.Errorf("global %q: %w", name, err)
	}
	e.st.SetGlobal(name, wv)
	return nil
}

// Call resolves the named global as a function and invokes it with no args
// (data flows in via globals). nret return values are lifted to Go-side types.
//
// Uses CallInto (wangshu v0.1.4, issue #8) with a reused, engine-owned dst
// buffer so scalar returns (bool/number) cost zero allocations per call — the
// boundary-dominated per-item path pineapple lives on. The function Value is
// Release()'d so the pin slot doesn't accumulate one per Call.
//
// Contract: CallInto's dst values alias the VM stack and must be consumed before
// the next VM entry. We do exactly that — fromValue lifts each into a Go-side
// value (strings copy arena bytes, composites pin) before returning, and the
// caller (LuaOp) reads the []any before the next Call. Each dst[j] is Release()'d
// after lift: a no-op for scalars, mandatory for pinned composites.
func (e *wangshuEngine) Call(fnName string, nret int) ([]any, error) {
	fn := e.st.GetGlobal(fnName)
	if !fn.IsFunction() {
		fn.Release()
		return nil, fmt.Errorf("lua: function %q not found", fnName)
	}
	defer fn.Release()

	if cap(e.dst) < nret {
		e.dst = make([]wangshu.Value, nret)
	}
	dst := e.dst[:nret]

	n, err := e.st.CallInto(dst, fn)
	if err != nil {
		return nil, err
	}
	out := make([]any, nret)
	for j := 0; j < nret; j++ {
		if j >= n {
			out[j] = nil
			continue
		}
		out[j] = e.fromValue(dst[j])
		dst[j].Release()
	}
	return out, nil
}

// toValue lifts a Go value into a wangshu.Value, recursing into composites by
// building wangshu Tables. Used both for SetGlobal and (via recursion) for
// nested list/map elements.
//
// Caveat: tables created here are pinned to the state and remain live until
// either (a) the global slot is overwritten with a different Value, or (b) the
// state is reset / GC'd. LuaOp.Execute always reuses the same input-named
// globals each call, so steady state has bounded pin-table churn.
func (e *wangshuEngine) toValue(v any) (wangshu.Value, error) {
	if v == nil {
		return wangshu.Nil(), nil
	}
	switch x := v.(type) {
	case bool:
		return wangshu.Bool(x), nil
	case float64:
		return wangshu.Number(x), nil
	case int64:
		return wangshu.Number(float64(x)), nil
	case int:
		return wangshu.Number(float64(x)), nil
	case string:
		return wangshu.String(x), nil
	case []any:
		return e.makeArrayTable(x)
	case map[string]any:
		return e.makeMapTable(x)
	}

	// Reflection fallback for typed slices / typed maps / wider integer kinds.
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		arr := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			arr[i] = rv.Index(i).Interface()
		}
		return e.makeArrayTable(arr)
	case reflect.Map:
		m := make(map[string]any, rv.Len())
		for _, k := range rv.MapKeys() {
			m[fmt.Sprint(k.Interface())] = rv.MapIndex(k).Interface()
		}
		return e.makeMapTable(m)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return wangshu.Number(float64(rv.Int())), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return wangshu.Number(float64(rv.Uint())), nil
	case reflect.Float32, reflect.Float64:
		return wangshu.Number(rv.Float()), nil
	}
	// Last-resort string coercion, matching gopher-lua side's behavior for
	// kinds the runtime can't represent (struct/chan/func).
	return wangshu.String(fmt.Sprintf("%v", v)), nil
}

func (e *wangshuEngine) makeArrayTable(arr []any) (wangshu.Value, error) {
	tv := e.st.NewTable()
	t := tv.AsTable()
	for i, elem := range arr {
		ev, err := e.toValue(elem)
		if err != nil {
			tv.Release()
			return wangshu.Nil(), fmt.Errorf("array[%d]: %w", i, err)
		}
		// 1-indexed per Lua convention; SetIndex returns error only on a
		// released table, which we just created.
		if err := t.SetIndex(i+1, ev); err != nil {
			tv.Release()
			return wangshu.Nil(), fmt.Errorf("array[%d]: %w", i, err)
		}
	}
	return tv, nil
}

func (e *wangshuEngine) makeMapTable(m map[string]any) (wangshu.Value, error) {
	tv := e.st.NewTable()
	t := tv.AsTable()
	for k, v := range m {
		vv, err := e.toValue(v)
		if err != nil {
			tv.Release()
			return wangshu.Nil(), fmt.Errorf("map[%q]: %w", k, err)
		}
		if err := t.Set(wangshu.String(k), vv); err != nil {
			tv.Release()
			return wangshu.Nil(), fmt.Errorf("map[%q]: %w", k, err)
		}
	}
	return tv, nil
}

// fromValue lifts a wangshu.Value back to a Go any, mirroring the gopher-lua
// fromLua conventions: contiguous 1..N arrays become []any, everything else
// becomes map[string]any, and an empty table becomes []any{} (cross-runtime
// convention).
//
// Function values are returned as a "<function>" string placeholder to avoid
// leaking pin slots into operator output; this matches gopher-lua's default
// behavior for non-data kinds.
func (e *wangshuEngine) fromValue(v wangshu.Value) any {
	switch {
	case v.IsNil():
		return nil
	case v.IsBool():
		return v.Bool()
	case v.IsNumber():
		return v.Number()
	case v.IsString():
		return v.Str()
	case v.IsTable():
		return e.tableToGo(v.AsTable())
	case v.IsFunction():
		return "<function>"
	}
	return v.Display()
}

// tableToGo walks a wangshu Table and converts it to []any when the integer
// keys are contiguous 1..N, else map[string]any. Mirrors fromLua on the
// gopher-lua side so cross-backend tests see the same shape.
//
// Released or empty tables map to []any{} by cross-runtime convention.
func (e *wangshuEngine) tableToGo(t *Table) any {
	if t == nil {
		return []any{}
	}
	wt := (*wangshu.Table)(t)
	n := wt.Len()
	if n > 0 {
		arr := make([]any, 0, n)
		contiguous := true
		for i := 1; i <= n; i++ {
			elem := wt.GetIndex(i)
			if elem.IsNil() {
				contiguous = false
				elem.Release()
				break
			}
			arr = append(arr, e.fromValue(elem))
			elem.Release()
		}
		if contiguous {
			return arr
		}
	}
	// Non-array shape: enumerate string keys via ForEach (wangshu issue #5).
	// Mirrors the gopher-lua fromLua map branch — only string keys land in the
	// map; an all-non-string-key table degrades to the empty-map placeholder.
	m := make(map[string]any)
	_ = wt.ForEach(func(key, val wangshu.Value) bool {
		if key.IsString() {
			m[key.Str()] = e.fromValue(val)
		}
		val.Release()
		return true
	})
	if len(m) == 0 {
		// Empty table → empty array (cross-runtime convention, matches fromLua).
		return []any{}
	}
	return m
}

// Table is a local type alias so internal helpers (tableToGo) don't have to
// import wangshu directly in their signatures — the alias keeps lua.go-side
// callers oblivious to wangshu.
type Table = wangshu.Table
