package lua

import (
	"fmt"
	"testing"

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
