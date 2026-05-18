package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
	_ "github.com/Liam0205/pineapple/pine-go/operators"
)

func loadConfig(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestFullPipelineE2E(t *testing.T) {
	cfg := loadConfig(t, "../testdata/e2e_full_pipeline.json")
	engine, err := pine.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}

	req := &pine.Request{
		Common: map[string]any{"scene": "homepage"},
		Items:  nil,
	}
	result, err := engine.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	// Verify: offline items removed (item_2 was offline)
	for _, item := range result.Items {
		if item["item_status"] == "offline" {
			t.Errorf("offline item not filtered: %v", item)
		}
	}

	// Verify: duplicates removed (item_3 appeared in both recalls)
	ids := make(map[any]int)
	for _, item := range result.Items {
		ids[item["item_id"]]++
	}
	for id, count := range ids {
		if count > 1 {
			t.Errorf("duplicate item_id %v found %d times", id, count)
		}
	}

	// Expected surviving items: item_1(80), item_3(90), item_4(70), item_5(50)
	// (item_2 filtered out, item_3 deduped keeping first from recall_hot)
	if len(result.Items) != 4 {
		t.Fatalf("expected 4 items, got %d: %v", len(result.Items), result.Items)
	}

	// Verify: sorted by score descending
	for i := 1; i < len(result.Items); i++ {
		prev := result.Items[i-1]["item_score"].(float64)
		curr := result.Items[i]["item_score"].(float64)
		if prev < curr {
			t.Errorf("items not sorted desc: item[%d] score=%f < item[%d] score=%f", i-1, prev, i, curr)
		}
	}

	// Verify: scene dispatched to all items
	for i, item := range result.Items {
		if item["item_scene"] != "homepage" {
			t.Errorf("item[%d] missing item_scene: %v", i, item)
		}
	}

	// Verify: normalized scores exist and are in [0, 1]
	for i, item := range result.Items {
		norm, ok := item["item_score_norm"].(float64)
		if !ok {
			t.Errorf("item[%d] missing item_score_norm", i)
			continue
		}
		if norm < 0 || norm > 1 {
			t.Errorf("item[%d] score_norm=%f out of range [0,1]", i, norm)
		}
	}

	// Verify: no warnings
	if len(result.Warnings) > 0 {
		t.Errorf("unexpected warnings: %v", result.Warnings)
	}

	// Verify: order is item_3(90) -> item_1(80) -> item_4(70) -> item_5(50)
	expectedOrder := []string{"item_3", "item_1", "item_4", "item_5"}
	for i, expected := range expectedOrder {
		if result.Items[i]["item_id"] != expected {
			t.Errorf("item[%d] = %v, want %s", i, result.Items[i]["item_id"], expected)
		}
	}
}

func TestFullPipelineConcurrent(t *testing.T) {
	cfg := loadConfig(t, "../testdata/e2e_full_pipeline.json")
	engine, err := pine.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := engine.Execute(context.Background(), &pine.Request{
				Common: map[string]any{"scene": "feed"},
			})
			if err != nil {
				errs <- err
				return
			}
			if len(result.Items) != 4 {
				errs <- fmt.Errorf("expected 4 items, got %d", len(result.Items))
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestNewEngineInvalidJSON_E2E(t *testing.T) {
	_, err := pine.NewEngine([]byte(`{not valid json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	var cfgErr *pine.ConfigError
	if !errors.As(err, &cfgErr) {
		t.Errorf("expected ConfigError, got %T: %v", err, err)
	}
}

func TestNewEngineMissingOperator_E2E(t *testing.T) {
	cfg := loadConfig(t, "../testdata/negative_unknown_operator.json")
	_, err := pine.NewEngine(cfg)
	if err == nil {
		t.Fatal("expected error for unknown operator type")
	}
	var regErr *pine.RegistryError
	if !errors.As(err, &regErr) {
		t.Errorf("expected RegistryError, got %T: %v", err, err)
	}
}

func TestExecuteMissingCommonField_E2E(t *testing.T) {
	cfg := loadConfig(t, "../testdata/e2e_full_pipeline.json")
	engine, err := pine.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// flow_contract requires common_input: ["scene"], omit it
	req := &pine.Request{
		Common: map[string]any{},
		Items:  nil,
	}
	_, err = engine.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for missing common field")
	}
	var valErr *pine.ValidationError
	if !errors.As(err, &valErr) {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
}
