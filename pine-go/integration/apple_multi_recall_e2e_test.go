package integration

import (
	"context"
	"os"
	"sort"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
	_ "github.com/Liam0205/pineapple/pine-go/operators"
)

// TestAppleDSLMultiRecallE2E covers issue #72: two recall_static operators
// independently writing item_score must validate at DSL time and execute
// end-to-end on the Go engine, with merge_dedup combining both row sets
// into a single result and reorder_sort producing a deterministic order.
//
// The JSON fixture is written by the Python e2e test (test_e2e.py).
func TestAppleDSLMultiRecallE2E(t *testing.T) {
	path := "../testdata/e2e_apple_multi_recall.json"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("fixture not found (run Python e2e test first): %v", err)
	}

	engine, err := pine.NewEngine(data)
	if err != nil {
		t.Fatal(err)
	}

	req := &pine.Request{
		Common: map[string]any{"user_id": "u1"},
	}
	result, err := engine.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("multi-recall pipeline failed: %v", err)
	}

	// recall_a contributes (a1, 1.0), (a2, 2.0); recall_b contributes (b1, 3.0).
	// Sorted desc by item_score: b1=3, a2=2, a1=1.
	if len(result.Items) != 3 {
		t.Fatalf("expected 3 merged items, got %d: %v", len(result.Items), result.Items)
	}

	expectedOrder := []struct {
		id    string
		score float64
	}{
		{"b1", 3.0},
		{"a2", 2.0},
		{"a1", 1.0},
	}
	for i, want := range expectedOrder {
		gotID := result.Items[i]["item_id"]
		gotScore := result.Items[i]["item_score"]
		if gotID != want.id || gotScore != want.score {
			t.Errorf("item[%d]: want (%s, %v), got (%v, %v)",
				i, want.id, want.score, gotID, gotScore)
		}
	}

	// Sanity: item_id set is exactly the union of both recalls (no duplicate
	// dropped, since merge_dedup with strategy=first only collapses on
	// repeated ids — there are none here).
	ids := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		ids = append(ids, item["item_id"].(string))
	}
	sort.Strings(ids)
	want := []string{"a1", "a2", "b1"}
	for i, w := range want {
		if ids[i] != w {
			t.Errorf("merged id set: want %v, got %v", want, ids)
			break
		}
	}
}
