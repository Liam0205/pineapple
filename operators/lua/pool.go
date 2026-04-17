package lua

import (
	"sync"

	glua "github.com/yuin/gopher-lua"
)

// statePool manages a pool of Lua states sharing the same loaded script.
// Each state is independent and safe for single-goroutine use.
type statePool struct {
	pool     sync.Pool
	script   string
	baseline map[string]struct{} // _G keys present after script load
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

	sp.pool.New = func() any {
		s, err := sp.newState()
		if err != nil {
			// Script was validated at init; this should not happen.
			panic("lua statePool: failed to create state: " + err.Error())
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
	return sp.pool.Get().(*glua.LState)
}

// Return cleans up non-baseline globals and puts the state back in the pool.
func (sp *statePool) Return(L *glua.LState) {
	clearNonBaseline(L, sp.baseline)
	sp.pool.Put(L)
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
