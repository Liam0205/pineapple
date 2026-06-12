package dag

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Liam0205/pineapple/pine-go/internal/config"
	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

func buildTestGraph(t *testing.T) *Graph {
	t.Helper()

	sequence := []string{"recall_a", "recall_b", "transform_c", "filter_d"}
	operators := map[string]config.OperatorConfig{
		"recall_a": {
			TypeName:             "recall_static",
			OperatorType:         string(types.OpTypeRecall),
			Recall:               true,
			AdditiveWritesRowSet: true,
			Meta:                 config.Metadata{ItemOutput: []string{"id", "score"}},
		},
		"recall_b": {
			TypeName:             "recall_static",
			OperatorType:         string(types.OpTypeRecall),
			Recall:               true,
			AdditiveWritesRowSet: true,
			Meta:                 config.Metadata{ItemOutput: []string{"id", "tag"}},
		},
		"transform_c": {
			TypeName:     "transform_copy",
			OperatorType: string(types.OpTypeTransform),
			Meta: config.Metadata{
				ItemInput:  []string{"score"},
				ItemOutput: []string{"score_norm"},
			},
		},
		"filter_d": {
			TypeName:       "filter_condition",
			OperatorType:   string(types.OpTypeFilter),
			ConsumesRowSet: true,
			MutatesRowSet:  true,
			Meta:           config.Metadata{ItemInput: []string{"score_norm"}},
		},
	}

	g, err := Build(sequence, operators, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

func TestRenderDOT(t *testing.T) {
	g := buildTestGraph(t)
	dot := RenderDOT(g)

	// Must be valid DOT structure
	if !strings.Contains(dot, "digraph pipeline {") {
		t.Error("missing digraph header")
	}

	// All nodes present with type labels
	for _, name := range []string{"recall_a", "recall_b", "transform_c", "filter_d"} {
		if !strings.Contains(dot, name) {
			t.Errorf("missing node %q", name)
		}
	}

	// Edges: recall_a -> transform_c and recall_b -> transform_c (via item field RAW)
	if !strings.Contains(dot, `"recall_a" -> "transform_c"`) {
		t.Error("missing edge recall_a -> transform_c")
	}

	// filter_d is a barrier: transform_c -> filter_d
	if !strings.Contains(dot, `"transform_c" -> "filter_d"`) {
		t.Error("missing edge transform_c -> filter_d")
	}

	t.Logf("DOT output:\n%s", dot)
}

func TestRenderMermaid(t *testing.T) {
	g := buildTestGraph(t)
	mmd := RenderMermaid(g)

	// Must start with graph LR
	if !strings.HasPrefix(mmd, "graph TB\n") {
		t.Error("missing graph TB header")
	}

	// All nodes present
	for _, name := range []string{"recall_a", "recall_b", "transform_c", "filter_d"} {
		if !strings.Contains(mmd, name) {
			t.Errorf("missing node %q", name)
		}
	}

	// Class definitions present
	for _, cls := range []string{"classDef recall", "classDef transform", "classDef filter"} {
		if !strings.Contains(mmd, cls) {
			t.Errorf("missing %q", cls)
		}
	}

	// Edges present
	if !strings.Contains(mmd, "recall_a --> transform_c") {
		t.Error("missing edge recall_a --> transform_c")
	}

	t.Logf("Mermaid output:\n%s", mmd)
}

func TestTransitiveReduction(t *testing.T) {
	// Build a graph where row-set semantics create redundant transitive edges.
	// recall_a(writes X, additive _row_set_) → filter_b(ConsumesRowSet+MutatesRowSet, reads X) → transform_c(reads X)
	// Without reduction: recall_a→filter_b (RAW on X + _row_set_), recall_a→transform_c (RAW on X), filter_b→transform_c (WAW on X if filter writes X, or WAR)
	// Actually: recall_a writes X additively. filter_b reads X (RAW from recall_a) and consumes _row_set_ (RAW from recall_a additive writer).
	// filter_b also MutatesRowSet, so it becomes lastMutWriter of _row_set_.
	// transform_c reads X: RAW from recall_a (recall_a is still additive writer of X, not cleared because filter_b doesn't write X).
	// So edges: recall_a→filter_b, recall_a→transform_c. No filter_b→transform_c unless transform_c also ConsumesRowSet.
	// For this test to demonstrate transitive reduction, let's add ConsumesRowSet to transform_c.
	sequence := []string{"recall_a", "filter_b", "transform_c"}
	operators := map[string]config.OperatorConfig{
		"recall_a": {
			TypeName:             "recall_static",
			OperatorType:         string(types.OpTypeRecall),
			Recall:               true,
			AdditiveWritesRowSet: true,
			Meta:                 config.Metadata{ItemOutput: []string{"x"}},
		},
		"filter_b": {
			TypeName:       "filter_condition",
			OperatorType:   string(types.OpTypeFilter),
			ConsumesRowSet: true,
			MutatesRowSet:  true,
			Meta:           config.Metadata{ItemInput: []string{"x"}},
		},
		"transform_c": {
			TypeName:       "noop",
			OperatorType:   string(types.OpTypeTransform),
			ConsumesRowSet: true,
			Meta:           config.Metadata{ItemInput: []string{"x"}},
		},
	}

	g, err := Build(sequence, operators, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// After reduction, recall_a→transform_c should NOT exist as a direct edge
	// because it's implied by recall_a→filter_b→transform_c
	for _, s := range g.Nodes[0].Succs {
		if s == 2 {
			t.Error("transitive reduction should remove recall_a→transform_c (implied by recall_a→filter_b→transform_c)")
		}
	}

	// Should have exactly 2 edges: recall_a→filter_b, filter_b→transform_c
	edgeCount := 0
	for _, node := range g.Nodes {
		edgeCount += len(node.Succs)
	}
	if edgeCount != 2 {
		t.Errorf("expected 2 edges, got %d", edgeCount)
	}

	// Verify the DOT output reflects the reduced graph
	dot := RenderDOT(g)
	if strings.Contains(dot, `"recall_a" -> "transform_c"`) {
		t.Error("DOT output should not contain redundant edge recall_a→transform_c")
	}
	if !strings.Contains(dot, `"recall_a" -> "filter_b"`) {
		t.Error("DOT output should contain edge recall_a→filter_b")
	}
	if !strings.Contains(dot, `"filter_b" -> "transform_c"`) {
		t.Error("DOT output should contain edge filter_b→transform_c")
	}

	t.Logf("Edges from recall_a: %v", g.Nodes[0].Succs)
	t.Logf("DOT output:\n%s", dot)
}

func buildTwoSubFlowGraph(t *testing.T) *Graph {
	t.Helper()

	sequence := []string{"recall_a", "recall_b", "transform_c", "filter_d", "reorder_e"}
	operators := map[string]config.OperatorConfig{
		"recall_a": {
			TypeName:             "recall_static",
			OperatorType:         string(types.OpTypeRecall),
			Recall:               true,
			AdditiveWritesRowSet: true,
			Meta:                 config.Metadata{ItemOutput: []string{"id", "score"}},
		},
		"recall_b": {
			TypeName:             "recall_static",
			OperatorType:         string(types.OpTypeRecall),
			Recall:               true,
			AdditiveWritesRowSet: true,
			Meta:                 config.Metadata{ItemOutput: []string{"id", "tag"}},
		},
		"transform_c": {
			TypeName:     "transform_copy",
			OperatorType: string(types.OpTypeTransform),
			Meta: config.Metadata{
				ItemInput:  []string{"score"},
				ItemOutput: []string{"score_norm"},
			},
		},
		"filter_d": {
			TypeName:       "filter_condition",
			OperatorType:   string(types.OpTypeFilter),
			ConsumesRowSet: true,
			MutatesRowSet:  true,
			Meta:           config.Metadata{ItemInput: []string{"score_norm"}},
		},
		"reorder_e": {
			TypeName:       "reorder_sort",
			OperatorType:   string(types.OpTypeReorder),
			ConsumesRowSet: true,
			MutatesRowSet:  true,
			Meta:           config.Metadata{ItemInput: []string{"score_norm"}},
		},
	}
	opToSubFlow := map[string]string{
		"recall_a":    "recall_stage",
		"recall_b":    "recall_stage",
		"transform_c": "recall_stage",
		"filter_d":    "rank_stage",
		"reorder_e":   "rank_stage",
	}

	g, err := Build(sequence, operators, opToSubFlow)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

func TestRenderCollapsedDOT(t *testing.T) {
	g := buildTwoSubFlowGraph(t)
	dot := RenderCollapsedDOT(g, 1)

	if !strings.Contains(dot, "digraph pipeline {") {
		t.Error("missing digraph header")
	}

	// Aggregate nodes for the two SubFlows
	if !strings.Contains(dot, `"recall_stage"`) {
		t.Error("missing aggregate node recall_stage")
	}
	if !strings.Contains(dot, `"rank_stage"`) {
		t.Error("missing aggregate node rank_stage")
	}

	// Should have cross-SubFlow edge
	if !strings.Contains(dot, "->") {
		t.Error("missing cross-subflow edge")
	}

	// Individual operator names should NOT appear
	for _, name := range []string{"recall_a", "recall_b", "transform_c", "filter_d", "reorder_e"} {
		if strings.Contains(dot, fmt.Sprintf("label=%q", name)) {
			t.Errorf("collapsed DOT should not contain individual node %q", name)
		}
	}

	t.Logf("Collapsed DOT output:\n%s", dot)
}

func TestRenderCollapsedMermaid(t *testing.T) {
	g := buildTwoSubFlowGraph(t)
	mmd := RenderCollapsedMermaid(g, 1)

	if !strings.HasPrefix(mmd, "graph TB\n") {
		t.Error("missing graph TB header")
	}

	// Aggregate nodes
	if !strings.Contains(mmd, `"recall_stage"`) {
		t.Error("missing aggregate node recall_stage")
	}
	if !strings.Contains(mmd, `"rank_stage"`) {
		t.Error("missing aggregate node rank_stage")
	}

	// Class definitions
	if !strings.Contains(mmd, "classDef subflow") {
		t.Error("missing classDef subflow")
	}

	// Cross-SubFlow edge
	if !strings.Contains(mmd, "-->") {
		t.Error("missing cross-subflow edge")
	}

	t.Logf("Collapsed Mermaid output:\n%s", mmd)
}

func TestRenderCollapsedMixedSubFlows(t *testing.T) {
	// One node without SubFlow, two in a SubFlow
	sequence := []string{"standalone_a", "grouped_b", "grouped_c"}
	operators := map[string]config.OperatorConfig{
		"standalone_a": {
			TypeName:             "recall_static",
			OperatorType:         string(types.OpTypeRecall),
			Recall:               true,
			AdditiveWritesRowSet: true,
			Meta:                 config.Metadata{ItemOutput: []string{"x"}},
		},
		"grouped_b": {
			TypeName:       "filter_condition",
			OperatorType:   string(types.OpTypeFilter),
			ConsumesRowSet: true,
			MutatesRowSet:  true,
			Meta:           config.Metadata{ItemInput: []string{"x"}},
		},
		"grouped_c": {
			TypeName:       "reorder_sort",
			OperatorType:   string(types.OpTypeReorder),
			ConsumesRowSet: true,
			MutatesRowSet:  true,
			Meta:           config.Metadata{ItemInput: []string{"x"}},
		},
	}
	opToSubFlow := map[string]string{
		"standalone_a": "",
		"grouped_b":    "my_group",
		"grouped_c":    "my_group",
	}

	g, err := Build(sequence, operators, opToSubFlow)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	dot := RenderCollapsedDOT(g, 1)

	// standalone_a should appear as its own node
	if !strings.Contains(dot, `"standalone_a"`) {
		t.Error("standalone node should keep its name")
	}
	// my_group should appear as aggregate
	if !strings.Contains(dot, `"my_group"`) {
		t.Error("missing aggregate node my_group")
	}

	t.Logf("Mixed collapsed DOT:\n%s", dot)
}

func buildNestedSubFlowGraph(t *testing.T) *Graph {
	t.Helper()

	sequence := []string{"recall_a", "recall_b", "merge_c", "transform_d", "filter_e"}
	operators := map[string]config.OperatorConfig{
		"recall_a": {
			TypeName:             "recall_static",
			OperatorType:         string(types.OpTypeRecall),
			Recall:               true,
			AdditiveWritesRowSet: true,
			Meta:                 config.Metadata{ItemOutput: []string{"id", "score"}},
		},
		"recall_b": {
			TypeName:             "recall_static",
			OperatorType:         string(types.OpTypeRecall),
			Recall:               true,
			AdditiveWritesRowSet: true,
			Meta:                 config.Metadata{ItemOutput: []string{"id", "tag"}},
		},
		"merge_c": {
			TypeName:     "noop",
			OperatorType: string(types.OpTypeTransform),
			Meta:         config.Metadata{ItemInput: []string{"id"}, ItemOutput: []string{"id"}},
		},
		"transform_d": {
			TypeName:     "transform_copy",
			OperatorType: string(types.OpTypeTransform),
			Meta:         config.Metadata{ItemInput: []string{"score"}, ItemOutput: []string{"score_norm"}},
		},
		"filter_e": {
			TypeName:       "filter_condition",
			OperatorType:   string(types.OpTypeFilter),
			ConsumesRowSet: true,
			MutatesRowSet:  true,
			Meta:           config.Metadata{ItemInput: []string{"score_norm"}},
		},
	}
	opToSubFlow := map[string]string{
		"recall_a":    "recall/candidates",
		"recall_b":    "recall/candidates",
		"merge_c":     "recall",
		"transform_d": "process",
		"filter_e":    "process",
	}

	g, err := Build(sequence, operators, opToSubFlow)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

func TestRenderCollapsedLevel1(t *testing.T) {
	g := buildNestedSubFlowGraph(t)
	dot := RenderCollapsedDOT(g, 1)

	// Level 1: "recall/candidates" and "recall" both → "recall"; "process" stays
	if !strings.Contains(dot, `"recall"`) {
		t.Error("missing aggregate node 'recall' at level 1")
	}
	if !strings.Contains(dot, `"process"`) {
		t.Error("missing aggregate node 'process' at level 1")
	}

	t.Logf("Level 1 DOT:\n%s", dot)
}

func TestRenderCollapsedLevel2(t *testing.T) {
	g := buildNestedSubFlowGraph(t)
	dot := RenderCollapsedDOT(g, 2)

	// Level 2: "recall/candidates" is distinct from "recall"; "process" stays
	if !strings.Contains(dot, `"recall/candidates"`) {
		t.Error("missing aggregate node 'recall/candidates' at level 2")
	}
	if !strings.Contains(dot, `"recall"`) {
		t.Error("missing aggregate node 'recall' at level 2")
	}
	if !strings.Contains(dot, `"process"`) {
		t.Error("missing aggregate node 'process' at level 2")
	}

	t.Logf("Level 2 DOT:\n%s", dot)
}

func TestRenderCollapsedMermaidLevel1(t *testing.T) {
	g := buildNestedSubFlowGraph(t)
	mmd := RenderCollapsedMermaid(g, 1)

	if !strings.HasPrefix(mmd, "graph TB\n") {
		t.Error("missing graph TB header")
	}
	if !strings.Contains(mmd, `"recall"`) {
		t.Error("missing aggregate node 'recall'")
	}
	if !strings.Contains(mmd, `"process"`) {
		t.Error("missing aggregate node 'process'")
	}

	t.Logf("Level 1 Mermaid:\n%s", mmd)
}

func buildDeepNestedGraph(t *testing.T) *Graph {
	t.Helper()

	sequence := []string{
		"recall_top",
		"ctrl_if",
		"transform_l1",
		"ctrl_l1_if",
		"transform_l2",
		"ctrl_l1_l2_if",
		"transform_l3",
		"ctrl_l1_l2_l3_if",
		"transform_leaf",
		"ctrl_else",
		"transform_else",
	}
	operators := map[string]config.OperatorConfig{
		"recall_top": {
			TypeName:             "recall_static",
			OperatorType:         string(types.OpTypeRecall),
			Recall:               true,
			AdditiveWritesRowSet: true,
			Meta:                 config.Metadata{ItemOutput: []string{"item_id", "item_score"}},
		},
		"ctrl_if": {
			TypeName:     "transform_by_lua",
			OperatorType: string(types.OpTypeTransform),
			Meta: config.Metadata{
				CommonInput:  []string{"enabled"},
				CommonOutput: []string{"_if_1"},
			},
		},
		"transform_l1": {
			TypeName:     "transform_by_lua",
			OperatorType: string(types.OpTypeTransform),
			Meta: config.Metadata{
				CommonInput: []string{"_if_1"},
				ItemInput:   []string{"item_score"},
				ItemOutput:  []string{"item_score"},
			},
		},
		"ctrl_l1_if": {
			TypeName:     "transform_by_lua",
			OperatorType: string(types.OpTypeTransform),
			Meta: config.Metadata{
				CommonInput:  []string{"flag_l1", "_if_1"},
				CommonOutput: []string{"_L1::if_1"},
			},
		},
		"transform_l2": {
			TypeName:     "transform_by_lua",
			OperatorType: string(types.OpTypeTransform),
			Meta: config.Metadata{
				CommonInput: []string{"_if_1", "_L1::if_1"},
				ItemInput:   []string{"item_score"},
				ItemOutput:  []string{"item_score"},
			},
		},
		"ctrl_l1_l2_if": {
			TypeName:     "transform_by_lua",
			OperatorType: string(types.OpTypeTransform),
			Meta: config.Metadata{
				CommonInput:  []string{"flag_l2", "_if_1", "_L1::if_1"},
				CommonOutput: []string{"_L1::L2::if_1"},
			},
		},
		"transform_l3": {
			TypeName:     "transform_by_lua",
			OperatorType: string(types.OpTypeTransform),
			Meta: config.Metadata{
				CommonInput: []string{"_if_1", "_L1::if_1", "_L1::L2::if_1"},
				ItemInput:   []string{"item_score"},
				ItemOutput:  []string{"item_score"},
			},
		},
		"ctrl_l1_l2_l3_if": {
			TypeName:     "transform_by_lua",
			OperatorType: string(types.OpTypeTransform),
			Meta: config.Metadata{
				CommonInput:  []string{"flag_l3", "_if_1", "_L1::if_1", "_L1::L2::if_1"},
				CommonOutput: []string{"_L1::L2::L3::if_1"},
			},
		},
		"transform_leaf": {
			TypeName:     "transform_by_lua",
			OperatorType: string(types.OpTypeTransform),
			Meta: config.Metadata{
				CommonInput: []string{"_L1::L2::L3::if_1", "_if_1", "_L1::if_1", "_L1::L2::if_1"},
				ItemInput:   []string{"item_score"},
				ItemOutput:  []string{"item_score"},
			},
		},
		"ctrl_else": {
			TypeName:     "transform_by_lua",
			OperatorType: string(types.OpTypeTransform),
			Meta: config.Metadata{
				CommonInput:  []string{"enabled"},
				CommonOutput: []string{"_else_2"},
			},
		},
		"transform_else": {
			TypeName:     "transform_by_lua",
			OperatorType: string(types.OpTypeTransform),
			Meta: config.Metadata{
				CommonInput: []string{"_else_2"},
				ItemInput:   []string{"item_score"},
				ItemOutput:  []string{"item_score"},
			},
		},
	}
	opToSubFlow := map[string]string{
		"transform_l1":     "L1",
		"ctrl_l1_if":       "L1",
		"transform_l2":     "L1/L2",
		"ctrl_l1_l2_if":    "L1/L2",
		"transform_l3":     "L1/L2/L3",
		"ctrl_l1_l2_l3_if": "L1/L2/L3",
		"transform_leaf":   "L1/L2/L3",
	}

	g, err := Build(sequence, operators, opToSubFlow)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

func TestRenderCollapsedDeepLevel1(t *testing.T) {
	g := buildDeepNestedGraph(t)
	dot := RenderCollapsedDOT(g, 1)

	// Level 1: "L1/L2", "L1/L2/L3", "L1" all collapse into "L1"
	if !strings.Contains(dot, `"L1"`) {
		t.Error("missing aggregate node 'L1' at level 1")
	}
	// standalone nodes remain
	if !strings.Contains(dot, `"recall_top"`) {
		t.Error("missing standalone node recall_top")
	}
	// deeper paths should NOT appear as distinct nodes
	if strings.Contains(dot, `"L1/L2"`) {
		t.Error("L1/L2 should be collapsed into L1 at level 1")
	}
	if strings.Contains(dot, `"L1/L2/L3"`) {
		t.Error("L1/L2/L3 should be collapsed into L1 at level 1")
	}

	t.Logf("Deep Level 1 DOT:\n%s", dot)
}

func TestRenderCollapsedDeepLevel2(t *testing.T) {
	g := buildDeepNestedGraph(t)
	dot := RenderCollapsedDOT(g, 2)

	// Level 2: "L1" stays, "L1/L2" and "L1/L2/L3" collapse into "L1/L2"
	if !strings.Contains(dot, `"L1"`) {
		t.Error("missing aggregate node 'L1' at level 2")
	}
	if !strings.Contains(dot, `"L1/L2"`) {
		t.Error("missing aggregate node 'L1/L2' at level 2")
	}
	if strings.Contains(dot, `"L1/L2/L3"`) {
		t.Error("L1/L2/L3 should be collapsed into L1/L2 at level 2")
	}

	t.Logf("Deep Level 2 DOT:\n%s", dot)
}

func TestRenderCollapsedDeepLevel3(t *testing.T) {
	g := buildDeepNestedGraph(t)
	dot := RenderCollapsedDOT(g, 3)

	// Level 3: all paths fully expanded
	if !strings.Contains(dot, `"L1"`) {
		t.Error("missing aggregate node 'L1' at level 3")
	}
	if !strings.Contains(dot, `"L1/L2"`) {
		t.Error("missing aggregate node 'L1/L2' at level 3")
	}
	if !strings.Contains(dot, `"L1/L2/L3"`) {
		t.Error("missing aggregate node 'L1/L2/L3' at level 3")
	}

	t.Logf("Deep Level 3 DOT:\n%s", dot)
}
