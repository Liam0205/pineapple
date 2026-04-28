package dag

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Liam0205/pineapple/internal/config"
	"github.com/Liam0205/pineapple/internal/types"
)

func buildTestGraph(t *testing.T) *Graph {
	t.Helper()

	sequence := []string{"recall_a", "recall_b", "transform_c", "filter_d"}
	operators := map[string]config.OperatorConfig{
		"recall_a": {
			TypeName:     "recall_static",
			OperatorType: string(types.OpTypeRecall),
			Recall:       true,
			Meta:         config.Metadata{ItemOutput: []string{"id", "score"}},
		},
		"recall_b": {
			TypeName:     "recall_static",
			OperatorType: string(types.OpTypeRecall),
			Recall:       true,
			Meta:         config.Metadata{ItemOutput: []string{"id", "tag"}},
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
			TypeName:     "filter_condition",
			OperatorType: string(types.OpTypeFilter),
			Meta:         config.Metadata{ItemInput: []string{"score_norm"}},
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
	// Build a graph where barrier would create redundant transitive edges.
	// recall_a(writes X) → filter_b(barrier) → transform_c(reads X)
	// Without reduction: recall_a→filter_b, recall_a→transform_c, filter_b→transform_c
	// Build() now applies transitive reduction, so the graph should only have:
	//   recall_a→filter_b, filter_b→transform_c
	sequence := []string{"recall_a", "filter_b", "transform_c"}
	operators := map[string]config.OperatorConfig{
		"recall_a": {
			TypeName:     "recall_static",
			OperatorType: string(types.OpTypeRecall),
			Recall:       true,
			Meta:         config.Metadata{ItemOutput: []string{"x"}},
		},
		"filter_b": {
			TypeName:     "filter_condition",
			OperatorType: string(types.OpTypeFilter),
			Meta:         config.Metadata{ItemInput: []string{"x"}},
		},
		"transform_c": {
			TypeName:     "noop",
			OperatorType: string(types.OpTypeTransform),
			Meta:         config.Metadata{ItemInput: []string{"x"}},
		},
	}

	g, err := Build(sequence, operators, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// After reduction, recall_a→transform_c should NOT exist as a direct edge
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
			TypeName:     "recall_static",
			OperatorType: string(types.OpTypeRecall),
			Recall:       true,
			Meta:         config.Metadata{ItemOutput: []string{"id", "score"}},
		},
		"recall_b": {
			TypeName:     "recall_static",
			OperatorType: string(types.OpTypeRecall),
			Recall:       true,
			Meta:         config.Metadata{ItemOutput: []string{"id", "tag"}},
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
			TypeName:     "filter_condition",
			OperatorType: string(types.OpTypeFilter),
			Meta:         config.Metadata{ItemInput: []string{"score_norm"}},
		},
		"reorder_e": {
			TypeName:     "reorder_sort",
			OperatorType: string(types.OpTypeReorder),
			Meta:         config.Metadata{ItemInput: []string{"score_norm"}},
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
	dot := RenderCollapsedDOT(g)

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
	mmd := RenderCollapsedMermaid(g)

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
			TypeName:     "recall_static",
			OperatorType: string(types.OpTypeRecall),
			Recall:       true,
			Meta:         config.Metadata{ItemOutput: []string{"x"}},
		},
		"grouped_b": {
			TypeName:     "filter_condition",
			OperatorType: string(types.OpTypeFilter),
			Meta:         config.Metadata{ItemInput: []string{"x"}},
		},
		"grouped_c": {
			TypeName:     "reorder_sort",
			OperatorType: string(types.OpTypeReorder),
			Meta:         config.Metadata{ItemInput: []string{"x"}},
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

	dot := RenderCollapsedDOT(g)

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
