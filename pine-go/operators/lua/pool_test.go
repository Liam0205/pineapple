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

// TestPoolDoesNotRetainStates is the regression test for the unbounded leak
// (issue #61). The pool must NOT hold a strong reference to every state it ever
// created: once a returned state is evicted from sync.Pool by GC and the caller
// drops its reference, it must become collectable. We can't use a finalizer
// here — gopher-lua's LState contains internal reference cycles
// (LState.G.MainThread == LState), and Go does not guarantee finalizers run for
// objects in a cycle. A weak pointer has no such restriction, so we observe
// collection through it instead. sync.Pool drains over two GC cycles
// (local → victim → cleared), so we run several before giving up.
func TestPoolDoesNotRetainStates(t *testing.T) {
	sp, err := newStatePool(`function f() return 1 end`)
	if err != nil {
		t.Fatal(err)
	}

	var wp weak.Pointer[glua.LState]
	func() {
		L := sp.Borrow()
		wp = weak.Make(L)
		sp.Return(L)
	}()

	for i := 0; i < 8; i++ {
		runtime.GC()
		if wp.Value() == nil {
			return // state was collected — pool retains no strong reference
		}
	}
	t.Fatal("returned state was never collected — pool is still pinning states (issue #61 leak)")
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
