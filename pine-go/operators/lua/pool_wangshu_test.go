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

// TestWangshuReturnDrivesArenaCompact pins the regression of the v0.2.0-rc3
// upgrade: wangshu's auto-GC starves on boundary-dominated LuaOp workloads
// (large SetGlobal + tiny script give few VM safepoints to check the trigger,
// so bytesAllocSince climbs without firing). On Return we now call
// MaybeCollectNow (wangshu #9 direction 2), which checks the threshold at the
// host boundary; the collector internally runs arena.Compact() after sweep
// (wangshu #11 direction 1 partial), so transient peaks release backing to the
// Go runtime in-place. The user script defines NO gc function — boundedness
// here comes purely from Return's MaybeCollectNow.
//
// Pinning a single warm state via LIFO re-borrow keeps us measuring one
// state's arena, not amortization across the pool.
func TestWangshuReturnDrivesArenaCompact(t *testing.T) {
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

	const iters = 2000
	const itemsPerIter = 100

	arr := make([]any, itemsPerIter)
	for i := range arr {
		arr[i] = float64(i)
	}

	var arenaAfterWarmup float64
	const warmupAt = 100
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
		wp.Return(eng)
	}

	eng := wp.Borrow()
	we := eng.(*wangshuEngine)
	arenaFinal := we.st.GCCountKB()
	wp.Return(eng)

	// Without MaybeCollectNow this would leak ~1 KB/iter (v0.1.4 baseline).
	// With it the arena oscillates around its working set.
	const maxGrowthKB = 256.0
	if grow := arenaFinal - arenaAfterWarmup; grow > maxGrowthKB {
		t.Fatalf("wangshu arena grew by %.1f KB across %d iterations (warmup=%.1f, final=%.1f); Return.MaybeCollectNow not bounding arena",
			grow, iters-warmupAt, arenaAfterWarmup, arenaFinal)
	}
}

// fatArrayItems is the element count that drives a single borrow's post-Compact
// ArenaCapKB above arenaDropThresholdKB. Calibrated against wangshu v0.2.0-rc3:
// once a borrow's arena grow-doublings push capacity past the threshold, Compact
// only shrinks cap to max(bump, 64 KiB), and bump is monotonic, so the cap
// latches at the bump extent — the sustained-fat shape the threshold targets.
// Empirically 200k float64 elements latch cap to ~1574 KiB (well above the 1024
// KiB threshold); 100k latches at ~793 KiB (below). 200k gives comfortable
// headroom so the drop fires deterministically across arena-layout noise.
// smallArrayItems = 100 latches at the 64 KiB initial cap — Compact fully self-
// heals it back to default so it must NEVER trip the drop.
const (
	fatArrayItems   = 200000
	smallArrayItems = 100
)

func fillArray(n int) []any {
	arr := make([]any, n)
	for i := range arr {
		arr[i] = float64(i)
	}
	return arr
}

// TestWangshuDropsFatStateOnReturn is the regression guard for the
// sustained-fat drop path (pineapple #105 / wangshu #11 Direction 1 partial). A
// borrow whose live set drives the arena cap past arenaDropThresholdKB and
// stays latched there even after Return's MaybeCollectNow + Compact must be
// DROPPED — not pooled — so the next Borrow rebuilds a clean ~64 KiB state.
// Transient peaks (lean workloads) self-heal via Compact and must NOT be
// dropped.
//
// Asserts: (1) the fat return increments dropFatCount and does NOT land in the
// warm tier; (2) the post-drop borrow misses the (now-empty) warm/pool tiers and
// creates a fresh state; (3) the 5-tuple invariant borrow == reuse + (create-1)
// still holds across the drop.
func TestWangshuDropsFatStateOnReturn(t *testing.T) {
	wp, err := newWangshuPool(`function f() return #xs end`)
	if err != nil {
		t.Fatal(err)
	}
	defer wp.Close()

	checkInvariant := func(label string) {
		t.Helper()
		b := atomic.LoadInt64(&wp.borrowCount)
		r := atomic.LoadInt64(&wp.reuseCount)
		c := atomic.LoadInt64(&wp.createCount)
		if b != r+(c-1) {
			t.Fatalf("%s: borrow_count(%d) != reuse_count(%d) + misses(%d)", label, b, r, c-1)
		}
	}

	// Drain the single pre-warmed state so the warm tier is empty and we control
	// exactly what is pooled. This borrow reuses the pre-warm state; returning a
	// SMALL workload keeps it (no drop), so warm holds exactly one lean state.
	eng := wp.Borrow()
	we := eng.(*wangshuEngine)
	if err := we.SetGlobal("xs", fillArray(smallArrayItems)); err != nil {
		t.Fatal(err)
	}
	if _, err := we.Call("f", 1); err != nil {
		t.Fatal(err)
	}
	wp.Return(eng)
	if d := atomic.LoadInt64(&wp.dropFatCount); d != 0 {
		t.Fatalf("small workload tripped the drop: dropFatCount=%d, want 0", d)
	}
	if n := len(wp.warm); n != 1 {
		t.Fatalf("warm tier after lean return = %d, want 1", n)
	}
	checkInvariant("after lean return")

	// Borrow again (reuses the lean warm state), this time balloon the arena past
	// the threshold. On Return it must be dropped: dropFatCount ticks and the warm
	// tier ends up empty (the fat state is discarded, not re-pooled).
	dropBefore := atomic.LoadInt64(&wp.dropFatCount)
	eng = wp.Borrow()
	we = eng.(*wangshuEngine)
	if err := we.SetGlobal("xs", fillArray(fatArrayItems)); err != nil {
		t.Fatal(err)
	}
	if _, err := we.Call("f", 1); err != nil {
		t.Fatal(err)
	}
	wp.Return(eng)
	if d := atomic.LoadInt64(&wp.dropFatCount); d != dropBefore+1 {
		t.Fatalf("fat return did not drop: dropFatCount=%d, want %d", d, dropBefore+1)
	}
	if n := len(wp.warm); n != 0 {
		t.Fatalf("warm tier after fat drop = %d, want 0 (fat state must not be pooled)", n)
	}
	checkInvariant("after fat drop")

	// The next borrow finds warm empty and the overflow pool empty (the fat state
	// was never Put there), so it must build a fresh state — create_count climbs.
	createBefore := atomic.LoadInt64(&wp.createCount)
	eng = wp.Borrow()
	if eng == nil {
		t.Fatal("unexpected nil borrow after drop")
	}
	if c := atomic.LoadInt64(&wp.createCount); c != createBefore+1 {
		t.Fatalf("post-drop borrow did not create a fresh state: create_count=%d, want %d", c, createBefore+1)
	}
	wp.Return(eng)
	checkInvariant("after post-drop rebuild")
}

