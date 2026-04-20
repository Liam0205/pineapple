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

// --- FetcherFactory / LoadConfig / Names tests ---

func TestRegisterFetcherAndLoadConfig(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	RegisterFetcher("test_type", func(params map[string]any) (Fetcher, error) {
		prefix, _ := params["prefix"].(string)
		return func(ctx context.Context) (any, error) {
			return prefix + "_loaded", nil
		}, nil
	})

	m := NewManager()
	config := `{
		"my_resource": {
			"type": "test_type",
			"interval": 600,
			"params": {"prefix": "hello"}
		}
	}`
	if err := m.LoadConfig([]byte(config)); err != nil {
		t.Fatal(err)
	}

	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	val, ok := m.Get("my_resource")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if val != "hello_loaded" {
		t.Errorf("val = %v, want hello_loaded", val)
	}
}

func TestLoadConfigUnknownType(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	m := NewManager()
	config := `{
		"bad": {
			"type": "nonexistent_type",
			"interval": 60,
			"params": {}
		}
	}`
	err := m.LoadConfig([]byte(config))
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestLoadConfigFactoryError(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	RegisterFetcher("fail_type", func(params map[string]any) (Fetcher, error) {
		return nil, fmt.Errorf("missing required param")
	})

	m := NewManager()
	config := `{
		"broken": {
			"type": "fail_type",
			"interval": 60,
			"params": {}
		}
	}`
	err := m.LoadConfig([]byte(config))
	if err == nil {
		t.Fatal("expected error from factory")
	}
}

func TestLoadConfigInvalidJSON(t *testing.T) {
	m := NewManager()
	err := m.LoadConfig([]byte(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadConfigDefaultInterval(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	RegisterFetcher("simple", func(params map[string]any) (Fetcher, error) {
		return func(ctx context.Context) (any, error) {
			return "ok", nil
		}, nil
	})

	m := NewManager()
	config := `{
		"res": {
			"type": "simple",
			"interval": 0,
			"params": {}
		}
	}`
	if err := m.LoadConfig([]byte(config)); err != nil {
		t.Fatal(err)
	}

	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	val, ok := m.Get("res")
	if !ok || val != "ok" {
		t.Errorf("Get(res) = %v, %v", val, ok)
	}
}

func TestDuplicateRegisterFetcherPanics(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	factory := func(params map[string]any) (Fetcher, error) { return nil, nil }
	RegisterFetcher("dup_type", factory)

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate RegisterFetcher")
		}
	}()
	RegisterFetcher("dup_type", factory)
}

func TestNames(t *testing.T) {
	m := NewManager()
	m.Register("bravo", func(ctx context.Context) (any, error) { return nil, nil }, time.Hour)
	m.Register("alpha", func(ctx context.Context) (any, error) { return nil, nil }, time.Hour)
	m.Register("charlie", func(ctx context.Context) (any, error) { return nil, nil }, time.Hour)

	names := m.Names()
	expected := []string{"alpha", "bravo", "charlie"}
	if len(names) != len(expected) {
		t.Fatalf("Names() = %v, want %v", names, expected)
	}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("Names()[%d] = %q, want %q", i, name, expected[i])
		}
	}
}

func TestNamesEmpty(t *testing.T) {
	m := NewManager()
	names := m.Names()
	if len(names) != 0 {
		t.Errorf("Names() = %v, want empty", names)
	}
}

func TestLoadConfigWithManualRegister(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	RegisterFetcher("cfg_type", func(params map[string]any) (Fetcher, error) {
		return func(ctx context.Context) (any, error) {
			return "from_config", nil
		}, nil
	})

	m := NewManager()
	// Manual register first
	m.Register("manual_res", func(ctx context.Context) (any, error) {
		return "from_manual", nil
	}, time.Hour)

	// Then config
	config := `{
		"config_res": {
			"type": "cfg_type",
			"interval": 300,
			"params": {}
		}
	}`
	if err := m.LoadConfig([]byte(config)); err != nil {
		t.Fatal(err)
	}

	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	v1, ok := m.Get("manual_res")
	if !ok || v1 != "from_manual" {
		t.Errorf("manual_res = %v, %v", v1, ok)
	}
	v2, ok := m.Get("config_res")
	if !ok || v2 != "from_config" {
		t.Errorf("config_res = %v, %v", v2, ok)
	}

	names := m.Names()
	if len(names) != 2 {
		t.Errorf("Names() = %v, want 2 entries", names)
	}
}
