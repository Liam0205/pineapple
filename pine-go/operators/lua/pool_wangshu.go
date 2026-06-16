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

// arenaDropThresholdKB drops sustained-fat states whose arena backing cannot be
// shrunk in-place (pineapple #105, wangshu #11 Direction 1 partial). wangshu
// v0.2.0-rc3's collector auto-runs arena.Compact() after sweep, which shrinks
// cap to max(bump, 64 KiB) — sufficient for *transient* peaks. But bump is
// monotonic and Compact does not retract it; if live objects are scattered
// across [0..bump) the cap stays latched at the bump extent (a full
// copy-compact GC would be required, deferred upstream as a follow-up).
//
// For sustained-fat states (where the live set itself stays large), the only
// available lever is to drop the state and let the next Borrow rebuild a clean
// ~64 KiB one. We threshold on ArenaCapKB (wangshu #11 Direction 3, real
// backing capacity — not GCCountKB live bytes) sampled AFTER MaybeCollectNow:
// this is the post-Compact cap, i.e. the capacity that genuinely won't shrink
// further on this state. transient peaks self-heal via Compact and never trip
// the drop; only states whose live set drives a latched-high cap reach it.
//
// Threshold sizing: wangshu's default initial arena is 64 KiB. 1024 KiB = 16×
// default is 4 grow-doublings — well above any healthy steady state (lean
// workloads sit in the low hundreds of KiB) but well below the multi-MiB cap
// production #105 observed.
const arenaDropThresholdKB = 1024.0

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

	// dropFatCount counts states discarded by the arena-drop path (sustained-fat
	// guard, see arenaDropThresholdKB). Internal-only (not a public /stats key);
	// tests assert on it and ops can read it as a cheap "is the drop firing"
	// signal. Goes away when wangshu lands a full copy-compact GC (#11 Direction
	// 1 follow-up).
	dropFatCount int64

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
// Memory management is a two-stage pipeline against wangshu's two memory
// pacing/sizing pitfalls (#100 / #105):
//
//  1. MaybeCollectNow (wangshu #9 direction 2): wangshu's auto-GC only checks
//     the trigger at VM opcode safepoints, which boundary-dominated LuaOp
//     workloads barely hit. We force a check at this host-safe boundary; the
//     collector internally runs arena.Compact() after sweep so transient peaks
//     release their backing slab to the Go runtime in-place.
//
//  2. ArenaCapKB drop (sustained-fat guard, #11 partial — see
//     arenaDropThresholdKB): Compact shrinks cap to max(bump, 64 KiB) but bump
//     is monotonic, so a state whose live set is genuinely large stays at a
//     latched cap. Sampled AFTER MaybeCollectNow, ArenaCapKB reflects the
//     post-Compact ceiling — if it still exceeds the threshold, the state is
//     sustained-fat and we drop it so the next Borrow rebuilds a clean one.
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
	// Release the Call fn cache (#112 finding #1) on every path: the pin slot
	// belongs to this borrow's Engine wrapper and would otherwise accumulate
	// across borrows. Order matters slightly — done before MaybeCollectNow so
	// the freed slot is available if the sweep re-uses the freelist.
	we.releaseFnCache()

	wp.mu.Lock()
	closed := wp.closed
	wp.mu.Unlock()

	drop := false
	if !closed {
		we.st.ResetGlobalsToBaseline()
		we.st.RemoveContext()
		we.st.MaybeCollectNow()
		// Post-Compact sample: ArenaCapKB is the real backing capacity, not live
		// bytes, so it is not deflated by sweep — a latched cap above threshold
		// means the live set itself sustains a fat state and Compact cannot
		// shrink further. Drop it; the next Borrow builds a fresh ~64 KiB state.
		if we.st.ArenaCapKB() > arenaDropThresholdKB {
			drop = true
			atomic.AddInt64(&wp.dropFatCount, 1)
		}
	}
	wp.returnState(we.st, drop)
}