// TestWangshuLeanWorkloadNeverDrops pins the negative: a sustained stream of
// normal-sized borrows must never trip the arena-drop workaround. Dropping lean
// states would defeat pooling (every borrow would miss + rebuild), so the
// threshold has to sit well above any healthy steady-state working set.
func TestWangshuLeanWorkloadNeverDrops(t *testing.T) {
	wp, err := newWangshuPool(`function f() local s=0; for i=1,#xs do s=s+xs[i] end; return s end`)
	if err != nil {
		t.Fatal(err)
	}
	defer wp.Close()

	arr := fillArray(smallArrayItems)
	for r := 0; r < 2000; r++ {
		eng := wp.Borrow()
		we := eng.(*wangshuEngine)
		if err := we.SetGlobal("xs", arr); err != nil {
			t.Fatalf("SetGlobal: %v", err)
		}
		if _, err := we.Call("f", 1); err != nil {
			t.Fatalf("Call: %v", err)
		}
		wp.Return(eng)
	}
	if d := atomic.LoadInt64(&wp.dropFatCount); d != 0 {
		t.Fatalf("lean workload tripped the drop %d times across 2000 returns; threshold too low", d)
	}
	// Steady-state reuse: with no drops, the warm tier keeps serving, so create
	// stays at the pre-warm 1.
	if c := atomic.LoadInt64(&wp.createCount); c != 1 {
		t.Fatalf("lean workload created %d states (want 1 pre-warm); unexpected misses", c)
	}
}

// TestWangshuDropFatStateConcurrent drives mixed lean/fat returns across
// goroutines and asserts the counters stay balanced and the 5-tuple invariant
// holds even when fat drops interleave with reuse. wangshu requires one
// goroutine per state, which Borrow/Return already enforce (a state is owned by
// exactly one borrower at a time), so this exercises the drop path under the
// concurrent counter traffic it will see in production.
func TestWangshuDropFatStateConcurrent(t *testing.T) {
	wp, err := newWangshuPool(`function f() return #xs end`)
	if err != nil {
		t.Fatal(err)
	}
	defer wp.Close()

	const goroutines, iters = 8, 60
	fat := fillArray(fatArrayItems)
	lean := fillArray(smallArrayItems)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				eng := wp.Borrow()
				if eng == nil {
					return
				}
				we := eng.(*wangshuEngine)
				// Every 5th borrow goes fat to force interleaved drops.
				arr := lean
				if i%5 == 0 {
					arr = fat
				}
				if err := we.SetGlobal("xs", arr); err != nil {
					t.Errorf("SetGlobal: %v", err)
					wp.Return(eng)
					return
				}
				if _, err := we.Call("f", 1); err != nil {
					t.Errorf("Call: %v", err)
					wp.Return(eng)
					return
				}
				wp.Return(eng)
			}
		}(g)
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
	// Invariant holds even with drops: each drop forces a later create, so the
	// reuse/create split shifts but borrow == reuse + (create-1) is preserved.
	c := atomic.LoadInt64(&wp.createCount)
	reuse := atomic.LoadInt64(&wp.reuseCount)
	if b != reuse+(c-1) {
		t.Fatalf("borrow_count(%d) != reuse_count(%d) + misses(%d) under concurrent drops", b, reuse, c-1)
	}
	// Sanity: the fat returns actually exercised the drop path.
	if d := atomic.LoadInt64(&wp.dropFatCount); d == 0 {
		t.Fatal("no drops recorded; concurrent test did not exercise the workaround")
	}
}
