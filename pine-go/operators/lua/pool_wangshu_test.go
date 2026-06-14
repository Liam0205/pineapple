//go:build !lua_gopher

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

// TestWangshuSetGlobalCompositeNoPinLeak guards against the v0.10.0 regression
// where wangshuEngine.SetGlobal handed a NewTable() Value to st.SetGlobal but
// never Released it. Each call left a permanent pin slot + arena table alive,
// so high-throughput common-mode LuaOp form (where every ItemInput field is
// lifted into a []any → wangshu Table per request) leaked linearly with QPS.
//
// Strategy: drive a tight Borrow → SetGlobal([]any of N entries) → Call →
// Return loop, periodically forcing wangshu's collector via the script (the
// only safepoint path that runs the sweep), and assert that arena KB does not
// grow unboundedly across iterations. With the leak this asserts within a few
// hundred iterations; without it the arena oscillates around a small steady
// state regardless of iteration count.
func TestWangshuSetGlobalCompositeNoPinLeak(t *testing.T) {
	const script = `
function f()
    local s = 0
    for i = 1, #xs do s = s + xs[i] end
    return s
end
function gc() collectgarbage("collect") end
`
	wp, err := newWangshuPool(script)
	if err != nil {
		t.Fatal(err)
	}
	defer wp.Close()

	// Drive 2k iterations of the leaky shape. Use a fixed warm state so we
	// observe one state's pin table, not amortization across the warm pool.
	const iters = 2000
	const itemsPerIter = 100

	arr := make([]any, itemsPerIter)
	for i := range arr {
		arr[i] = float64(i)
	}

	var arenaAfterWarmup float64
	for r := 0; r < iters; r++ {
		eng := wp.Borrow()
		if eng == nil {
			t.Fatal("unexpected nil borrow")
		}
		we := eng.(*wangshuEngine)
		if err := we.SetGlobal("xs", arr); err != nil {
			t.Fatalf("SetGlobal: %v", err)
		}
		if _, err := we.Call("f", 1); err != nil {
			t.Fatalf("Call: %v", err)
		}
		// Force a sweep so dropped pins actually return arena bytes. Without
		// this we'd race the collector's own threshold heuristic.
		if _, err := we.Call("gc", 0); err != nil {
			t.Fatalf("gc Call: %v", err)
		}
		// Sample arena right after warm-up so the assertion compares like to
		// like (post-first-Call internal allocations are baked into baseline).
		if r == 100 {
			arenaAfterWarmup = we.st.GCCountKB()
		}
		wp.Return(eng)
	}

	// Re-borrow to read the same state's arena counter. The pool's warm tier
	// is LIFO so we get the most recently returned state — the one we were
	// hammering above.
	eng := wp.Borrow()
	we := eng.(*wangshuEngine)
	arenaFinal := we.st.GCCountKB()
	wp.Return(eng)

	// Tolerance: allow a small drift (intern interactions, idle freelist
	// fragmentation). The leak grows ~1 KB per iteration; (iters-100) at
	// 100 items reproduces ≈ 2 MB of growth pre-fix. 256 KB headroom is
	// well above noise (single-state arena baseline) and well below leak
	// magnitude.
	const maxGrowthKB = 256.0
	if grow := arenaFinal - arenaAfterWarmup; grow > maxGrowthKB {
		t.Fatalf("wangshu arena grew by %.1f KB across %d iterations (warmup=%.1f, final=%.1f); pin leak suspected",
			grow, iters-100, arenaAfterWarmup, arenaFinal)
	}
}

// TestWangshuGCCadenceBoundsArenaWithoutInLoopCollect is the regression guard
// for the GC-pacing workaround (pineapple #100 / wangshu #9). Unlike
// TestWangshuSetGlobalCompositeNoPinLeak above, the script defines NO gc
// function and the loop NEVER triggers a collect itself — the realistic
// embedding shape where user scripts don't self-GC. Arena boundedness here
// comes solely from wangshuPool.Return's cadence sweep (every gcCadenceWangshu
// returns).
//
// Without the workaround this loop leaks ~1 KB/iter (wangshu's auto-GC almost
// never fires on host-driven allocs + tiny scripts); with it the arena stays
// flat. The loop runs well past gcCadenceWangshu so multiple cadence sweeps
// fire. Pinning a single warm state (LIFO re-borrow) keeps us measuring one
// state's arena, not amortization across the pool.
func TestWangshuGCCadenceBoundsArenaWithoutInLoopCollect(t *testing.T) {
	// Script body is a few opcodes — the boundary-dominated shape that starves
	// wangshu's safepoint-driven GC. No gc() function on purpose.
	const script = `
function f()
    local s = 0
    for i = 1, #xs do s = s + xs[i] end
    return s
end
`
	wp, err := newWangshuPool(script)
	if err != nil {
		t.Fatal(err)
	}
	defer wp.Close()
	if wp.collectProg == nil {
		t.Fatal("collectProg failed to compile — cadence workaround inactive")
	}

	// Run several multiples of the cadence so the sweep fires repeatedly.
	const iters = gcCadenceWangshu * 8
	const itemsPerIter = 100

	arr := make([]any, itemsPerIter)
	for i := range arr {
		arr[i] = float64(i)
	}

	var arenaAfterWarmup float64
	const warmupAt = gcCadenceWangshu // sample after the first cadence sweep
	for r := 0; r < iters; r++ {
		eng := wp.Borrow()
		if eng == nil {
			t.Fatal("unexpected nil borrow")
		}
		we := eng.(*wangshuEngine)
		if err := we.SetGlobal("xs", arr); err != nil {
			t.Fatalf("SetGlobal: %v", err)
		}
		if _, err := we.Call("f", 1); err != nil {
			t.Fatalf("Call: %v", err)
		}
		if r == warmupAt {
			arenaAfterWarmup = we.st.GCCountKB()
		}
		wp.Return(eng) // cadence sweep fires here every gcCadenceWangshu returns
	}

	eng := wp.Borrow()
	we := eng.(*wangshuEngine)
	arenaFinal := we.st.GCCountKB()
	wp.Return(eng)

	// Pre-workaround this leaks ~1 KB/iter → ~iters KB of growth. With the
	// cadence sweep the arena oscillates around its working set. Headroom well
	// below the leak magnitude (iters*8 = 2048 iters ≈ 2 MB leak pre-fix) and
	// above single-state arena noise.
	const maxGrowthKB = 256.0
	if grow := arenaFinal - arenaAfterWarmup; grow > maxGrowthKB {
		t.Fatalf("wangshu arena grew by %.1f KB across %d iterations (warmup=%.1f, final=%.1f) with NO in-loop collect; cadence workaround not bounding arena",
			grow, iters-warmupAt, arenaAfterWarmup, arenaFinal)
	}
}
