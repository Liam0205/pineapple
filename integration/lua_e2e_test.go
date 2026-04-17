package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"

	pine "github.com/Liam0205/pineapple"
	_ "github.com/Liam0205/pineapple/operators"
)

func TestLuaPipelineE2E(t *testing.T) {
	cfg := loadConfig(t, "../testdata/e2e_lua_pipeline.json")
	engine, err := pine.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Test with a young user (age < 18 → 20% discount)
	req := &pine.Request{
		Common: map[string]any{"user_age": 15.0},
	}
	result, err := engine.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	// Items: a=100, b=200, c=50, d=300
	// With 20% discount: a=80, b=160, c=40, d=240
	// avg_price = (80+160+40+240)/4 = 130
	// item_count = 4
	if result.Common["avg_price"] != 130.0 {
		t.Errorf("avg_price: expected 130, got %v", result.Common["avg_price"])
	}
	if result.Common["item_count"] != 4.0 {
		t.Errorf("item_count: expected 4, got %v", result.Common["item_count"])
	}

	// avg_price=130 < 200, so _skip_sort=false → sort should execute
	// Sorted desc by final_price: d=240, b=160, a=80, c=40
	expectedPrices := []float64{240.0, 160.0, 80.0, 40.0}
	if len(result.Items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(result.Items))
	}
	for i, expected := range expectedPrices {
		got := result.Items[i]["item_final_price"]
		if got != expected {
			t.Errorf("item[%d] final_price: expected %v, got %v", i, expected, got)
		}
	}
}

func TestLuaPipelineAdult(t *testing.T) {
	cfg := loadConfig(t, "../testdata/e2e_lua_pipeline.json")
	engine, err := pine.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Adult user (18 <= age <= 60 → no discount)
	req := &pine.Request{
		Common: map[string]any{"user_age": 30.0},
	}
	result, err := engine.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	// Prices unchanged: a=100, b=200, c=50, d=300
	// avg_price = 650/4 = 162.5
	if result.Common["avg_price"] != 162.5 {
		t.Errorf("avg_price: expected 162.5, got %v", result.Common["avg_price"])
	}

	// avg_price=162.5 < 200 → sort executes
	// Sorted desc: d=300, b=200, a=100, c=50
	expectedIDs := []string{"d", "b", "a", "c"}
	for i, expected := range expectedIDs {
		if result.Items[i]["item_id"] != expected {
			t.Errorf("item[%d] id: expected %s, got %v", i, expected, result.Items[i]["item_id"])
		}
	}
}

func TestLuaPipelineConcurrent(t *testing.T) {
	cfg := loadConfig(t, "../testdata/e2e_lua_pipeline.json")
	engine, err := pine.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(age float64) {
			defer wg.Done()
			result, err := engine.Execute(context.Background(), &pine.Request{
				Common: map[string]any{"user_age": age},
			})
			if err != nil {
				errs <- err
				return
			}
			if len(result.Items) != 4 {
				errs <- fmt.Errorf("age=%v: expected 4 items, got %d", age, len(result.Items))
			}
		}(float64(10 + i*3))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
