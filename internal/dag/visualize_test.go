package dag

import (
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

	g, err := Build(sequence, operators)
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

	// Type labels present
	for _, label := range []string{"[Recall]", "[Transform]", "[Filter]"} {
		if !strings.Contains(dot, label) {
			t.Errorf("missing type label %q", label)
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
	if !strings.HasPrefix(mmd, "graph LR\n") {
		t.Error("missing graph LR header")
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