func (wp *wangshuPool) returnState(st *wangshu.State, drop bool) {
	wp.mu.Lock()
	closed := wp.closed
	wp.mu.Unlock()

	if !closed && !drop {
		wp.mu.Lock()
		if !wp.closed && len(wp.warm) < wp.minIdle {
			wp.warm = append(wp.warm, st)
			wp.mu.Unlock()
		} else {
			wp.mu.Unlock()
			wp.pool.Put(st)
		}
	}
	// drop || closed: st is not pooled. It falls out of scope here and the Go
	// runtime reclaims it — and, for the drop case, its fat arena backing slab.

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
	// fn caches the Call function's resolved Value within a borrow scope (#112
	// finding #1). The first Call for a given fnName resolves it via GetGlobal +
	// IsFunction and caches the Value here; subsequent Calls with the same
	// fnName reuse the cache. Cleared on Release in returnState so the pin slot
	// returns to freePins for the next borrower.
	fn wangshu.Value
	// fnName matches the cached fn; when "" the cache is empty. LuaOp uses one
	// fnName per Engine borrow (item mode runs N Calls with the same name), so
	// a borrow-scope cache turns N globals lookups + N pin churn cycles into 1.
	fnName string
	// dst is the caller-owned results buffer reused across Call invocations so
	// the zero-alloc CallInto path (wangshu v0.1.4, issue #8) stays alloc-free.
	// Grown on demand to fit the largest nret seen; never shrinks.
	dst []wangshu.Value
}

