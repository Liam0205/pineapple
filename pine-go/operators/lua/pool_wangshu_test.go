//go:build lua_wangshu

package lua

import (
	"sync"
	"sync/atomic"
	"testing"
)

// These tests cover the wangshu pool's borrow/return/create/reuse/active
// counters and warm-set reuse — the counterpart to pool_gopher_lua_test.go's
// accounting tests, which are gated behind the inverse build tag. The CallInto
// adapter change (issue #8) only touches the per-borrow Call path, not the pool
// machinery, so these pin that the stats contract still holds under wangshu.

func TestWangshuPoolReuseCountAccounting(t *testing.T) {
	wp, err := newWangshuPool(`function f() return 1 end`)
	if err != nil {
		t.Fatal(err)
	}

	// Construction pre-warms exactly one state.
	if c := atomic.LoadInt64(&wp.createCount); c != 1 {
		t.Fatalf("create_count after construction = %d, want 1 (pre-warm)", c)
	}
	if r := atomic.LoadInt64(&wp.reuseCount); r != 0 {
		t.Fatalf("reuse_count after construction = %d, want 0", r)
	}

	// Invariant: every borrow is a reuse (warm/pool hit) or a miss that builds a
	// fresh state. create_count counts the pre-warm too, so misses = create-1 and
	// borrow_count == reuse_count + misses must always hold.
	checkInvariant := func() {
		t.Helper()
		b := atomic.LoadInt64(&wp.borrowCount)
		r := atomic.LoadInt64(&wp.reuseCount)
		c := atomic.LoadInt64(&wp.createCount)
		if b != r+(c-1) {
			t.Fatalf("borrow_count(%d) != reuse_count(%d) + misses(%d)", b, r, c-1)
		}
	}

	// Sequential borrow/return: the returned state goes to the warm set, so the
	// next borrow is a reuse and no fresh state is created.
	for i := 0; i < 5; i++ {
		eng := wp.Borrow()
		if eng == nil {
			t.Fatal("unexpected nil borrow")
		}
		wp.Return(eng)
		checkInvariant()
	}
	if r := atomic.LoadInt64(&wp.reuseCount); r == 0 {
		t.Fatal("reuse_count stayed 0 — warm-set hits are not being counted")
	}

	// Holding two states at once forces a fresh creation: the second borrow can't
	// reuse the one still checked out.
	createBefore := atomic.LoadInt64(&wp.createCount)
	e1 := wp.Borrow()
	e2 := wp.Borrow()
	if c := atomic.LoadInt64(&wp.createCount); c <= createBefore {
		t.Fatalf("holding two states did not create a new one: create_count=%d (was %d)", c, createBefore)
	}
	wp.Return(e1)
	wp.Return(e2)
	checkInvariant()
}

func TestWangshuPoolActiveCountBalances(t *testing.T) {
	wp, err := newWangshuPool(`function f() return 1 end`)
	if err != nil {
		t.Fatal(err)
	}
	if a := atomic.LoadInt64(&wp.activeCount); a != 0 {
		t.Fatalf("active_count at rest = %d, want 0", a)
	}
	// Borrow N without returning: active climbs to N.
	const n = 8
	engs := make([]Engine, n)
	for i := range engs {
		engs[i] = wp.Borrow()
	}
	if a := atomic.LoadInt64(&wp.activeCount); a != n {
		t.Fatalf("active_count with %d outstanding = %d, want %d", n, a, n)
	}
	// Return all: active drains back to 0, and borrow==return.
	for _, e := range engs {
		wp.Return(e)
	}
	if a := atomic.LoadInt64(&wp.activeCount); a != 0 {
		t.Fatalf("active_count after returning all = %d, want 0", a)
	}
	b := atomic.LoadInt64(&wp.borrowCount)
	r := atomic.LoadInt64(&wp.returnCount)
	if b != r {
		t.Fatalf("borrow_count(%d) != return_count(%d) after balanced cycle", b, r)
	}
}

func TestWangshuPoolStatsSnapshotKeys(t *testing.T) {
	wp, err := newWangshuPool(`function f() return 1 end`)
	if err != nil {
		t.Fatal(err)
	}
	snap := wp.StatsSnapshot()
	for _, k := range []string{"borrow_count", "return_count", "create_count", "reuse_count", "active_count"} {
		if _, ok := snap[k]; !ok {
			t.Fatalf("StatsSnapshot missing key %q", k)
		}
	}
}

func TestWangshuPoolBorrowAfterCloseReturnsNil(t *testing.T) {
	wp, err := newWangshuPool(`function f() return 1 end`)
	if err != nil {
		t.Fatal(err)
	}
	wp.Close()
	if eng := wp.Borrow(); eng != nil {
		t.Fatal("Borrow after Close returned non-nil")
	}
	// Counters must not have advanced active beyond a balanced state: a refused
	// borrow rolls back its own borrow/active increments.
	if a := atomic.LoadInt64(&wp.activeCount); a != 0 {
		t.Fatalf("active_count after refused borrow = %d, want 0", a)
	}
}

func TestWangshuPoolConcurrentCounters(t *testing.T) {
	wp, err := newWangshuPool(`function f() return 1 end`)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	const goroutines, iters = 8, 200
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				eng := wp.Borrow()
				if eng == nil {
					return
				}
				wp.Return(eng)
			}
		}()
	}
	wg.Wait()

	b := atomic.LoadInt64(&wp.borrowCount)
	r := atomic.LoadInt64(&wp.returnCount)
	a := atomic.LoadInt64(&wp.activeCount)
	if b != goroutines*iters {
		t.Fatalf("borrow_count = %d, want %d", b, goroutines*iters)
	}
	if b != r {
		t.Fatalf("borrow_count(%d) != return_count(%d)", b, r)
	}
	if a != 0 {
		t.Fatalf("active_count after balanced concurrent cycle = %d, want 0", a)
	}
	// reuse + misses invariant holds under concurrency too.
	c := atomic.LoadInt64(&wp.createCount)
	reuse := atomic.LoadInt64(&wp.reuseCount)
	if b != reuse+(c-1) {
		t.Fatalf("borrow_count(%d) != reuse_count(%d) + misses(%d)", b, reuse, c-1)
	}
}
