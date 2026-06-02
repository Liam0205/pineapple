package config

import "testing"

func TestCommonReadFields_NoOptionalBuckets(t *testing.T) {
	m := Metadata{CommonInput: []string{"a", "b"}}
	got := m.CommonReadFields()
	want := []string{"a", "b"}
	if len(got) != len(want) || got[0] != "a" || got[1] != "b" {
		t.Errorf("got %v, want %v", got, want)
	}
	// Identity short-circuit: nothing to union, same backing array.
	if &got[0] != &m.CommonInput[0] {
		t.Errorf("expected identity return when skip/template empty")
	}
}

func TestCommonReadFields_UnionAndDedup(t *testing.T) {
	m := Metadata{
		CommonInput:         []string{"uid"},
		CommonInputSkip:     []string{"_if_branch", "uid"}, // overlap with business
		CommonInputTemplate: []string{"tenant_id", "uid"},  // overlap with business
	}
	got := m.CommonReadFields()
	want := []string{"uid", "_if_branch", "tenant_id"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, f := range want {
		if got[i] != f {
			t.Errorf("idx %d: got %q want %q", i, got[i], f)
		}
	}
}

func TestComputeInputFieldSpec_ExcludesSkipBucket(t *testing.T) {
	meta := Metadata{
		CommonInput:     []string{"uid"},
		CommonInputSkip: []string{"_if_branch"},
	}
	spec := ComputeInputFieldSpec(meta, nil, nil, nil, nil, nil)
	if len(spec.NullableCommon) != 1 || spec.NullableCommon[0] != "uid" {
		t.Errorf("NullableCommon = %v, want [uid]", spec.NullableCommon)
	}
}

func TestComputeInputFieldSpec_ExcludesTemplateBucket(t *testing.T) {
	meta := Metadata{
		CommonInput:         []string{"uid"},
		CommonInputTemplate: []string{"tenant_id"},
	}
	spec := ComputeInputFieldSpec(meta, nil, nil, nil, nil, nil)
	if len(spec.NullableCommon) != 1 || spec.NullableCommon[0] != "uid" {
		t.Errorf("NullableCommon = %v, want [uid]", spec.NullableCommon)
	}
}

func TestComputeInputFieldSpec_LegacySkipArgStillFilters(t *testing.T) {
	// Backward-compat path: skip field placed directly into common_input,
	// with the field name passed via the skip argument.
	meta := Metadata{CommonInput: []string{"uid", "_if_branch"}}
	spec := ComputeInputFieldSpec(meta, nil, nil, nil, nil, []string{"_if_branch"})
	if len(spec.NullableCommon) != 1 || spec.NullableCommon[0] != "uid" {
		t.Errorf("NullableCommon = %v, want [uid]", spec.NullableCommon)
	}
}

func TestComputeInputFieldSpec_DefensiveExcludeWhenTemplateInCommonInput(t *testing.T) {
	// Hand-edited config where the template source field accidentally
	// also appears in common_input. The exclusion must still apply so
	// the operator-visible input matches the new contract.
	meta := Metadata{
		CommonInput:         []string{"uid", "tenant_id"},
		CommonInputTemplate: []string{"tenant_id"},
	}
	spec := ComputeInputFieldSpec(meta, nil, nil, nil, nil, nil)
	if len(spec.NullableCommon) != 1 || spec.NullableCommon[0] != "uid" {
		t.Errorf("NullableCommon = %v, want [uid]", spec.NullableCommon)
	}
}
