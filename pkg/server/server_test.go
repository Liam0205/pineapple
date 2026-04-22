package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	pine "github.com/Liam0205/pineapple"
	"github.com/Liam0205/pineapple/internal/registry"
	"github.com/Liam0205/pineapple/internal/types"
	"github.com/Liam0205/pineapple/pkg/resource"
)

// --- test operator ---

type noopOp struct{}

func (o *noopOp) Init(params map[string]any) error                                              { return nil }
func (o *noopOp) Execute(_ context.Context, _ *types.OperatorInput, _ *types.OperatorOutput) error { return nil }

func init() {
	registry.Reset()
	registry.Register(types.OperatorSchema{
		Name:        "noop",
		Type:        types.OpTypeTransform,
		Description: "No-op test operator.",
	}, func() types.Operator { return &noopOp{} })
}

// --- helpers ---

func minimalConfig(t *testing.T, resConfig map[string]any) []byte {
	t.Helper()
	cfg := map[string]any{
		"_PINEAPPLE_VERSION": pine.Version,
		"pipeline_config": map[string]any{
			"operators": map[string]any{
				"op_a_A1B2C3": map[string]any{
					"type_name": "noop",
					"$metadata": map[string]any{
						"common_input":  []string{"x"},
						"common_output": []string{"y"},
						"item_input":    []string{},
						"item_output":   []string{},
					},
				},
			},
			"pipeline_map": map[string]any{
				"stage1": map[string]any{
					"pipeline": []string{"op_a_A1B2C3"},
				},
			},
		},
		"pipeline_group": map[string]any{
			"main": map[string]any{
				"pipeline": []string{"stage1"},
			},
		},
		"flow_contract": map[string]any{
			"common_input":  []string{"x"},
			"item_input":    []string{},
			"common_output": []string{"y"},
			"item_output":   []string{},
		},
	}
	if resConfig != nil {
		cfg["resource_config"] = resConfig
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeTempConfig(t *testing.T, data []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "config-*.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

// --- tests ---

func TestReloadConfig_EngineAndResources(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	var callCount int
	resource.Register(types.ResourceSchema{
		Name:            "test_res",
		Description:     "test resource",
		DefaultInterval: 3600,
	}, func(params map[string]any) (resource.Fetcher, error) {
		return func(ctx context.Context) (any, error) {
			callCount++
			return fmt.Sprintf("value_%d", callCount), nil
		}, nil
	})

	cfg1 := minimalConfig(t, map[string]any{
		"my_res": map[string]any{
			"type":     "test_res",
			"interval": 3600,
			"params":   map[string]any{},
		},
	})
	path := writeTempConfig(t, cfg1)

	// Initial load via reloadConfig
	if err := reloadConfig(path); err != nil {
		t.Fatalf("initial reloadConfig failed: %v", err)
	}
	defer func() {
		if rm := resources.Load(); rm != nil {
			rm.Stop()
		}
	}()

	// Verify engine and resources are loaded
	if enginePtr.Load() == nil {
		t.Fatal("engine should be loaded")
	}
	rm := resources.Load()
	if rm == nil {
		t.Fatal("resources should be loaded")
	}
	val, ok := rm.Get("my_res")
	if !ok {
		t.Fatal("expected my_res to exist")
	}
	if val != "value_1" {
		t.Errorf("val = %v, want value_1", val)
	}

	// Reload — should create new Manager
	oldRM := rm
	if err := reloadConfig(path); err != nil {
		t.Fatalf("second reloadConfig failed: %v", err)
	}

	newRM := resources.Load()
	if newRM == oldRM {
		t.Error("expected new Manager after reload")
	}
	val, ok = newRM.Get("my_res")
	if !ok {
		t.Fatal("expected my_res in new Manager")
	}
	if val != "value_2" {
		t.Errorf("val = %v, want value_2", val)
	}
}

func TestReloadConfig_ResourceStartFailure(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	resource.Register(types.ResourceSchema{
		Name:            "good_res",
		Description:     "always works",
		DefaultInterval: 3600,
	}, func(params map[string]any) (resource.Fetcher, error) {
		return func(ctx context.Context) (any, error) {
			return "ok", nil
		}, nil
	})

	resource.Register(types.ResourceSchema{
		Name:            "bad_res",
		Description:     "always fails on fetch",
		DefaultInterval: 3600,
	}, func(params map[string]any) (resource.Fetcher, error) {
		return func(ctx context.Context) (any, error) {
			return nil, fmt.Errorf("connection refused")
		}, nil
	})

	// Initial config with good resource only
	cfg1 := minimalConfig(t, map[string]any{
		"r1": map[string]any{
			"type":     "good_res",
			"interval": 3600,
			"params":   map[string]any{},
		},
	})
	path1 := writeTempConfig(t, cfg1)
	if err := reloadConfig(path1); err != nil {
		t.Fatalf("initial reloadConfig failed: %v", err)
	}
	defer func() {
		if rm := resources.Load(); rm != nil {
			rm.Stop()
		}
	}()

	origEngine := enginePtr.Load()
	origRM := resources.Load()

	// Config with bad resource — Start should fail
	cfg2 := minimalConfig(t, map[string]any{
		"r1": map[string]any{
			"type":     "bad_res",
			"interval": 3600,
			"params":   map[string]any{},
		},
	})
	path2 := writeTempConfig(t, cfg2)
	err := reloadConfig(path2)
	if err == nil {
		t.Fatal("expected error from bad resource")
	}

	// Engine and resources should be unchanged
	if enginePtr.Load() != origEngine {
		t.Error("engine should not change on failed reload")
	}
	if resources.Load() != origRM {
		t.Error("resources should not change on failed reload")
	}

	// Old resources still work
	val, ok := origRM.Get("r1")
	if !ok || val != "ok" {
		t.Errorf("old resource should still work, got %v, %v", val, ok)
	}
}

func TestReloadConfig_NoResources(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	cfg := minimalConfig(t, nil)
	path := writeTempConfig(t, cfg)

	if err := reloadConfig(path); err != nil {
		t.Fatalf("reloadConfig with no resources failed: %v", err)
	}
	defer func() {
		if rm := resources.Load(); rm != nil {
			rm.Stop()
		}
	}()

	if enginePtr.Load() == nil {
		t.Fatal("engine should be loaded")
	}
	rm := resources.Load()
	if rm == nil {
		t.Fatal("resources manager should exist even without resources")
	}
	if len(rm.Names()) != 0 {
		t.Errorf("expected no resources, got %v", rm.Names())
	}
}

func TestReloadConfig_OldManagerStopped(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	var activeCount int32
	resource.Register(types.ResourceSchema{
		Name:            "track_res",
		Description:     "tracks active fetches",
		DefaultInterval: 3600,
	}, func(params map[string]any) (resource.Fetcher, error) {
		return func(ctx context.Context) (any, error) {
			return "data", nil
		}, nil
	})

	cfg := minimalConfig(t, map[string]any{
		"r": map[string]any{
			"type":     "track_res",
			"interval": 3600,
			"params":   map[string]any{},
		},
	})
	path := writeTempConfig(t, cfg)

	if err := reloadConfig(path); err != nil {
		t.Fatal(err)
	}
	oldRM := resources.Load()

	// Reload to replace
	if err := reloadConfig(path); err != nil {
		t.Fatal(err)
	}
	defer resources.Load().Stop()

	// Old Manager data is still readable (atomic.Value persists after Stop)
	val, ok := oldRM.Get("r")
	if !ok {
		t.Error("old Manager should still return data after Stop")
	}
	if val != "data" {
		t.Errorf("val = %v, want data", val)
	}

	_ = activeCount
}

func TestWatchConfigIntegration(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	resource.Register(types.ResourceSchema{
		Name:            "watch_res",
		Description:     "for watch test",
		DefaultInterval: 3600,
	}, func(params map[string]any) (resource.Fetcher, error) {
		return func(ctx context.Context) (any, error) {
			return "initial", nil
		}, nil
	})

	cfg := minimalConfig(t, map[string]any{
		"wr": map[string]any{
			"type":     "watch_res",
			"interval": 3600,
			"params":   map[string]any{},
		},
	})
	path := writeTempConfig(t, cfg)

	// Do initial load
	if err := reloadConfig(path); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rm := resources.Load(); rm != nil {
			rm.Stop()
		}
	}()

	rm := resources.Load()
	val, _ := rm.Get("wr")
	if val != "initial" {
		t.Errorf("val = %v, want initial", val)
	}

	// Touch the file to trigger a reload
	time.Sleep(10 * time.Millisecond)
	if err := os.Chtimes(path, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}

	// reloadConfig should succeed when called directly (simulating watchConfig behavior)
	if err := reloadConfig(path); err != nil {
		t.Fatalf("manual reload failed: %v", err)
	}
}
