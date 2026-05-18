package resource

import (
	"context"
	"testing"
	"time"
)

func TestValidateResourceDeps(t *testing.T) {
	m := NewManager()
	m.Register("feed_data", func(ctx context.Context) (any, error) { return "ok", nil }, time.Hour)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	pipelineJSON := `{
		"pipeline_config": {
			"operators": {
				"recall_feed_data_ABC123": {
					"type_name": "recall_feed_data",
					"resource_name": "feed_data",
					"recall": true
				},
				"transform_by_lua_XYZ": {
					"type_name": "transform_by_lua",
					"lua_script": "return 1"
				}
			}
		}
	}`

	if err := ValidateResourceDeps([]byte(pipelineJSON), m); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateResourceDepsMissing(t *testing.T) {
	m := NewManager()
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	pipelineJSON := `{
		"pipeline_config": {
			"operators": {
				"recall_feed_data_ABC123": {
					"type_name": "recall_feed_data",
					"resource_name": "feed_data",
					"recall": true
				}
			}
		}
	}`

	err := ValidateResourceDeps([]byte(pipelineJSON), m)
	if err == nil {
		t.Fatal("expected error for missing resource")
	}
	if got := err.Error(); !contains(got, "feed_data") {
		t.Errorf("error should mention 'feed_data', got: %s", got)
	}
}

func TestValidateResourceDepsNoResourceRefs(t *testing.T) {
	m := NewManager()
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	pipelineJSON := `{
		"pipeline_config": {
			"operators": {
				"transform_by_lua_XYZ": {
					"type_name": "transform_by_lua",
					"lua_script": "return 1"
				}
			}
		}
	}`

	if err := ValidateResourceDeps([]byte(pipelineJSON), m); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateResourceDepsInvalidJSON(t *testing.T) {
	m := NewManager()
	err := ValidateResourceDeps([]byte(`{invalid`), m)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstr(s, sub))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
