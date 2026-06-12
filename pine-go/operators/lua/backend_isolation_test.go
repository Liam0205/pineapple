package lua

import (
	"testing"
)

// TestBackendPoolGlobalsIsolation is a backend-agnostic regression for
// script-level global leakage across pool reuse. Both backends must wipe
// script-created / script-hijacked globals on Return so the next Borrow sees a
// clean baseline.
//
// gopher-lua does this via snapshotGlobals/resetToBaseline; wangshu via
// MarkGlobalsBaseline/ResetGlobalsToBaseline (issue #6). This test runs under
// both build tags and pins the invariant either way — unlike the gopher-lua
// internal pool tests, which are build-tagged to one backend.
func TestBackendPoolGlobalsIsolation(t *testing.T) {
	// A script that, when called, both hijacks a base global (tostring) and
	// creates a brand-new global (leaked_marker). After the call returns the
	// state to the pool, neither must survive into the next borrow.
	pool, err := backend.NewPool(`
		function poison()
			tostring = "hijacked"
			leaked_marker = 42
			return 1
		end
		function probe()
			-- returns true only if the environment is clean: tostring is a
			-- function again and leaked_marker is absent.
			return (type(tostring) == "function") and (leaked_marker == nil)
		end
	`)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	// Borrow #1: poison the environment, then return to the pool.
	eng1 := pool.Borrow()
	if eng1 == nil {
		t.Fatal("Borrow #1 returned nil")
	}
	if _, err := eng1.Call("poison", 1); err != nil {
		t.Fatalf("poison call: %v", err)
	}
	pool.Return(eng1)

	// Borrow #2: a fresh-looking state. The pool may hand back the very same
	// underlying state object (warm reuse), so this is exactly the leak path.
	eng2 := pool.Borrow()
	if eng2 == nil {
		t.Fatal("Borrow #2 returned nil")
	}
	defer pool.Return(eng2)

	got, err := eng2.Call("probe", 1)
	if err != nil {
		t.Fatalf("probe call: %v", err)
	}
	clean, ok := got[0].(bool)
	if !ok {
		t.Fatalf("probe returned %T, want bool", got[0])
	}
	if !clean {
		t.Errorf("globals leaked across pool reuse on backend %q: "+
			"tostring hijack or leaked_marker survived Return", activeBackendName())
	}
}

// TestBackendPoolReuseSurvivesManyCycles exercises the same isolation under
// repeated borrow/return so a backend that only resets on the first cycle (or
// resets the wrong tier) is caught.
func TestBackendPoolReuseSurvivesManyCycles(t *testing.T) {
	pool, err := backend.NewPool(`
		function poison() tostring = "x"; return 1 end
		function probe() return type(tostring) == "function" end
	`)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	for i := 0; i < 20; i++ {
		eng := pool.Borrow()
		if eng == nil {
			t.Fatalf("cycle %d: Borrow returned nil", i)
		}
		// Verify clean on entry, then poison before returning.
		got, err := eng.Call("probe", 1)
		if err != nil {
			t.Fatalf("cycle %d: probe: %v", i, err)
		}
		if clean, _ := got[0].(bool); !clean {
			t.Fatalf("cycle %d: tostring not restored before this borrow (backend %q)",
				i, activeBackendName())
		}
		if _, err := eng.Call("poison", 1); err != nil {
			t.Fatalf("cycle %d: poison: %v", i, err)
		}
		pool.Return(eng)
	}
}
