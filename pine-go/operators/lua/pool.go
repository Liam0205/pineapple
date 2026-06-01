package lua

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/Liam0205/pineapple/pine-go/pkg/metrics"
	glua "github.com/yuin/gopher-lua"
)

var errPoolClosed = errors.New("lua statePool: pool is closed")

// defaultMinIdleStates is how many warm states each pool keeps resident by
// strong reference. This bounds memory (fixing the unbounded leak of #61) while
// keeping states alive across GC cycles so the steady-state hot path reuses
// them instead of rebuilding (fixing the per-GC rebuild of #67).
const defaultMinIdleStates = 100

// statePool manages a pool of Lua states sharing the same loaded script.
// Each state is independent and safe for single-goroutine use.
//
// Idle states live in two tiers:
//   - warm: a bounded set (minIdle) held by strong reference so it survives GC
//     and keeps the hot path rebuild-free.
//   - pool: a sync.Pool holding the overflow, which the GC may reclaim.
//
// Memory is therefore bounded by minIdle + in-flight borrows, independent of
// uptime or GC frequency.
type statePool struct {
	pool     sync.Pool
	script   string
	baseline map[string]struct{} // _G key names present after script load

	mu        sync.Mutex
	minIdle   int            // cap on warm; states beyond this go to sync.Pool
	warm      []*glua.LState // bounded strong-ref idle states (len <= minIdle)
	closed    bool
	snapshots sync.Map // *glua.LState → map[string]glua.LValue (borrow-time values)

	// always-on atomic counters (powers /stats)
	borrowCount int64
	returnCount int64
	createCount int64 // states constructed from scratch (pre-warm + pool misses)
	reuseCount  int64 // borrows served by reusing a pooled state (pool hits)
	activeCount int64

	// external metrics (nil-safe, powers Prometheus)
	mBorrow metrics.Counter
	mReturn metrics.Counter
	mCreate metrics.Counter
	mActive metrics.Gauge
}

// newStatePool creates a pool that lazily creates Lua states with the given script loaded.
func newStatePool(script string) (*statePool, error) {
	sp := &statePool{script: script, minIdle: defaultMinIdleStates}

	// Create the first state to validate the script and capture baseline. It is
	// held as a warm (strong-ref) idle state so the hot path can reuse it.
	L, err := sp.newState()
	if err != nil {
		return nil, err
	}
	sp.baseline = snapshotGlobals(L)
	sp.warm = append(sp.warm, L)
	atomic.AddInt64(&sp.createCount, 1)

	// Note: we deliberately do NOT set sp.pool.New. Borrow handles the empty-pool
	// case itself so it can distinguish a pool hit (reuse) from a miss (fresh
	// creation) for the stats snapshot — sync.Pool.Get gives no way to tell which
	// path it took when New is set.

	return sp, nil
}

func (sp *statePool) newState() (*glua.LState, error) {
	L := glua.NewState(glua.Options{SkipOpenLibs: true})
	// Only open safe libraries: base, table, string, math.
	for _, lib := range []struct {
		name string
		fn   glua.LGFunction
	}{
		{glua.BaseLibName, glua.OpenBase},
		{glua.TabLibName, glua.OpenTable},
		{glua.StringLibName, glua.OpenString},
		{glua.MathLibName, glua.OpenMath},
	} {
		L.Push(L.NewFunction(lib.fn))
		L.Push(glua.LString(lib.name))
		L.Call(1, 0)
	}
	// Remove filesystem-accessing functions from base library
	for _, name := range []string{"dofile", "loadfile"} {
		L.SetGlobal(name, glua.LNil)
	}
	if err := L.DoString(sp.script); err != nil {
		L.Close()
		return nil, err
	}
	sp.mu.Lock()
	if sp.closed {
		sp.mu.Unlock()
		L.Close()
		return nil, errPoolClosed
	}
	sp.mu.Unlock()
	return L, nil
}

// Borrow returns a Lua state from the pool, ready for use.
// Returns nil if the pool has been closed.
func (sp *statePool) Borrow() *glua.LState {
	atomic.AddInt64(&sp.borrowCount, 1)
	atomic.AddInt64(&sp.activeCount, 1)
	if sp.mBorrow != nil {
		sp.mBorrow.Inc()
	}
	if sp.mActive != nil {
		sp.mActive.Add(1)
	}

	var L *glua.LState
	if w := sp.takeWarm(); w != nil {
		// Warm hit: reuse a resident state.
		L = w
		atomic.AddInt64(&sp.reuseCount, 1)
	} else if v := sp.pool.Get(); v != nil {
		// Pool hit: reuse an overflow state before it is GC-reclaimed.
		L = v.(*glua.LState)
		atomic.AddInt64(&sp.reuseCount, 1)
	} else {
		// Both tiers empty: build a fresh state. newState refuses once the pool
		// is closed, in which case the borrow is rolled back and we return nil.
		s, err := sp.newState()
		if err != nil {
			atomic.AddInt64(&sp.borrowCount, -1)
			atomic.AddInt64(&sp.activeCount, -1)
			return nil
		}
		atomic.AddInt64(&sp.createCount, 1)
		if sp.mCreate != nil {
			sp.mCreate.Inc()
		}
		L = s
	}
	sp.snapshots.Store(L, snapshotBaselineValues(L, sp.baseline))
	return L
}

