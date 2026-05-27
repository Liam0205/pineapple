package integration

import (
	"context"
	"os"
	"strings"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
	_ "github.com/Liam0205/pineapple/pine-go/operators"
)

// TestAppleDSLStrictFieldE2E validates the full path:
// Apple DSL declares strict_common → compiled JSON contains "strict_common" →
// pine-go reads it → nil value triggers ExecutionError.
//
// The JSON fixture is written by the Python e2e test (test_e2e.py).
func TestAppleDSLStrictFieldE2E(t *testing.T) {
	path := "../testdata/e2e_apple_strict_field.json"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("fixture not found (run Python e2e test first): %v", err)
	}

	engine, err := pine.NewEngine(data)
	if err != nil {
		t.Fatal(err)
	}

	// Case 1: non-nil value should succeed
	req := &pine.Request{
		Common: map[string]any{"age": 25.0},
	}
	result, err := engine.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("expected success with non-nil strict field, got error: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result.Items))
	}

	// Case 2: nil value should fail with strict validation error
	reqNil := &pine.Request{
		Common: map[string]any{"age": nil},
	}
	_, err = engine.Execute(context.Background(), reqNil)
	if err == nil {
		t.Fatal("expected ExecutionError for nil strict field, got nil")
	}
	if !strings.Contains(err.Error(), "nil") || !strings.Contains(err.Error(), "age") {
		t.Fatalf("error should mention nil and field name 'age', got: %v", err)
	}
}
