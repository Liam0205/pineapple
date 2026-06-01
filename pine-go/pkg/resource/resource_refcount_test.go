package resource

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// closerValue is a test resource value that records whether it was closed.
type closerValue struct {
	id        int
	closed    atomic.Bool
	closeErr  error
	onClose   func(id int)
}

func (c *closerValue) Close() error {
	c.closed.Store(true)
	if c.onClose != nil {
		c.onClose(c.id)
	}
	return c.closeErr
}

// TestRefreshClosesSupersededValue verifies that when the background refresh
// stores a new value, the superseded value (with no outstanding borrows) is
// closed.
func TestRefreshClosesSupersededValue(t *testing.T) {
	var mu sync.Mutex
	var closedIDs []int
	var counter int32

	m := NewManager()
	m.Register("res", func(ctx context.Context) (any, error) {
		id := int(atomic.AddInt32(&counter, 1))
		return &closerValue{id: id, onClose: func(id int) {
			mu.Lock()
			closedIDs = append(closedIDs, id)
			mu.Unlock()
		}}, nil
	}, 40*time.Millisecond)

	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	// Wait for a couple of refreshes.
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	n := len(closedIDs)
	mu.Unlock()
	if n == 0 {
		t.Fatal("expected at least one superseded value to be closed after refreshes")
	}
	// The first value (id=1) must have been closed when superseded.
	mu.Lock()
	first := closedIDs[0]
	mu.Unlock()
	if first != 1 {
		t.Errorf("first closed value id = %d, want 1", first)
	}
}

// TestStopClosesCurrentValue verifies that Stop closes the current value held
// by each resource when no borrows are outstanding.
func TestStopClosesCurrentValue(t *testing.T) {
	cv := &closerValue{id: 1}

	m := NewManager()
	m.Register("res", func(ctx context.Context) (any, error) {
		return cv, nil
	}, time.Hour) // long interval: no refresh during test

	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if cv.closed.Load() {
		t.Fatal("value closed before Stop")
	}
	m.Stop()
	if !cv.closed.Load() {
		t.Error("Stop did not close the current value")
	}
}

// TestInFlightBorrowDefersClose verifies the use-after-close guarantee: while a
// borrow is held, Stop must NOT close the value; the close happens only after
// the borrow is released.
func TestInFlightBorrowDefersClose(t *testing.T) {
	cv := &closerValue{id: 1}

	m := NewManager()
	m.Register("res", func(ctx context.Context) (any, error) {
		return cv, nil
	}, time.Hour)

	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	h, ok := m.Get("res")
	if !ok {
		t.Fatal("expected borrow to succeed")
	}

	// Retire the manager while the borrow is held.
	m.Stop()

	if cv.closed.Load() {
		t.Fatal("value closed while an in-flight borrow was still held (use-after-close)")
	}
	if h.Value() != cv {
		t.Errorf("borrowed value = %v, want the original", h.Value())
	}

	// Releasing the last borrow triggers the deferred close.
	h.Release()
	if !cv.closed.Load() {
		t.Error("value not closed after the final borrow was released")
	}
}

// TestRefreshDefersCloseWhileBorrowed verifies that a refresh replacing a value
// that is currently borrowed defers the close until the borrow is released.
func TestRefreshDefersCloseWhileBorrowed(t *testing.T) {
	var counter int32
	first := &closerValue{id: 1}
	second := &closerValue{id: 2}

	m := NewManager()
	m.Register("res", func(ctx context.Context) (any, error) {
		n := atomic.AddInt32(&counter, 1)
		if n == 1 {
			return first, nil
		}
		return second, nil
	}, 40*time.Millisecond)

	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	// Borrow the first value, then let a refresh supersede it.
	h, ok := m.Get("res")
	if !ok {
		t.Fatal("expected borrow to succeed")
	}
	if h.Value() != first {
		t.Fatalf("borrowed value = %v, want first", h.Value())
	}

	// Wait for the refresh to replace the value.
	deadline := time.Now().Add(time.Second)
	for atomic.LoadInt32(&counter) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&counter) < 2 {
		t.Fatal("refresh did not run")
	}

	// The superseded first value must NOT be closed while still borrowed.
	if first.closed.Load() {
		t.Fatal("superseded value closed while still borrowed (use-after-close)")
	}

	h.Release()
	if !first.closed.Load() {
		t.Error("superseded value not closed after borrow released")
	}
}

// TestNonCloserValueIsSkipped verifies that resource values that do not
// implement io.Closer are handled without error on refresh and Stop.
func TestNonCloserValueIsSkipped(t *testing.T) {
	m := NewManager()
	m.Register("plain", func(ctx context.Context) (any, error) {
		return "just a string", nil
	}, 40*time.Millisecond)

	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	time.Sleep(120 * time.Millisecond) // let refresh run on a non-Closer value

	v, ok := getVal(m, "plain")
	if !ok || v != "just a string" {
		t.Errorf("Get(plain) = %v, %v", v, ok)
	}
	m.Stop() // must not panic on a non-Closer value
}

// TestStaticHandleReleaseIsNoop verifies the Static provider's handle releases
// without closing (Static never owns closable values).
func TestStaticHandleReleaseIsNoop(t *testing.T) {
	cv := &closerValue{id: 1}
	s := NewStatic(map[string]any{"res": cv})

	h, ok := s.Get("res")
	if !ok {
		t.Fatal("expected ok")
	}
	if h.Value() != cv {
		t.Errorf("value = %v, want cv", h.Value())
	}
	h.Release()
	if cv.closed.Load() {
		t.Error("Static handle Release must not close the value")
	}
}