// releaseFnCache returns the cached fn pin slot to freePins. Called by the pool
// on Return (before the state is handed back to warm/sync.Pool) so the next
// borrower starts with an empty cache. No-op when no fn was cached.
func (e *wangshuEngine) releaseFnCache() {
	if e.fnName != "" {
		e.fn.Release()
		e.fnName = ""
	}
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
// Pin-table contract: NewTable() values returned by toValue (table-kind) hold a
// pin slot until Release(). st.SetGlobal copies the GCRef into the globals
// table — it does NOT take ownership of the pin slot. We therefore Release wv
// after SetGlobal: the table stays reachable through globals (mark root), and
// the pin slot returns to freePins for reuse. Without this Release the slot
// accumulates per call — high-throughput LuaOp form (common-mode SetGlobal of
// []any per ItemInput field per request) leaked one pin slot + one arena
// table per call, growing arena linearly with QPS.
//
// Release is a no-op for scalar Values (kBool/kNumber/kString) so we can call
// it unconditionally without branching on kind.
func (e *wangshuEngine) SetGlobal(name string, value any) error {
	wv, err := e.toValue(value)
	if err != nil {
		return fmt.Errorf("global %q: %w", name, err)
	}
	defer wv.Release()
	e.st.SetGlobal(name, wv)
	return nil
}

// CallInto resolves the named global as a function and invokes it with no
// args (data flows in via globals), writing up to len(dst) return values into
// the caller-owned dst.
//
// Uses CallInto (wangshu v0.1.4, issue #8) with a reused, engine-owned scratch
// buffer (e.dst []wangshu.Value) so scalar returns cost zero allocations per
// call — the boundary-dominated per-item path pineapple lives on. The function
// Value is resolved once per borrow and cached (#112 finding #1): item-mode
// N=1000 pays a single GetGlobal + pin slot allocation instead of 1000. The
// dst []any avoids an additional per-call allocation (#112 finding #2).
//
// Contract: the engine-internal scratch buffer's values alias the VM stack and
// must be consumed before the next VM entry. We do exactly that — fromValue
// lifts each into a Go-side value (strings copy arena bytes, composites pin)
// before returning, and the caller (LuaOp) reads dst[j] before the next
// CallInto. Each scratch entry is Release()'d after lift: a no-op for scalars,
// mandatory for pinned composites.
func (e *wangshuEngine) CallInto(fnName string, dst []any) (int, error) {
	if e.fnName != fnName {
		// fnName changed (or first call): drop any previous cache and resolve.
		// LuaOp keeps fnName stable per borrow so this branch runs exactly once.
		if e.fnName != "" {
			e.fn.Release()
			e.fnName = ""
		}
		v := e.st.GetGlobal(fnName)
		if !v.IsFunction() {
			v.Release()
			return 0, fmt.Errorf("lua: function %q not found", fnName)
		}
		e.fn = v
		e.fnName = fnName
	}

	nret := len(dst)
	if cap(e.dst) < nret {
		e.dst = make([]wangshu.Value, nret)
	}
	scratch := e.dst[:nret]

	n, err := e.st.CallInto(scratch, e.fn)
	if err != nil {
		// err: scratch untouched per wangshu CallInto contract; skip Release.
		// (See wangshu.go:397-419 — call paths bail before writing on
		// type-check / pin-resolve / VM panic, leaving the slice in its prior
		// post-Release zero state. Releasing now would double-free.)
		return 0, err
	}
	for j := 0; j < nret; j++ {
		if j >= n {
			dst[j] = nil
			continue
		}
		val, ferr := e.fromValue(scratch[j])
		if ferr != nil {
			// Release remaining scratch entries (own and tail) before returning,
			// mirroring gopher-lua's `L.Pop(nret)` on the same error path.
			for k := j; k < n; k++ {
				scratch[k].Release()
			}
			return 0, ferr
		}
		dst[j] = val
		scratch[j].Release()
	}
	return nret, nil
}

// Call is a thin wrapper around CallInto for the common-mode call site (one
// call per request — no per-iteration alloc pressure to optimize). Allocates
// the result slice fresh each time.
func (e *wangshuEngine) Call(fnName string, nret int) ([]any, error) {
	out := make([]any, nret)
	if _, err := e.CallInto(fnName, out); err != nil {
		return nil, err
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
	if len(arr) == 0 {
		return e.st.NewTable(), nil
	}
	// Fast path: when every element shares one of the four primitive Go types
	// (#115 finding A, wangshu v0.2.0-rc4), route to the matching typed bulk
	// constructor. Drops the per-element toValue type-switch and the []Value
	// intermediate materialization in favour of a single linear classifier
	// walk plus one O(N) wangshu-side build. The cross-engine lua_script
	// contract is preserved — scripts still see a normal Lua array table.
	if v, ok := e.tryTypedArray(arr); ok {
		return v, nil
	}
	// Fallback: heterogeneous, nil-present, or composite elements. Build via
	// NewArrayTable (wangshu #10 direction 2, v0.2.0-rc3), which still beats
	// NewTable + N×SetIndex because the array segment is sized once.
	vals := make([]wangshu.Value, len(arr))
	for i, elem := range arr {
		ev, err := e.toValue(elem)
		if err != nil {
			for k := 0; k < i; k++ {
				vals[k].Release()
			}
			return wangshu.Nil(), fmt.Errorf("array[%d]: %w", i, err)
		}
		vals[i] = ev
	}
	tv := e.st.NewArrayTable(vals)
	for i := range vals {
		vals[i].Release()
	}
	return tv, nil
}

// tryTypedArray probes arr for type homogeneity against the four primitive
// Go types wangshu's typed-array constructors accept. Returns (Value, true)
// when every element matches; falls back to (Nil, false) on the first
// heterogeneous element, nil, or composite. NewInt64ArrayTable can return an
// error when a value exceeds float64 precision (|v| > 2^53); we treat that as
// a silent fallback so the heterogeneous path's lossy int64→float64 conversion
// (toValue's `wangshu.Number(float64(x))`) keeps the historical behaviour.
func (e *wangshuEngine) tryTypedArray(arr []any) (wangshu.Value, bool) {
	switch arr[0].(type) {
	case float64:
		out := make([]float64, len(arr))
		for i, v := range arr {
			f, ok := v.(float64)
			if !ok {
				return wangshu.Nil(), false
			}
			out[i] = f
		}
		return e.st.NewFloatArrayTable(out), true
	case int64:
		out := make([]int64, len(arr))
		for i, v := range arr {
			n, ok := v.(int64)
			if !ok {
				return wangshu.Nil(), false
			}
			out[i] = n
		}
		tv, err := e.st.NewInt64ArrayTable(out)
		if err != nil {
			return wangshu.Nil(), false
		}
		return tv, true
	case bool:
		out := make([]bool, len(arr))
		for i, v := range arr {
			b, ok := v.(bool)
			if !ok {
				return wangshu.Nil(), false
			}
			out[i] = b
		}
		return e.st.NewBoolArrayTable(out), true
	case string:
		out := make([]string, len(arr))
		for i, v := range arr {
			s, ok := v.(string)
			if !ok {
				return wangshu.Nil(), false
			}
			out[i] = s
		}
		return e.st.NewStringArrayTable(out), true
	}
	return wangshu.Nil(), false
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
			vv.Release()
			tv.Release()
			return wangshu.Nil(), fmt.Errorf("map[%q]: %w", k, err)
		}
		// Parent table holds the GCRef now. Release the child pin slot to
		// keep the pin table flat under nested-composite SetGlobal traffic.
		vv.Release()
	}
	return tv, nil
}

