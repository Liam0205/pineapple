package lua

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"weak"

	glua "github.com/yuin/gopher-lua"
)

func TestNewStatePool(t *testing.T) {
	sp, err := newStatePool(`function hello() return 42 end`)
	if err != nil {
		t.Fatal(err)
	}
	L := sp.Borrow()
	defer sp.Return(L)

	if err := L.CallByParam(glua.P{
		Fn:      L.GetGlobal("hello"),
		NRet:    1,
		Protect: true,
	}); err != nil {
		t.Fatal(err)
	}
	val := L.Get(-1)
	L.Pop(1)
	if val.(glua.LNumber) != 42 {
		t.Errorf("expected 42, got %v", val)
	}
}

func TestNewStatePoolBadScript(t *testing.T) {
	_, err := newStatePool(`this is not valid lua!!!`)
	if err == nil {
		t.Fatal("expected error for invalid script")
	}
}

func TestSnapshotAndClear(t *testing.T) {
	sp, err := newStatePool(`function f() new_global = 123; return new_global end`)
	if err != nil {
		t.Fatal(err)
	}
	L := sp.Borrow()

	// Call the function that creates a new global
	if err := L.CallByParam(glua.P{Fn: L.GetGlobal("f"), NRet: 1, Protect: true}); err != nil {
		t.Fatal(err)
	}
	L.Pop(1)

	// Verify new_global exists
	if L.GetGlobal("new_global") == glua.LNil {
		t.Fatal("expected new_global to exist")
	}

	// Return the state — should clear non-baseline globals
	sp.Return(L)

	// Borrow again and check new_global is gone
	L2 := sp.Borrow()
	defer sp.Return(L2)
	if L2.GetGlobal("new_global") != glua.LNil {
		t.Error("new_global should have been cleared")
	}
}

