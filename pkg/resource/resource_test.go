package resource

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestBasicRegisterStartGet(t *testing.T) {
	m := NewManager()
	m.Register("counter", func(ctx context.Context) (any, error) {
		return 42, nil
	}, time.Hour)

	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	val, ok := m.Get("counter")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if val != 42 {
		t.Errorf("val = %v, want 42", val)
	}
}

func TestGetUnknownResource(t *testing.T) {
	m := NewManager()
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	_, ok := m.Get("nonexistent")
	if ok {
		t.Error("expected ok=false for unknown resource")
	}
}

func TestInitialLoadFailure(t *testing.T) {
	m := NewManager()
	m.Register("bad", func(ctx context.Context) (any, error) {
		return nil, fmt.Errorf("connection refused")
	}, time.Hour)

	err := m.Start(context.Background())
	if err == nil {
		m.Stop()
		t.Fatal("expected error on initial load failure")
	}
}

func TestRefreshFailureKeepsOldValue(t *testing.T) {
	var callCount atomic.Int32

	m := NewManager()
	m.Register("flaky", func(ctx context.Context) (any, error) {
		n := callCount.Add(1)
		if n == 1 {
			return "initial", nil // first call succeeds
		}
		return nil, fmt.Errorf("transient error") // subsequent calls fail
	}, 50*time.Millisecond)

	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	// Wait for at least one refresh attempt
	time.Sleep(150 * time.Millisecond)

	// Should still have the initial value
	val, ok := m.Get("flaky")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if val != "initial" {
		t.Errorf("val = %v, want 'initial'", val)
	}

	// Should have been called more than once (initial + at least one refresh)
	if callCount.Load() < 2 {
		t.Errorf("expected at least 2 fetcher calls, got %d", callCount.Load())
	}
}

func TestRefreshUpdatesValue(t *testing.T) {
	var counter atomic.Int32

	m := NewManager()
	m.Register("inc", func(ctx context.Context) (any, error) {
		return int(counter.Add(1)), nil
	}, 50*time.Millisecond)

	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	// Initial value should be 1
	val, _ := m.Get("inc")
	if val != 1 {
		t.Errorf("initial val = %v, want 1", val)
	}

	// Wait for a refresh
	time.Sleep(150 * time.Millisecond)

	val, _ = m.Get("inc")
	if val.(int) <= 1 {
		t.Errorf("expected updated value > 1, got %v", val)
	}
}

func TestStopDoesNotLeak(t *testing.T) {
	m := NewManager()
	m.Register("a", func(ctx context.Context) (any, error) {
		return "a", nil
	}, 50*time.Millisecond)
	m.Register("b", func(ctx context.Context) (any, error) {
		return "b", nil
	}, 50*time.Millisecond)

	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Stop should return promptly without blocking
	done := make(chan struct{})
	go func() {
		m.Stop()
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return within 2s — possible goroutine leak")
	}
}

func TestContextInjection(t *testing.T) {
	ctx := context.Background()

	// Without injection, FromContext returns nil
	if rp := FromContext(ctx); rp != nil {
		t.Error("expected nil without injection")
	}

	// With injection
	mock := NewStatic(map[string]any{"key": "value"})
	ctx = WithResources(ctx, mock)

	rp := FromContext(ctx)
	if rp == nil {
		t.Fatal("expected non-nil after injection")
	}

	val, ok := rp.Get("key")
	if !ok || val != "value" {
		t.Errorf("Get(key) = %v, %v", val, ok)
	}

	_, ok = rp.Get("missing")
	if ok {
		t.Error("expected ok=false for missing key")
	}
}

func TestStaticProvider(t *testing.T) {
	s := NewStatic(map[string]any{
		"x": 1,
		"y": "hello",
	})

	v, ok := s.Get("x")
	if !ok || v != 1 {
		t.Errorf("x = %v, %v", v, ok)
	}

	v, ok = s.Get("y")
	if !ok || v != "hello" {
		t.Errorf("y = %v, %v", v, ok)
	}

	_, ok = s.Get("z")
	if ok {
		t.Error("expected false for z")
	}
}

func TestDuplicateRegisterPanics(t *testing.T) {
	m := NewManager()
	m.Register("dup", func(ctx context.Context) (any, error) { return nil, nil }, time.Hour)

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate Register")
		}
	}()
	m.Register("dup", func(ctx context.Context) (any, error) { return nil, nil }, time.Hour)
}

func TestRegisterAfterStartPanics(t *testing.T) {
	m := NewManager()
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on Register after Start")
		}
	}()
	m.Register("late", func(ctx context.Context) (any, error) { return nil, nil }, time.Hour)
}

func TestDoubleStartReturnsError(t *testing.T) {
	m := NewManager()
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	if err := m.Start(context.Background()); err == nil {
		t.Error("expected error on double Start")
	}
}

func TestManagerImplementsProvider(t *testing.T) {
	// Compile-time check that *Manager satisfies ResourceProvider.
	var _ ResourceProvider = (*Manager)(nil)
}
