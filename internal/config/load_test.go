package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testdataPath(name string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", name)
}

func mustReadTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(testdataPath(name))
	if err != nil {
		t.Fatalf("read testdata %q: %v", name, err)
	}
	return data
}

func TestLoadMinimalValid(t *testing.T) {
	cfg, err := Load(mustReadTestdata(t, "minimal_valid.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PineappleVersion == "" {
		t.Error("version should not be empty")
	}
	if len(cfg.PipelineConfig.Operators) != 2 {
		t.Errorf("operators count = %d", len(cfg.PipelineConfig.Operators))
	}

	// Check RawParams extraction
	opA := cfg.PipelineConfig.Operators["op_a_A1B2C3"]
	if opA.TypeName != "noop" {
		t.Errorf("op_a type_name = %q", opA.TypeName)
	}
	if opA.RawParams["custom_param"] != "hello" {
		t.Errorf("op_a custom_param = %v", opA.RawParams["custom_param"])
	}
	// Reserved keys should not appear in RawParams
	if _, ok := opA.RawParams["type_name"]; ok {
		t.Error("type_name should not be in RawParams")
	}
	if _, ok := opA.RawParams["$metadata"]; ok {
		t.Error("$metadata should not be in RawParams")
	}
	if _, ok := opA.RawParams["$code_info"]; ok {
		t.Error("$code_info should not be in RawParams")
	}

	opB := cfg.PipelineConfig.Operators["op_b_D4E5F6"]
	// threshold is a business param (float64 from JSON)
	if opB.RawParams["threshold"] != 0.5 {
		t.Errorf("op_b threshold = %v", opB.RawParams["threshold"])
	}
}

func TestLoadRecallMerge(t *testing.T) {
	cfg, err := Load(mustReadTestdata(t, "recall_merge.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	recall := cfg.PipelineConfig.Operators["recall_idx_A1"]
	if !recall.Recall {
		t.Error("recall_idx_A1 should have recall=true")
	}
	merge := cfg.PipelineConfig.Operators["merge_C3"]
	if len(merge.Sources) != 2 {
		t.Errorf("merge sources = %v", merge.Sources)
	}
}

func TestLoadSkipBranch(t *testing.T) {
	cfg, err := Load(mustReadTestdata(t, "skip_branch.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	ctrl := cfg.PipelineConfig.Operators["_ctrl_1_A1"]
	if !ctrl.ForBranchControl {
		t.Error("ctrl should have for_branch_control=true")
	}

	branch := cfg.PipelineConfig.Operators["op_branch_B2"]
	if branch.Skip != "_if_1" {
		t.Errorf("branch skip = %q", branch.Skip)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	_, err := Load([]byte("{bad json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestLoadMissingTypeName(t *testing.T) {
	data := []byte(`{
		"pipeline_config": {
			"operators": {
				"bad_op": {
					"$metadata": {"common_input":[], "common_output":[], "item_input":[], "item_output":[]}
				}
			},
			"pipeline_map": {"s": {"pipeline": ["bad_op"]}}
		},
		"pipeline_group": {"main": {"pipeline": ["s"]}},
		"flow_contract": {}
	}`)
	_, err := Load(data)
	if err == nil {
		t.Error("expected error for missing type_name")
	}
}

func TestLoadEmptyOperators(t *testing.T) {
	data := []byte(`{
		"pipeline_config": {"operators": {}, "pipeline_map": {}},
		"pipeline_group": {"main": {"pipeline": []}},
		"flow_contract": {}
	}`)
	_, err := Load(data)
	if err == nil {
		t.Error("expected error for empty operators")
	}
}

func TestLoadInvalidSourcesReference(t *testing.T) {
	data := []byte(`{
		"pipeline_config": {
			"operators": {
				"merge_op": {
					"type_name": "merge",
					"$metadata": {"common_input":[], "common_output":[], "item_input":[], "item_output":[]},
					"sources": ["nonexistent"]
				}
			},
			"pipeline_map": {"s": {"pipeline": ["merge_op"]}}
		},
		"pipeline_group": {"main": {"pipeline": ["s"]}},
		"flow_contract": {}
	}`)
	_, err := Load(data)
	if err == nil {
		t.Error("expected error for invalid sources reference")
	}
}

func TestExpandOperatorSequence(t *testing.T) {
	cfg, err := Load(mustReadTestdata(t, "minimal_valid.json"))
	if err != nil {
		t.Fatal(err)
	}

	seq, err := ExpandOperatorSequence(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(seq) != 2 {
		t.Fatalf("sequence length = %d", len(seq))
	}
	if seq[0] != "op_a_A1B2C3" || seq[1] != "op_b_D4E5F6" {
		t.Errorf("sequence = %v", seq)
	}
}

func TestExpandOperatorSequenceRecallMerge(t *testing.T) {
	cfg, err := Load(mustReadTestdata(t, "recall_merge.json"))
	if err != nil {
		t.Fatal(err)
	}

	seq, err := ExpandOperatorSequence(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(seq) != 3 {
		t.Fatalf("sequence length = %d", len(seq))
	}
	if seq[0] != "recall_idx_A1" || seq[1] != "recall_rt_B2" || seq[2] != "merge_C3" {
		t.Errorf("sequence = %v", seq)
	}
}

func TestExpandUndefinedSubFlow(t *testing.T) {
	cfg := &RootConfig{
		PipelineGroup: map[string]SubFlowRef{
			"main": {Pipeline: []string{"nonexistent_flow"}},
		},
		PipelineConfig: PipelineConfig{
			Operators:   map[string]OperatorConfig{"op": {TypeName: "t"}},
			PipelineMap: map[string]SubFlowRef{},
		},
	}
	_, err := ExpandOperatorSequence(cfg)
	if err == nil {
		t.Error("expected error for undefined sub-flow")
	}
}

func TestExpandUndefinedOperator(t *testing.T) {
	cfg := &RootConfig{
		PipelineGroup: map[string]SubFlowRef{
			"main": {Pipeline: []string{"s1"}},
		},
		PipelineConfig: PipelineConfig{
			Operators:   map[string]OperatorConfig{},
			PipelineMap: map[string]SubFlowRef{"s1": {Pipeline: []string{"ghost_op"}}},
		},
	}
	_, err := ExpandOperatorSequence(cfg)
	if err == nil {
		t.Error("expected error for undefined operator in sub-flow")
	}
}

func TestExpandNoMainMultipleGroups(t *testing.T) {
	cfg := &RootConfig{
		PipelineGroup: map[string]SubFlowRef{
			"a": {Pipeline: []string{}},
			"b": {Pipeline: []string{}},
		},
		PipelineConfig: PipelineConfig{
			Operators:   map[string]OperatorConfig{"op": {TypeName: "t"}},
			PipelineMap: map[string]SubFlowRef{},
		},
	}
	_, err := ExpandOperatorSequence(cfg)
	if err == nil {
		t.Error("expected error when no 'main' and multiple groups")
	}
}

func FuzzLoad(f *testing.F) {
	seed, err := os.ReadFile(testdataPath("minimal_valid.json"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"pipeline_config":{}}`))

	f.Fuzz(func(_ *testing.T, data []byte) {
		Load(data) //nolint:errcheck
	})
}
