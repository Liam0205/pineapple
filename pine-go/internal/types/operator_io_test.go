package types

import (
	"errors"
	"testing"
)

// TestOperatorOutput_Reset_ClearsAllFields locks the lifetime contract that
// sync.Pool reclaim depends on: after Reset, every field of an
// OperatorOutput must look like a freshly-constructed one. If any future
// edit to Reset misses a field, a pooled output will carry the previous
// request's state into the next borrow — a silent correctness bug.
func TestOperatorOutput_Reset_ClearsAllFields(t *testing.T) {
	out := NewOperatorOutput()
	out.SetCommon("user_id", 42)
	out.SetItem(0, "score", 1.5)
	out.SetItem(1, "rank", int64(2))
	out.AddItem(map[string]any{"id": "new0"})
	out.RemoveItem(3)
	out.SetItemOrder([]int{2, 0, 1})
	out.SetWarning(errors.New("warn"))

	out.Reset()

	if cw := out.GetCommonWrites(); len(cw) != 0 {
		t.Errorf("commonWrites len = %d, want 0; got %v", len(cw), cw)
	}
	if iw := out.GetItemWrites(); len(iw) != 0 {
		t.Errorf("itemWrites len = %d, want 0", len(iw))
	}
	if ai := out.GetAddedItems(); len(ai) != 0 {
		t.Errorf("addedItems len = %d, want 0", len(ai))
	}
	if ri := out.GetRemovedItems(); len(ri) != 0 {
		t.Errorf("removedItems len = %d, want 0", len(ri))
	}
	if io := out.GetItemOrder(); io != nil {
		t.Errorf("itemOrder = %v, want nil", io)
	}
	if w := out.GetWarning(); w != nil {
		t.Errorf("warning = %v, want nil", w)
	}
}

// TestOperatorOutput_Reset_NilsItemWriteValues guards a subtle property:
// Reset must zero the ItemWrite entries inside the backing array before
// truncating to [:0], otherwise the pooled output keeps the previous
// request's Value (e.g. a large Lua-side payload) reachable through the
// slice's underlying memory until the next Put overwrites it. We verify by
// re-growing the slice past the previous length and observing that the
// formerly-occupied slots are zero.
func TestOperatorOutput_Reset_NilsItemWriteValues(t *testing.T) {
	out := NewOperatorOutput()
	bigPayload := make([]byte, 1024)
	out.SetItem(0, "blob", bigPayload)
	out.SetItem(1, "blob", bigPayload)

	out.Reset()

	// Force itemWrites to re-grow past previous len so the backing array's
	// former cells become observable via the accessor. They must be the
	// zero value of ItemWrite, not the bigPayload from before Reset.
	out.SetItem(2, "after", "fresh")
	iw := out.GetItemWrites()
	if len(iw) != 1 {
		t.Fatalf("itemWrites after one SetItem = %d, want 1", len(iw))
	}
	if iw[0].Field != "after" || iw[0].Value != "fresh" || iw[0].Index != 2 {
		t.Errorf("first ItemWrite = %+v, want {2, after, fresh}", iw[0])
	}

	// Reach into the backing array via a forced reslice — if Reset left
	// the underlying cells populated with bigPayload, they'd surface here
	// rather than being zero ItemWrites.
	backing := iw[:cap(iw)]
	for i := 1; i < len(backing); i++ {
		if backing[i] != (ItemWrite{}) {
			t.Errorf("backing[%d] = %+v, want zero ItemWrite", i, backing[i])
		}
	}
}

// TestOperatorOutput_Reset_NilsAddedItemSlots mirrors the previous test for
// the addedItems slice — Reset must drop map references so the pool
// doesn't hold a previous request's added rows past Reset.
func TestOperatorOutput_Reset_NilsAddedItemSlots(t *testing.T) {
	out := NewOperatorOutput()
	out.AddItem(map[string]any{"a": 1})
	out.AddItem(map[string]any{"b": 2})

	out.Reset()

	out.AddItem(map[string]any{"c": 3})
	ai := out.GetAddedItems()
	if len(ai) != 1 || ai[0]["c"] != 3 {
		t.Fatalf("addedItems after one AddItem = %v, want [{c:3}]", ai)
	}

	backing := ai[:cap(ai)]
	for i := 1; i < len(backing); i++ {
		if backing[i] != nil {
			t.Errorf("backing[%d] = %v, want nil", i, backing[i])
		}
	}
}

// TestOperatorOutput_Reset_RetainsSliceCapacity is a positive-direction
// check: Reset trims to [:0] but must not drop the backing array (that's
// the whole point of pooling). The next SetItem should not reallocate.
func TestOperatorOutput_Reset_RetainsSliceCapacity(t *testing.T) {
	out := NewOperatorOutput()
	for i := 0; i < 16; i++ {
		out.SetItem(i, "f", i)
	}
	preCap := cap(out.GetItemWrites())
	if preCap < 16 {
		t.Fatalf("preCap = %d, want >= 16", preCap)
	}

	out.Reset()

	out.SetItem(0, "f", 0)
	postCap := cap(out.GetItemWrites())
	if postCap < preCap {
		t.Errorf("itemWrites capacity shrank after Reset: %d → %d", preCap, postCap)
	}
}
