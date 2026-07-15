package types

import (
	"strings"
	"testing"
)

// These tests pin the operator-type method-restriction contract enforced by
// OperatorType.ValidateOutput. Historically this codepath had no direct Go
// unit test (only an end-to-end C++ case existed); it is exercised here so
// the Recall-may-write-common relaxation and the still-forbidden item
// mutations are both regression-guarded.

func TestValidateOutput_RecallMayWriteCommon(t *testing.T) {
	out := NewOperatorOutput()
	out.SetCommon("request_id", "req-123")
	out.AddItem(map[string]any{"id": "a"})
	if err := OpTypeRecall.ValidateOutput(out); err != nil {
		t.Fatalf("Recall writing common + AddItem should be allowed, got: %v", err)
	}
}

func TestValidateOutput_RecallStillForbidsItemMutations(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*OperatorOutput)
		wantSub string
	}{
		{"SetItem", func(o *OperatorOutput) { o.SetItem(0, "score", 1.0) }, "SetItem"},
		{"SetItemColumn", func(o *OperatorOutput) { o.SetItemColumnFloat64("score", []float64{1.0}) }, "SetItem"},
		{"RemoveItem", func(o *OperatorOutput) { o.RemoveItem(0) }, "RemoveItem"},
		{"SetItemOrder", func(o *OperatorOutput) { o.SetItemOrder([]int{0}) }, "SetItemOrder"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := NewOperatorOutput()
			tc.mutate(out)
			err := OpTypeRecall.ValidateOutput(out)
			if err == nil {
				t.Fatalf("Recall.%s should be forbidden, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q should mention %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestValidateOutput_TransformMayWriteCommonAndItem(t *testing.T) {
	out := NewOperatorOutput()
	out.SetCommon("x", 1)
	out.SetItem(0, "y", 2)
	if err := OpTypeTransform.ValidateOutput(out); err != nil {
		t.Fatalf("Transform writing common + item should be allowed, got: %v", err)
	}
}

func TestValidateOutput_TransformForbidsAddRemoveReorder(t *testing.T) {
	out := NewOperatorOutput()
	out.AddItem(map[string]any{"id": "a"})
	err := OpTypeTransform.ValidateOutput(out)
	if err == nil || !strings.Contains(err.Error(), "AddItem") {
		t.Fatalf("Transform.AddItem should be forbidden, got: %v", err)
	}
}

// Observe stays read-only: it may not write anything.
func TestValidateOutput_ObserveIsReadOnly(t *testing.T) {
	out := NewOperatorOutput()
	out.SetCommon("x", 1)
	err := OpTypeObserve.ValidateOutput(out)
	if err == nil || !strings.Contains(err.Error(), "SetCommon") {
		t.Fatalf("Observe.SetCommon should be forbidden, got: %v", err)
	}
}

// Clean output passes for every type — the validator never invents violations.
func TestValidateOutput_EmptyOutputAlwaysClean(t *testing.T) {
	for _, ty := range AllOperatorTypes {
		if err := ty.ValidateOutput(NewOperatorOutput()); err != nil {
			t.Errorf("empty output should be clean for %s, got: %v", ty, err)
		}
	}
}
