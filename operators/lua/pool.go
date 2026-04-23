package lua

import (
	"sync"
	"sync/atomic"

	"github.com/Liam0205/pineapple/pkg/metrics"
	glua "github.com/yuin/gopher-lua"
)

// statePool manages a pool of Lua states sharing the same loaded script.
// Each state is independent and safe for single-goroutine use.
type statePool struct {
	pool     sync.Pool
	script   string
	baseline map[string]struct{} // _G keys present after script load

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
			// Script was validated at init; this should not happen.
			panic("lua statePool: failed to create state: " + err.Error())
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
	L := glua.NewState()
	if err := L.DoString(sp.script); err != nil {
		L.Close()
		return nil, err
	}
	return L, nil
}

// Borrow returns a Lua state from the pool, ready for use.
func (sp *statePool) Borrow() *glua.LState {
	atomic.AddInt64(&sp.borrowCount, 1)
	atomic.AddInt64(&sp.activeCount, 1)
	if sp.mBorrow != nil {
		sp.mBorrow.Inc()
	}
	if sp.mActive != nil {
		sp.mActive.Add(1)
	}
	return sp.pool.Get().(*glua.LState)
}

// Return cleans up non-baseline globals and puts the state back in the pool.
func (sp *statePool) Return(L *glua.LState) {
	clearNonBaseline(L, sp.baseline)
	sp.pool.Put(L)
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

// clearNonBaseline removes all globals not present in the baseline snapshot.
func clearNonBaseline(L *glua.LState, baseline map[string]struct{}) {
	g := L.Get(glua.GlobalsIndex)
	tbl, ok := g.(*glua.LTable)
	if !ok {
		return
	}
	var toRemove []string
	tbl.ForEach(func(key glua.LValue, _ glua.LValue) {
		if s, ok := key.(glua.LString); ok {
			if _, base := baseline[string(s)]; !base {
				toRemove = append(toRemove, string(s))
			}
		}
	})
	for _, k := range toRemove {
		L.SetGlobal(k, glua.LNil)
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

func (sp *statePool) setMetrics(borrow, ret, create metrics.Counter, active metrics.Gauge) {
	sp.mBorrow = borrow
	sp.mReturn = ret
	sp.mCreate = create
	sp.mActive = active
}
