package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Liam0205/pineapple/pine-go/internal/registry"
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
	expected := []string{"_if_1"}
	if len(branch.Skip) != len(expected) || branch.Skip[0] != expected[0] {
		t.Errorf("branch skip = %v, want %v", branch.Skip, expected)
	}
}

func TestLoadSkipBackwardCompat(t *testing.T) {
	// Old format: skip is a string, not an array
	data := []byte(`{
		"pipeline_config": {
			"operators": {
				"ctrl": {
					"type_name": "transform_by_lua",
					"$metadata": {"common_input": [], "common_output": ["_if_1"], "item_input": [], "item_output": []},
					"for_branch_control": true
				},
				"op": {
					"type_name": "noop",
					"$metadata": {"common_input": ["_if_1"], "common_output": [], "item_input": [], "item_output": []},
					"skip": "_if_1"
				}
			},
			"pipeline_map": {}
		},
		"pipeline_group": {"main": {"pipeline": ["ctrl", "op"]}},
		"flow_contract": {"common_input": [], "item_input": [], "common_output": [], "item_output": []}
	}`)
	cfg, err := Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	op := cfg.PipelineConfig.Operators["op"]
	if len(op.Skip) != 1 || op.Skip[0] != "_if_1" {
		t.Errorf("skip = %v, want [_if_1]", op.Skip)
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

func TestExpandOperatorSequenceWithSubFlows(t *testing.T) {
	cfg, err := Load(mustReadTestdata(t, "e2e_full_pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}

	seq, mapping, err := ExpandOperatorSequenceWithSubFlows(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if len(seq) == 0 {
		t.Fatal("sequence should not be empty")
	}

	// Every operator in the sequence must have a SubFlow mapping
	for _, opName := range seq {
		sf, ok := mapping[opName]
		if !ok {
			t.Errorf("operator %q missing from opToSubFlow map", opName)
		}
		if sf == "" {
			t.Errorf("operator %q has empty SubFlow name", opName)
		}
	}

	// e2e_full_pipeline.json has recall_stage and process_stage
	subFlows := make(map[string]bool)
	for _, sf := range mapping {
		subFlows[sf] = true
	}
	if !subFlows["recall_stage"] {
		t.Error("expected recall_stage in subflow mapping")
	}
	if !subFlows["process_stage"] {
		t.Error("expected process_stage in subflow mapping")
	}
}

func TestExpandWithSubFlowsMatchesOriginal(t *testing.T) {
	cfg, err := Load(mustReadTestdata(t, "minimal_valid.json"))
	if err != nil {
		t.Fatal(err)
	}

	seqOld, err := ExpandOperatorSequence(cfg)
	if err != nil {
		t.Fatal(err)
	}

	seqNew, _, err := ExpandOperatorSequenceWithSubFlows(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if len(seqOld) != len(seqNew) {
		t.Fatalf("lengths differ: %d vs %d", len(seqOld), len(seqNew))
	}
	for i := range seqOld {
		if seqOld[i] != seqNew[i] {
			t.Errorf("index %d: %q vs %q", i, seqOld[i], seqNew[i])
		}
	}
}

func TestExpandNestedSubFlow(t *testing.T) {
	cfg, err := Load(mustReadTestdata(t, "nested_subflow.json"))
	if err != nil {
		t.Fatal(err)
	}

	seq, mapping, err := ExpandOperatorSequenceWithSubFlows(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Expected flat order: recall_a, recall_b, merge_c, transform_d, filter_e
	expected := []string{"recall_a", "recall_b", "merge_c", "transform_d", "filter_e"}
	if len(seq) != len(expected) {
		t.Fatalf("sequence length = %d, want %d", len(seq), len(expected))
	}
	for i, name := range expected {
		if seq[i] != name {
			t.Errorf("seq[%d] = %q, want %q", i, seq[i], name)
		}
	}

	// Verify opToSubFlow paths
	wantMap := map[string]string{
		"recall_a":    "recall/candidates",
		"recall_b":    "recall/candidates",
		"merge_c":     "recall",
		"transform_d": "process",
		"filter_e":    "process",
	}
	for op, wantSF := range wantMap {
		if mapping[op] != wantSF {
			t.Errorf("opToSubFlow[%q] = %q, want %q", op, mapping[op], wantSF)
		}
	}
}

func TestExpandTopLevelMixedEntries(t *testing.T) {
	cfg := &RootConfig{
		PipelineGroup: map[string]SubFlowRef{
			"main": {Pipeline: []string{"op_a", "sub1", "op_b"}},
		},
		PipelineConfig: PipelineConfig{
			Operators: map[string]OperatorConfig{
				"op_a": {TypeName: "noop"},
				"op_b": {TypeName: "noop"},
				"op_c": {TypeName: "noop"},
			},
			PipelineMap: map[string]SubFlowRef{
				"sub1": {Pipeline: []string{"op_c"}},
			},
		},
	}

	seq, mapping, err := ExpandOperatorSequenceWithSubFlows(cfg)
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{"op_a", "op_c", "op_b"}
	if len(seq) != len(expected) {
		t.Fatalf("len = %d, want %d", len(seq), len(expected))
	}
	for i := range expected {
		if seq[i] != expected[i] {
			t.Errorf("seq[%d] = %q, want %q", i, seq[i], expected[i])
		}
	}

	if mapping["op_a"] != "" {
		t.Errorf("op_a should have empty SubFlow, got %q", mapping["op_a"])
	}
	if mapping["op_c"] != "sub1" {
		t.Errorf("op_c SubFlow = %q, want %q", mapping["op_c"], "sub1")
	}
	if mapping["op_b"] != "" {
		t.Errorf("op_b should have empty SubFlow, got %q", mapping["op_b"])
	}
}

func TestExpandCycleDetection(t *testing.T) {
	cfg := &RootConfig{
		PipelineGroup: map[string]SubFlowRef{
			"main": {Pipeline: []string{"a"}},
		},
		PipelineConfig: PipelineConfig{
			Operators: map[string]OperatorConfig{},
			PipelineMap: map[string]SubFlowRef{
				"a": {Pipeline: []string{"b"}},
				"b": {Pipeline: []string{"a"}},
			},
		},
	}

	_, _, err := ExpandOperatorSequenceWithSubFlows(cfg)
	if err == nil {
		t.Error("expected error for cycle")
	}
}

func TestExpandUndefinedEntry(t *testing.T) {
	cfg := &RootConfig{
		PipelineGroup: map[string]SubFlowRef{
			"main": {Pipeline: []string{"ghost"}},
		},
		PipelineConfig: PipelineConfig{
			Operators:   map[string]OperatorConfig{},
			PipelineMap: map[string]SubFlowRef{},
		},
	}

	_, _, err := ExpandOperatorSequenceWithSubFlows(cfg)
	if err == nil {
		t.Error("expected error for undefined entry")
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
	for _, name := range []string{
		"e2e_apple_dsl.json",
		"e2e_full_pipeline.json",
		"e2e_lua_pipeline.json",
		"e2e_recall_resource.json",
		"e2e_resource_lookup.json",
		"e2e_resource_pipeline.json",
		"nested_subflow.json",
		"recall_merge.json",
		"skip_branch.json",
	} {
		data, err := os.ReadFile(testdataPath(name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64*1024 {
			t.Skip("config fuzz input exceeds CI budget")
		}
		cfg, err := Load(data)
		if err != nil {
			return
		}
		for opName, op := range cfg.PipelineConfig.Operators {
			for key := range op.RawParams {
				if registry.IsReservedKey(key) {
					t.Fatalf("operator %q RawParams contains reserved key %q", opName, key)
				}
			}
		}
		if sequence, err := ExpandOperatorSequence(cfg); err == nil {
			for _, opName := range sequence {
				if _, ok := cfg.PipelineConfig.Operators[opName]; !ok {
					t.Fatalf("expanded sequence references missing operator %q", opName)
				}
			}
		}
	})
}

func TestValidateSkipNotInCommonInput(t *testing.T) {
	data := []byte(`{
		"pipeline_config": {
			"operators": {
				"ctrl": {
					"type_name": "transform_by_lua",
					"$metadata": {"common_input": [], "common_output": ["_if_1"], "item_input": [], "item_output": []},
					"for_branch_control": true
				},
				"op": {
					"type_name": "noop",
					"$metadata": {"common_input": [], "common_output": [], "item_input": [], "item_output": []},
					"skip": ["_if_1"]
				}
			},
			"pipeline_map": {}
		},
		"pipeline_group": {"main": {"pipeline": ["ctrl", "op"]}},
		"flow_contract": {"common_input": [], "item_input": [], "common_output": [], "item_output": []}
	}`)
	_, err := Load(data)
	if err == nil {
		t.Error("expected error for skip field not in common_input")
	}
}

func TestValidateSkipFieldNoUnderscorePrefix(t *testing.T) {
	data := []byte(`{
		"pipeline_config": {
			"operators": {
				"op": {
					"type_name": "noop",
					"$metadata": {"common_input": ["bad_field"], "common_output": [], "item_input": [], "item_output": []},
					"skip": ["bad_field"]
				}
			},
			"pipeline_map": {}
		},
		"pipeline_group": {"main": {"pipeline": ["op"]}},
		"flow_contract": {"common_input": [], "item_input": [], "common_output": [], "item_output": []}
	}`)
	_, err := Load(data)
	if err == nil {
		t.Error("expected error for skip field without _ prefix")
	}
}

func TestValidateSkipFieldValid(t *testing.T) {
	data := []byte(`{
		"pipeline_config": {
			"operators": {
				"ctrl": {
					"type_name": "transform_by_lua",
					"$metadata": {"common_input": [], "common_output": ["_if_1"], "item_input": [], "item_output": []},
					"for_branch_control": true
				},
				"op": {
					"type_name": "noop",
					"$metadata": {"common_input": ["_if_1"], "common_output": [], "item_input": [], "item_output": []},
					"skip": ["_if_1"]
				}
			},
			"pipeline_map": {}
		},
		"pipeline_group": {"main": {"pipeline": ["ctrl", "op"]}},
		"flow_contract": {"common_input": [], "item_input": [], "common_output": [], "item_output": []}
	}`)
	_, err := Load(data)
	if err != nil {
		t.Errorf("expected valid config to pass, got: %v", err)
	}
}

func TestLoadRejectsOperatorNameCollidingWithSubFlowPath(t *testing.T) {
	// "stage" appears both as an operator name and a pipeline_map key.
	// Without validation, Go silently treats it as an operator and drops
	// the SubFlow contents.
	data := []byte(`{
		"_PINEAPPLE_VERSION": "0.1.0",
		"pipeline_config": {
			"operators": {
				"stage": {
					"type_name": "noop",
					"$metadata": {"common_input": [], "common_output": ["x"], "item_input": [], "item_output": []}
				},
				"inner_op": {
					"type_name": "noop",
					"$metadata": {"common_input": ["x"], "common_output": ["y"], "item_input": [], "item_output": []}
				}
			},
			"pipeline_map": {
				"stage": {"pipeline": ["inner_op"]}
			}
		},
		"pipeline_group": {"main": {"pipeline": ["stage"]}},
		"flow_contract": {"common_input": [], "item_input": [], "common_output": [], "item_output": []}
	}`)
	_, err := Load(data)
	if err == nil {
		t.Fatal("expected error when operator name collides with SubFlow path, but got nil")
	}
	if !strings.Contains(err.Error(), "exists in both") {
		t.Errorf("error = %q, want message about name collision", err.Error())
	}
}