func TestPoolConcurrent(t *testing.T) {
	sp, err := newStatePool(`function inc() return x + 1 end`)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func(val float64) {
			L := sp.Borrow()
			defer sp.Return(L)

			L.SetGlobal("x", glua.LNumber(val))
			if err := L.CallByParam(glua.P{
				Fn: L.GetGlobal("inc"), NRet: 1, Protect: true,
			}); err != nil {
				done <- err
				return
			}
			result := float64(L.Get(-1).(glua.LNumber))
			L.Pop(1)
			if result != val+1 {
				done <- fmt.Errorf("expected %f, got %f", val+1, result)
				return
			}
			done <- nil
		}(float64(i))
	}
	for i := 0; i < 10; i++ {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

func TestResetToBaselineRestoresModifiedGlobal(t *testing.T) {
	L := glua.NewState()
	defer L.Close()

	baseline := snapshotGlobals(L)
	snap := snapshotBaselineValues(L, baseline)

	L.SetGlobal("tostring", glua.LString("hijacked"))

	resetToBaseline(L, baseline, snap)

	fn := L.GetGlobal("tostring")
	if fn.Type() != glua.LTFunction {
		t.Errorf("tostring type = %s, want function", fn.Type())
	}
}

func TestResetToBaselineRestoresDeletedGlobal(t *testing.T) {
	L := glua.NewState()
	defer L.Close()

	baseline := snapshotGlobals(L)
	snap := snapshotBaselineValues(L, baseline)

	L.SetGlobal("tostring", glua.LNil)

	resetToBaseline(L, baseline, snap)

	fn := L.GetGlobal("tostring")
	if fn.Type() != glua.LTFunction {
		t.Errorf("tostring type = %s, want function", fn.Type())
	}
}

func TestPoolReturnRestoresModifiedBaseline(t *testing.T) {
	sp, err := newStatePool(`function hijack() tostring = "pwned"; return 1 end`)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()

	const n = 5
	states := make([]*glua.LState, n)
	for i := range states {
		states[i] = sp.Borrow()
		if err := states[i].CallByParam(glua.P{Fn: states[i].GetGlobal("hijack"), NRet: 1, Protect: true}); err != nil {
			t.Fatal(err)
		}
		states[i].Pop(1)
	}
	for _, L := range states {
		sp.Return(L)
	}

	for i := 0; i < n; i++ {
		L := sp.Borrow()
		if L.GetGlobal("tostring").Type() != glua.LTFunction {
			t.Errorf("state %d: tostring not restored after Return", i)
		}
		sp.Return(L)
	}
}

// TestPoolWarmSetBoundedOverflowCollectable is the regression test for the
// unbounded leak (issue #61) under the min-idle warm-set design. The pool keeps
// a *bounded* set of warm states resident by strong reference (sp.minIdle);
// everything beyond that overflows to sync.Pool and MUST become collectable
// once the caller drops its reference and GC evicts it. If the pool pinned every
// state (the #61 bug), none of the overflow would ever be collected. We can't
// use a finalizer here — gopher-lua's LState contains internal reference cycles
// (LState.G.MainThread == LState), and Go does not guarantee finalizers run for
// objects in a cycle. A weak pointer has no such restriction. sync.Pool drains
// over two GC cycles (local → victim → cleared), so we run several.
func TestPoolWarmSetBoundedOverflowCollectable(t *testing.T) {
	sp, err := newStatePool(`function f() return 1 end`)
	if err != nil {
		t.Fatal(err)
	}
	const extra = 8
	total := sp.minIdle + extra

	wps := make([]weak.Pointer[glua.LState], 0, total)
	func() {
		// Hold all states at once so each is a distinct object, then return them:
		// the first minIdle refill the warm set, the rest overflow to sync.Pool.
		states := make([]*glua.LState, total)
		for i := range states {
			states[i] = sp.Borrow()
			wps = append(wps, weak.Make(states[i]))
		}
		for _, L := range states {
			sp.Return(L)
		}
	}()

	var collected int
	for i := 0; i < 8; i++ {
		runtime.GC()
		collected = 0
		for _, wp := range wps {
			if wp.Value() == nil {
				collected++
			}
		}
		if collected >= extra {
			break
		}
	}
	if collected < extra {
		t.Fatalf("only %d/%d overflow states collected — pool is pinning states (issue #61 leak)", collected, extra)
	}
	// Conversely, the resident set stays bounded: at most minIdle survive.
	if survivors := total - collected; survivors > sp.minIdle {
		t.Fatalf("%d states still resident, exceeds minIdle=%d — warm set is unbounded", survivors, sp.minIdle)
	}
}

// TestWarmStatesSurviveGC is the regression test for the per-GC rebuild (issue
// #67). Before the warm set existed, idle states lived only in sync.Pool, which
// GC clears every cycle, so the next borrow rebuilt a state from scratch and
// create_count tracked GC frequency. The bounded warm set holds states by strong
// reference across GC, so steady-state borrows must reuse without creating.
func TestWarmStatesSurviveGC(t *testing.T) {
	sp, err := newStatePool(`function f() return 1 end`)
	if err != nil {
		t.Fatal(err)
	}
	// Warm up so at least one state is resident in the warm set.
	sp.Return(sp.Borrow())

	createBefore := atomic.LoadInt64(&sp.createCount)
	for i := 0; i < 5; i++ {
		runtime.GC()
		L := sp.Borrow()
		if L == nil {
			t.Fatal("unexpected nil borrow")
		}
		sp.Return(L)
	}
	if c := atomic.LoadInt64(&sp.createCount); c != createBefore {
		t.Fatalf("create_count rose from %d to %d across GC cycles — warm states not surviving GC (issue #67)", createBefore, c)
	}
}

func TestPoolCloseIdempotent(t *testing.T) {
	sp, err := newStatePool(`function f() return 1 end`)
	if err != nil {
		t.Fatal(err)
	}
	sp.Close()
	sp.Close()
}

func TestBorrowAfterCloseReturnsNil(t *testing.T) {
	sp, err := newStatePool(`function f() return 1 end`)
	if err != nil {
		t.Fatal(err)
	}

	// Drain any cached states from the pool
	L := sp.Borrow()
	sp.Return(L)

	sp.Close()

	// After Close, pool.New returns nil because sp.closed == true.
	// The sync.Pool may still return previously cached (now-closed) states,
	// so borrow twice to exhaust the cache and trigger pool.New.
	_ = sp.Borrow() // may get cached (closed) state
	L2 := sp.Borrow()
	if L2 != nil {
		t.Errorf("expected nil from Borrow after Close (pool.New path), got %v", L2)
	}
}

func TestReturnAfterCloseNoPanic(t *testing.T) {
	sp, err := newStatePool(`function f() return 1 end`)
	if err != nil {
		t.Fatal(err)
	}

	L := sp.Borrow()
	sp.Close()

	// Return after Close should not panic or corrupt state.
	sp.Return(L)

	if atomic.LoadInt64(&sp.returnCount) != 1 {
		t.Errorf("expected returnCount=1, got %d", atomic.LoadInt64(&sp.returnCount))
	}
	if atomic.LoadInt64(&sp.activeCount) != 0 {
		t.Errorf("expected activeCount=0, got %d", atomic.LoadInt64(&sp.activeCount))
	}
}

// TestPoolReuseCountAccounting locks in the hit/miss split surfaced via
// reuse_count. Every borrow is either a pool hit (reuse_count++) or a pool miss
// that builds a fresh state (create_count++). create_count also counts the
// single pre-warm creation done at construction, so on-borrow misses equal
// create_count - 1 and the invariant borrow_count == reuse_count + misses must
// always hold regardless of GC or sync.Pool scheduling.
func TestPoolReuseCountAccounting(t *testing.T) {
	sp, err := newStatePool(`function f() return 1 end`)
	if err != nil {
		t.Fatal(err)
	}

	if c := atomic.LoadInt64(&sp.createCount); c != 1 {
		t.Fatalf("create_count after construction = %d, want 1 (pre-warm)", c)
	}
	if r := atomic.LoadInt64(&sp.reuseCount); r != 0 {
		t.Fatalf("reuse_count after construction = %d, want 0", r)
	}

	checkInvariant := func() {
		t.Helper()
		b := atomic.LoadInt64(&sp.borrowCount)
		r := atomic.LoadInt64(&sp.reuseCount)
		c := atomic.LoadInt64(&sp.createCount)
		if b != r+(c-1) {
			t.Fatalf("borrow_count(%d) != reuse_count(%d) + misses(%d)", b, r, c-1)
		}
	}

	// Sequential reuse: return before borrowing again so the next borrow is a
	// hit. reuse_count must climb and no fresh states should be created.
	for i := 0; i < 5; i++ {
		L := sp.Borrow()
		if L == nil {
			t.Fatal("unexpected nil borrow")
		}
		sp.Return(L)
		checkInvariant()
	}
	if r := atomic.LoadInt64(&sp.reuseCount); r == 0 {
		t.Fatal("reuse_count stayed 0 — pool hits are not being counted")
	}

	// Force a miss: hold two states at once. The pool holds at most one idle
	// state, so the second borrow must build a fresh one.
	beforeCreate := atomic.LoadInt64(&sp.createCount)
	a := sp.Borrow()
	b := sp.Borrow()
	if a == nil || b == nil {
		t.Fatal("unexpected nil borrow")
	}
	if c := atomic.LoadInt64(&sp.createCount); c <= beforeCreate {
		t.Fatalf("holding two states did not create a new one: create_count=%d", c)
	}
	checkInvariant()
	sp.Return(a)
	sp.Return(b)
	checkInvariant()

	if _, ok := sp.statsSnapshot()["reuse_count"]; !ok {
		t.Fatal("statsSnapshot missing reuse_count key")
	}
}