// takeWarm pops a resident warm state, or returns nil if none are available.
func (sp *statePool) takeWarm() *glua.LState {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	n := len(sp.warm)
	if n == 0 {
		return nil
	}
	L := sp.warm[n-1]
	sp.warm[n-1] = nil
	sp.warm = sp.warm[:n-1]
	return L
}

// Return cleans up non-baseline globals and puts the state back. It refills the
// bounded warm set first (keeping states resident across GC); states beyond
// minIdle go to sync.Pool, which the GC may reclaim. This bounds the resident
// set at minIdle while keeping the steady-state hot path rebuild-free.
func (sp *statePool) Return(L *glua.LState) {
	sp.mu.Lock()
	closed := sp.closed
	sp.mu.Unlock()

	if closed {
		sp.snapshots.Delete(L)
	} else {
		var snap map[string]glua.LValue
		if v, ok := sp.snapshots.LoadAndDelete(L); ok {
			snap = v.(map[string]glua.LValue)
		}
		resetToBaseline(L, sp.baseline, snap)

		sp.mu.Lock()
		if !sp.closed && len(sp.warm) < sp.minIdle {
			sp.warm = append(sp.warm, L)
			sp.mu.Unlock()
		} else {
			sp.mu.Unlock()
			sp.pool.Put(L)
		}
	}

	atomic.AddInt64(&sp.returnCount, 1)
	atomic.AddInt64(&sp.activeCount, -1)
	if sp.mReturn != nil {
		sp.mReturn.Inc()
	}
	if sp.mActive != nil {
		sp.mActive.Add(-1)
	}
}

// snapshotGlobals captures the set of global variable names after script load.
func snapshotGlobals(L *glua.LState) map[string]struct{} {
	snapshot := make(map[string]struct{})
	g := L.Get(glua.GlobalsIndex)
	tbl, ok := g.(*glua.LTable)
	if !ok {
		return snapshot
	}
	tbl.ForEach(func(key glua.LValue, _ glua.LValue) {
		if s, ok := key.(glua.LString); ok {
			snapshot[string(s)] = struct{}{}
		}
	})
	return snapshot
}

// snapshotBaselineValues captures current values of baseline globals for a specific state.
func snapshotBaselineValues(L *glua.LState, baseline map[string]struct{}) map[string]glua.LValue {
	snap := make(map[string]glua.LValue, len(baseline))
	for k := range baseline {
		snap[k] = L.GetGlobal(k)
	}
	return snap
}

// resetToBaseline removes non-baseline globals and restores modified/deleted baseline globals
// using the per-state borrow-time snapshot.
func resetToBaseline(L *glua.LState, baseline map[string]struct{}, borrowSnap map[string]glua.LValue) {
	g := L.Get(glua.GlobalsIndex)
	tbl, ok := g.(*glua.LTable)
	if !ok {
		return
	}
	var toRemove []string
	tbl.ForEach(func(key glua.LValue, _ glua.LValue) {
		if s, ok := key.(glua.LString); ok {
			if _, isBase := baseline[string(s)]; !isBase {
				toRemove = append(toRemove, string(s))
			}
		}
	})
	for _, k := range toRemove {
		L.SetGlobal(k, glua.LNil)
	}
	for k, v := range borrowSnap {
		L.SetGlobal(k, v)
	}
}

func (sp *statePool) statsSnapshot() map[string]int64 {
	return map[string]int64{
		"borrow_count": atomic.LoadInt64(&sp.borrowCount),
		"return_count": atomic.LoadInt64(&sp.returnCount),
		"create_count": atomic.LoadInt64(&sp.createCount),
		"reuse_count":  atomic.LoadInt64(&sp.reuseCount),
		"active_count": atomic.LoadInt64(&sp.activeCount),
	}
}

// Close marks the pool as closed and releases the bounded warm set (which is
// safe to close eagerly because it is capped at minIdle, unlike the unbounded
// allStates of #61). Overflow states in sync.Pool, and any states still checked
// out, are reclaimed by the GC once unreferenced. After Close, newState refuses
// to hand out fresh states and Return drops states instead of recycling them.
func (sp *statePool) Close() {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.closed {
		return
	}
	sp.closed = true
	for i, L := range sp.warm {
		L.Close()
		sp.warm[i] = nil
	}
	sp.warm = nil
}

func (sp *statePool) setMetrics(borrow, ret, create metrics.Counter, active metrics.Gauge) {
	sp.mBorrow = borrow
	sp.mReturn = ret
	sp.mCreate = create
	sp.mActive = active
}
