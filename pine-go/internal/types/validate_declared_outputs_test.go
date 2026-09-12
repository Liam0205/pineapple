package types

import (
	"testing"
)

// These tests pin the declared-output contract enforced by
// ValidateDeclaredOutputs (issue #205). The DAG's hazard inference is derived
// entirely from the declared $metadata field lists, so a write to an
// undeclared field carries no RAW/WAW/WAR edge — the engine rejects it right
// after the operator-type method check and before apply_output.
//
// The exact message text is part of the cross-runtime error contract; the
// end-to-end byte-exact lock lives in fixtures/errors/
// runtime_undeclared_item_output.json and runtime_undeclared_common_output.json.

func TestValidateDeclaredOutputs_AllChannelsCompliant(t *testing.T) {
	out := NewOperatorOutput()
	out.SetCommon("request_id", "req-1")
	out.SetItem(0, "score", 1.0)
	out.SetItemColumnFloat64("rank", []float64{1.0})
	out.AddItem(map[string]any{"item_id": "a", "score": 2.0})

	err := ValidateDeclaredOutputs(out,
		[]string{"request_id", "unused_but_declared"},
		[]string{"score", "rank", "item_id"})
	if err != nil {
		t.Fatalf("writes within declared outputs should pass, got: %v", err)
	}
}

func TestValidateDeclaredOutputs_EmptyOutputIsCompliant(t *testing.T) {
	if err := ValidateDeclaredOutputs(NewOperatorOutput(), nil, nil); err != nil {
		t.Fatalf("an operator that writes nothing should pass, got: %v", err)
	}
}

// Each of the four field-carrying write paths must be checked: the issue as
// filed named only SetItem and SetCommon, but SetItemColumnFloat64 and
// AddItem carry field names too (AddItem's come from the operator's config in
// recall_static, which is the least controlled source of all).
func TestValidateDeclaredOutputs_EachWritePathIsChecked(t *testing.T) {
	cases := []struct {
		name  string
		write func(*OperatorOutput)
		want  string
	}{
		{
			"SetCommon",
			func(o *OperatorOutput) { o.SetCommon("undeclared", 1.0) },
			`operator wrote undeclared common output field(s) [undeclared]`,
		},
		{
			"SetItem",
			func(o *OperatorOutput) { o.SetItem(0, "undeclared", 1.0) },
			`operator wrote undeclared item output field(s) [undeclared]`,
		},
		{
			"SetItemColumnFloat64",
			func(o *OperatorOutput) { o.SetItemColumnFloat64("undeclared", []float64{1.0}) },
			`operator wrote undeclared item output field(s) [undeclared]`,
		},
		{
			"AddItem",
			func(o *OperatorOutput) { o.AddItem(map[string]any{"undeclared": 1.0}) },
			`operator wrote undeclared item output field(s) [undeclared]`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := NewOperatorOutput()
			tc.write(out)
			err := ValidateDeclaredOutputs(out, []string{"declared"}, []string{"declared"})
			if err == nil {
				t.Fatalf("%s to an undeclared field should be rejected", tc.name)
			}
			if err.Error() != tc.want {
				t.Errorf("message mismatch:\n got: %s\nwant: %s", err.Error(), tc.want)
			}
		})
	}
}

// Field names are reported sorted so the message does not depend on map
// iteration order (commonWrites and AddItem payloads are maps) — a message
// that varied per run could not be locked byte-exactly across runtimes.
func TestValidateDeclaredOutputs_FieldNamesAreSortedAndDeduplicated(t *testing.T) {
	out := NewOperatorOutput()
	out.SetItem(0, "zeta", 1.0)
	out.SetItem(1, "zeta", 2.0) // same field twice — must appear once
	out.SetItem(0, "alpha", 3.0)
	out.SetItemColumnFloat64("mid", []float64{1.0})
	out.AddItem(map[string]any{"alpha": 4.0, "beta": 5.0}) // alpha already seen

	err := ValidateDeclaredOutputs(out, nil, nil)
	if err == nil {
		t.Fatal("expected rejection")
	}
	want := `operator wrote undeclared item output field(s) [alpha beta mid zeta]`
	if err.Error() != want {
		t.Errorf("message mismatch:\n got: %s\nwant: %s", err.Error(), want)
	}
}

func TestValidateDeclaredOutputs_MultipleCommonFieldsSorted(t *testing.T) {
	out := NewOperatorOutput()
	out.SetCommon("trace_id", "t")
	out.SetCommon("request_id", "r")

	err := ValidateDeclaredOutputs(out, nil, nil)
	if err == nil {
		t.Fatal("expected rejection")
	}
	want := `operator wrote undeclared common output field(s) [request_id trace_id]`
	if err.Error() != want {
		t.Errorf("message mismatch:\n got: %s\nwant: %s", err.Error(), want)
	}
}

// The common channel is checked first and returns immediately, so a violation
// on both channels reports only the common fields. The three runtimes must
// agree on this precedence or their messages diverge for the same config.
func TestValidateDeclaredOutputs_CommonChannelTakesPrecedence(t *testing.T) {
	out := NewOperatorOutput()
	out.SetCommon("undeclared_common", 1.0)
	out.SetItem(0, "undeclared_item", 2.0)

	err := ValidateDeclaredOutputs(out, nil, nil)
	if err == nil {
		t.Fatal("expected rejection")
	}
	want := `operator wrote undeclared common output field(s) [undeclared_common]`
	if err.Error() != want {
		t.Errorf("common channel should be reported alone:\n got: %s\nwant: %s", err.Error(), want)
	}
}

// Only the offending fields are named: a partially compliant write must not
// drag its declared siblings into the message.
func TestValidateDeclaredOutputs_OnlyUndeclaredFieldsAreReported(t *testing.T) {
	out := NewOperatorOutput()
	out.SetItem(0, "declared", 1.0)
	out.SetItem(0, "undeclared", 2.0)

	err := ValidateDeclaredOutputs(out, nil, []string{"declared"})
	if err == nil {
		t.Fatal("expected rejection")
	}
	want := `operator wrote undeclared item output field(s) [undeclared]`
	if err.Error() != want {
		t.Errorf("message mismatch:\n got: %s\nwant: %s", err.Error(), want)
	}
}

// RemoveItem and SetItemOrder carry no field names, so a row-set mutator that
// declares no outputs stays compliant. Filter/Reorder operators are exactly
// this shape, so a regression here would break every such pipeline.
func TestValidateDeclaredOutputs_FieldlessWritesNeedNoDeclaration(t *testing.T) {
	out := NewOperatorOutput()
	out.RemoveItem(0)
	out.SetItemOrder([]int{1, 0})

	if err := ValidateDeclaredOutputs(out, nil, nil); err != nil {
		t.Fatalf("RemoveItem/SetItemOrder declare no fields, got: %v", err)
	}
}