// fromValue lifts a wangshu.Value back to a Go any, mirroring the gopher-lua
// fromLua conventions: contiguous 1..N arrays become []any, everything else
// becomes map[string]any, and an empty table becomes []any{} (cross-runtime
// convention). Returns an error when a sub-table contains a non-string key —
// the cross-runtime contract requires only string-keyed tables to be valid maps,
// and gopher-lua's fromLua raises the same shape via a "lua: table has
// non-string key of type %q" message; cross-validate Section 12 (error parity)
// asserts byte equality.
//
// Function values are returned as a "<function>" string placeholder to avoid
// leaking pin slots into operator output; this matches gopher-lua's default
// behavior for non-data kinds.
func (e *wangshuEngine) fromValue(v wangshu.Value) (any, error) {
	switch {
	case v.IsNil():
		return nil, nil
	case v.IsBool():
		return v.Bool(), nil
	case v.IsNumber():
		return v.Number(), nil
	case v.IsString():
		return v.Str(), nil
	case v.IsTable():
		return e.tableToGo(v.AsTable())
	case v.IsFunction():
		return "<function>", nil
	}
	return v.Display(), nil
}

// tableToGo walks a wangshu Table and converts it to []any when the integer
// keys are contiguous 1..N, else map[string]any. Mirrors fromLua on the
// gopher-lua side so cross-backend tests see the same shape.
//
// Released or empty tables map to []any{} by cross-runtime convention. Tables
// with non-string keys (other than the integer 1..N array shape) raise an
// error byte-equal to gopher-lua's, so cross-runtime fixtures keep parity.
func (e *wangshuEngine) tableToGo(t *Table) (any, error) {
	if t == nil {
		return []any{}, nil
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
			converted, ferr := e.fromValue(elem)
			elem.Release()
			if ferr != nil {
				return nil, ferr
			}
			arr = append(arr, converted)
		}
		if contiguous {
			return arr, nil
		}
	}
	// Non-array shape: enumerate keys via ForEach (wangshu issue #5). Mirrors
	// the gopher-lua fromLua map branch — only string keys land in the map; a
	// non-string key raises a parity-byte-equal error. ForEach has no early-exit
	// path for errors, so capture into iterErr and return false to stop walking.
	m := make(map[string]any)
	var iterErr error
	_ = wt.ForEach(func(key, val wangshu.Value) bool {
		// Both key and val are pinned by ForEach via fromInnerWithPin (godoc:
		// "fn 不在外保留时,可在 fn 末尾顺手 Release 复合 val/key 防 pin 槽
		// 累积"). We never carry either past this callback — converted goes
		// into m by value or iterErr aborts — so release both unconditionally.
		defer key.Release()
		defer val.Release()
		if iterErr != nil {
			return false
		}
		if !key.IsString() {
			iterErr = fmt.Errorf("lua: table has non-string key of type %q", wangshuTypeName(key))
			return false
		}
		converted, ferr := e.fromValue(val)
		if ferr != nil {
			iterErr = ferr
			return false
		}
		m[key.Str()] = converted
		return true
	})
	if iterErr != nil {
		return nil, iterErr
	}
	if len(m) == 0 {
		// Empty table → empty array (cross-runtime convention, matches fromLua).
		return []any{}, nil
	}
	return m, nil
}

// wangshuTypeName returns the Lua type name string matching gopher-lua's
// LValueType.String() output for the same kind. Used to make
// "lua: table has non-string key of type %q" byte-equal across backends.
func wangshuTypeName(v wangshu.Value) string {
	switch {
	case v.IsNil():
		return "nil"
	case v.IsBool():
		return "boolean"
	case v.IsNumber():
		return "number"
	case v.IsString():
		return "string"
	case v.IsFunction():
		return "function"
	case v.IsTable():
		return "table"
	}
	// Fallback for kinds gopher-lua exposes via its 9-name table but wangshu's
	// public API does not currently surface (userdata/thread/channel). The
	// Display() output is human-readable rather than the LValueType string;
	// cross-validate fixtures only exercise scalar/table keys, so this branch
	// is unreached today but kept to avoid a silent empty quote.
	return v.Display()
}

// Table is a local type alias so internal helpers (tableToGo) don't have to
// import wangshu directly in their signatures — the alias keeps lua.go-side
// callers oblivious to wangshu.
type Table = wangshu.Table
