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

// gcCadenceWangshu drives a workaround for wangshu's host-driven-alloc GC
// pacing gap (pineapple #100 / wangshu #9): wangshu only checks its GC
// threshold at VM opcode safepoints, so a boundary-dominated LuaOp workload
// (large composite SetGlobal + tiny script) advances the collector's
// accounting but rarely its trigger, and the arena climbs unbounded. Until the
// upstream fix lands, Return drives an explicit sweep every gcCadenceWangshu
// returns. Cost is negligible (microseconds per collect, well under 1ms across
// thousands of returns).
//
// REMOVE once wangshu #9 makes host allocation drive GC cadence (or exposes a
// host-callable pacing API): this whole cadence path becomes dead weight.
const gcCadenceWangshu = 256

// arenaDropThresholdKB drives a workaround for wangshu's grow-only arena
// (pineapple #105 / wangshu #11): wangshu's arena backing slab only ever grows
// (grow64 doubles + copies; there is no shrink path), and Collect/sweep recycle
// dead objects into the freelist without ever returning the backing []uint64 to
// the runtime. So a state whose arena was ballooned once by an occasional large
// host allocation stays fat for life — and the warm/sync.Pool tiers cache that
// fat state, latching a high RSS high-water that the #100 cadence sweep cannot
// lower (it only frees into the arena's own freelist).
//
// Workaround: on Return, if the state's arena grew past this threshold, drop the
// state instead of returning it to the pool. The next Borrow builds a clean
// ~64 KB state (wangshu's default InitialBytes). This trades a small bump in
// create-rate for a bounded high-water.
//
// Threshold sizing: wangshu's default initial arena is 64 KB and a warm state's
// steady-state working set sits in the low hundreds of KB. 1 MB is well above
// any healthy steady state (so normal traffic never trips it) but far below the
// multi-MB fat states that latch production RSS (#105's geometric steps reach
// hundreds of MB). Only states that genuinely ballooned are dropped.
//
// REMOVE once wangshu #11 lands an arena shrink/rebuild after Collect (direction
// 1) — then high-water memory is released in-place and dropping states is
// unnecessary. A wired-up MaxArenaBytes (direction 2) or arena-cap observable
// (direction 3) would also let this be retired or made exact.
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

	// collectProg is a standalone `collectgarbage("collect")` chunk run on a
	// returning state every gcCadenceWangshu returns — the GC-pacing workaround
	// (see gcCadenceWangshu). It is independent of the user program: running it
	// on a state that already loaded `program` does not touch the user script's
	// globals or reset baseline (verified). nil only if its compile failed, in
	// which case the cadence sweep is silently skipped (the pool still works,
	// just without the workaround).
	collectProg *wangshu.Program

	pool sync.Pool
	mu   sync.Mutex

	minIdle int
	warm    []*wangshu.State
	closed  bool

	// gcReturnCount counts returns toward the gcCadenceWangshu sweep trigger.
	// Separate from returnCount (which is a public /stats counter) so the
	// workaround can be ripped out without perturbing stats.
	gcReturnCount int64

	// dropFatCount counts states dropped by the arena-drop workaround (#105 /
	// wangshu #11) — i.e. returns where GCCountKB exceeded arenaDropThresholdKB
	// so the fat state was discarded instead of pooled. Internal-only (not a
	// public /stats key); tests assert on it and it is a cheap signal that the
	// workaround is firing. Goes away with the workaround when wangshu #11 lands.
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
	// Compile the standalone collect chunk for the GC-pacing workaround. A
	// compile failure here is non-fatal: the pool still functions, it just
	// skips the cadence sweep (collectProg stays nil). collectgarbage is a
	// stdlib builtin so this should never fail, but we don't want a workaround
	// to be able to break pool construction.
	if cp, cErr := wangshu.Compile([]byte(`collectgarbage("collect")`), "pine_gc_cadence"); cErr == nil {
		wp.collectProg = cp
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

	drop := false
	if !closed {
		// Arena-drop workaround (pineapple #105 / wangshu #11): sample the arena
		// BEFORE reset/sweep, while this borrow's large host allocation (the
		// SetGlobal composite) is still live and counted. GCCountKB is
		// bump−freeBytes (LIVE bytes, not capacity), so the cadence sweep below
		// would deflate it and hide the state. wangshu exposes no arena-capacity
		// observable (wangshu #11 direction 3), so live-at-peak is our proxy: a
		// borrow whose live arena exceeds the threshold (16× the 64 KB default)
		// has necessarily grown its backing past that point, and grow-only means
		// that capacity is now latched. Drop such a state: skip reset/sweep (it's
		// being discarded, like the closed branch) and don't pool it, so the fat
		// backing slab is GC'd and the next Borrow rebuilds a clean ~64 KB state.
		// REMOVE when wangshu #11 lands an arena shrink/rebuild (direction 1).
		if we.st.GCCountKB() > arenaDropThresholdKB {
			drop = true
			atomic.AddInt64(&wp.dropFatCount, 1)
		}

		if !drop {
			// Wipe script-level globals back to the post-load baseline, then drop
			// any ctx so it does not leak into the next borrow.
			we.st.ResetGlobalsToBaseline()
			we.st.RemoveContext()
			// GC-pacing workaround (pineapple #100 / wangshu #9): drive an explicit
			// arena sweep every gcCadenceWangshu returns. Done here while this
			// goroutine still exclusively owns the state (before returnState hands
			// it back to warm/pool), honoring wangshu's single-goroutine-per-state
			// contract. collectProg is independent of the user program and does not
			// disturb globals/baseline. REMOVE when wangshu #9 lands.
			if wp.collectProg != nil && atomic.AddInt64(&wp.gcReturnCount, 1)%gcCadenceWangshu == 0 {
				_, _ = wp.collectProg.Run(we.st)
			}
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
		// err: dst untouched per wangshu CallInto contract; skip Release.
		// (See wangshu.go:397-419 — call paths bail before writing dst on
		// type-check / pin-resolve / VM panic, leaving the slice in its prior
		// post-Release zero state. Releasing now would double-free.)
		return nil, err
	}
	out := make([]any, nret)
	for j := 0; j < nret; j++ {
		if j >= n {
			out[j] = nil
			continue
		}
		val, ferr := e.fromValue(dst[j])
		if ferr != nil {
			// Release remaining dst entries (own and tail) before returning,
			// mirroring gopher-lua's `L.Pop(nret)` on the same error path.
			for k := j; k < n; k++ {
				dst[k].Release()
			}
			return nil, ferr
		}
		out[j] = val
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
			ev.Release()
			tv.Release()
			return wangshu.Nil(), fmt.Errorf("array[%d]: %w", i, err)
		}
		// Parent table now holds the GCRef via internal RawSet; the child
		// pin slot is redundant. No-op for scalars.
		ev.Release()
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
