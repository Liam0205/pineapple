package lua

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/Liam0205/pineapple/pkg/metrics"
	glua "github.com/yuin/gopher-lua"
)

var errPoolClosed = errors.New("lua statePool: pool is closed")

// statePool manages a pool of Lua states sharing the same loaded script.
// Each state is independent and safe for single-goroutine use.
type statePool struct {
	pool     sync.Pool
	script   string
	baseline map[string]struct{} // _G key names present after script load

	mu        sync.Mutex
	allStates []*glua.LState
	closed    bool
	snapshots sync.Map // *glua.LState → map[string]glua.LValue (borrow-time values)

	// always-on atomic counters (powers /stats)
	borrowCount int64
	returnCount int64
	createCount int64
	activeCount int64

	// external metrics (nil-safe, powers Prometheus)
	mBorrow metrics.Counter
	mReturn metrics.Counter
	mCreate metrics.Counter
	mActive metrics.Gauge
}

// newStatePool creates a pool that lazily creates Lua states with the given script loaded.
func newStatePool(script string) (*statePool, error) {
	sp := &statePool{script: script}

	// Create the first state to validate the script and capture baseline
	L, err := sp.newState()
	if err != nil {
		return nil, err
	}
	sp.baseline = snapshotGlobals(L)
	sp.pool.Put(L)
	atomic.AddInt64(&sp.createCount, 1)

	sp.pool.New = func() any {
		s, err := sp.newState()
		if err != nil {
			return nil
		}
		atomic.AddInt64(&sp.createCount, 1)
		if sp.mCreate != nil {
			sp.mCreate.Inc()
		}
		return s
	}

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
	sp.allStates = append(sp.allStates, L)
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
	v := sp.pool.Get()
	if v == nil {
		atomic.AddInt64(&sp.borrowCount, -1)
		atomic.AddInt64(&sp.activeCount, -1)
		return nil
	}
	L := v.(*glua.LState)
	sp.snapshots.Store(L, snapshotBaselineValues(L, sp.baseline))
	return L
}

// Return cleans up non-baseline globals and puts the state back in the pool.
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
		sp.pool.Put(L)
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
		"active_count": atomic.LoadInt64(&sp.activeCount),
	}
}

// Close releases all Lua states created by this pool.
func (sp *statePool) Close() {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.closed {
		return
	}
	sp.closed = true
	for _, L := range sp.allStates {
		L.Close()
	}
	sp.allStates = nil
}

func (sp *statePool) setMetrics(borrow, ret, create metrics.Counter, active metrics.Gauge) {
	sp.mBorrow = borrow
	sp.mReturn = ret
	sp.mCreate = create
	sp.mActive = active
}
