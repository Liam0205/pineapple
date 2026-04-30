package integration

import (
	"context"
	"testing"

	pine "github.com/Liam0205/pineapple"
	_ "github.com/Liam0205/pineapple/operators"
)

// TestDeepNestedE2E validates the deep nested SubFlow pipeline
// compiled from the Apple DSL executes correctly with multi-level skip.
func TestDeepNestedE2E(t *testing.T) {
	cfg := loadConfig(t, "../testdata/e2e_deep_nested.json")
	engine, err := pine.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		common   map[string]any
		wantMark map[string]bool // field -> should be present (non-nil)
	}{
		{
			name: "all_enabled",
			common: map[string]any{
				"enabled": true, "flag_l1": true, "flag_l2": true, "flag_l3": true,
			},
			wantMark: map[string]bool{
				"mark_l1": true, "mark_l2": true, "mark_l3": true, "mark_leaf": true, "mark_else": false,
			},
		},
		{
			name: "enabled_but_l2_disabled",
			common: map[string]any{
				"enabled": true, "flag_l1": true, "flag_l2": false, "flag_l3": true,
			},
			wantMark: map[string]bool{
				"mark_l1": true, "mark_l2": true, "mark_l3": false, "mark_leaf": false, "mark_else": false,
			},
		},
		{
			name: "enabled_but_l1_disabled",
			common: map[string]any{
				"enabled": true, "flag_l1": false, "flag_l2": true, "flag_l3": true,
			},
			wantMark: map[string]bool{
				"mark_l1": true, "mark_l2": false, "mark_l3": false, "mark_leaf": false, "mark_else": false,
			},
		},
		{
			// When enabled=false: _if_1=true, all ops inside the if-branch
			// have _if_1 in their skip list → all correctly skipped.
			// Only the else branch executes.
			name: "disabled_else_branch",
			common: map[string]any{
				"enabled": false, "flag_l1": true, "flag_l2": true, "flag_l3": true,
			},
			wantMark: map[string]bool{
				"mark_l1": false, "mark_l2": false, "mark_l3": false, "mark_leaf": false, "mark_else": true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &pine.Request{Common: tc.common}
			result, err := engine.Execute(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Items) != 1 {
				t.Fatalf("expected 1 item, got %d", len(result.Items))
			}
			item := result.Items[0]
			for field, want := range tc.wantMark {
				got := item[field]
				if want && got == nil {
					t.Errorf("expected %q to be set, got nil", field)
				}
				if !want && got != nil {
					t.Errorf("expected %q to be nil (skipped), got %v", field, got)
				}
			}
		})
	}
}
