package dag

import (
	"testing"

	"github.com/Liam0205/pineapple/internal/config"
)

func op(commonIn, commonOut, itemIn, itemOut []string) config.OperatorConfig {
	return config.OperatorConfig{
		TypeName: "test",
		Meta: config.Metadata{
			CommonInput:  commonIn,
			CommonOutput: commonOut,
			ItemInput:    itemIn,
			ItemOutput:   itemOut,
		},
	}
}

func recallOp(commonIn []string, itemOut []string) config.OperatorConfig {
	c := op(commonIn, nil, nil, itemOut)
	c.Recall = true
	return c
}

func mergeOp(sources []string, itemOut []string) config.OperatorConfig {
	c := op(nil, nil, nil, itemOut)
	c.Sources = sources
	return c
}

func hasPred(g *Graph, name string, pred string) bool {
	idx := g.NameToIndex[name]
	predIdx := g.NameToIndex[pred]
	for _, p := range g.Nodes[idx].Preds {
		if p == predIdx {
			return true
		}
	}
	return false
}

func hasNoPreds(g *Graph, name string) bool {
	return len(g.Nodes[g.NameToIndex[name]].Preds) == 0
}

// --- RAW ---

func TestRAWDependency(t *testing.T) {
	// op_a writes common_foo, op_b reads common_foo -> RAW edge a->b
	seq := []string{"op_a", "op_b"}
	ops := map[string]config.OperatorConfig{
		"op_a": op(nil, []string{"common_foo"}, nil, nil),
		"op_b": op([]string{"common_foo"}, nil, nil, nil),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPred(g, "op_b", "op_a") {
		t.Error("expected RAW edge op_a -> op_b")
	}
}

func TestRAWItemDependency(t *testing.T) {
	seq := []string{"op_a", "op_b"}
	ops := map[string]config.OperatorConfig{
		"op_a": op(nil, nil, nil, []string{"item_score"}),
		"op_b": op(nil, nil, []string{"item_score"}, nil),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPred(g, "op_b", "op_a") {
		t.Error("expected RAW edge for item_score")
	}
}

// --- WAW ---

func TestWAWDependency(t *testing.T) {
	// Both write common_foo -> WAW edge a->b (DSL order)
	seq := []string{"op_a", "op_b"}
	ops := map[string]config.OperatorConfig{
		"op_a": op(nil, []string{"common_foo"}, nil, nil),
		"op_b": op(nil, []string{"common_foo"}, nil, nil),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPred(g, "op_b", "op_a") {
		t.Error("expected WAW edge op_a -> op_b")
	}
}

// --- WAR ---

func TestWARDependency(t *testing.T) {
	// op_a reads foo, op_b writes foo -> WAR: b waits for a
	seq := []string{"op_a", "op_b"}
	ops := map[string]config.OperatorConfig{
		"op_a": op([]string{"foo"}, nil, nil, nil),
		"op_b": op(nil, []string{"foo"}, nil, nil),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPred(g, "op_b", "op_a") {
		t.Error("expected WAR edge op_a -> op_b")
	}
}

// --- Parallel independence ---

func TestParallelIndependentOps(t *testing.T) {
	// op_a writes foo, op_b writes bar -> no dependency
	seq := []string{"op_a", "op_b"}
	ops := map[string]config.OperatorConfig{
		"op_a": op(nil, []string{"foo"}, nil, nil),
		"op_b": op(nil, []string{"bar"}, nil, nil),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	if !hasNoPreds(g, "op_a") {
		t.Error("op_a should have no preds")
	}
	if !hasNoPreds(g, "op_b") {
		t.Error("op_b should have no preds")
	}
}

// --- Recall parallelism ---

func TestRecallOpsParallel(t *testing.T) {
	// Two recall ops output same item fields -> should NOT depend on each other
	seq := []string{"recall_a", "recall_b"}
	ops := map[string]config.OperatorConfig{
		"recall_a": recallOp([]string{"user_id"}, []string{"item_id", "item_score"}),
		"recall_b": recallOp([]string{"user_id"}, []string{"item_id", "item_score"}),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	// They should be independent (no WAW from item_output since recall is excluded)
	if !hasNoPreds(g, "recall_a") {
		t.Error("recall_a should have no preds")
	}
	if !hasNoPreds(g, "recall_b") {
		t.Error("recall_b should have no preds")
	}
}

func TestRecallCommonInputStillTracked(t *testing.T) {
	// op_a writes user_embedding, recall reads user_embedding
	seq := []string{"op_a", "recall_a"}
	ops := map[string]config.OperatorConfig{
		"op_a":    op(nil, []string{"user_embedding"}, nil, nil),
		"recall_a": recallOp([]string{"user_embedding"}, []string{"item_id"}),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPred(g, "recall_a", "op_a") {
		t.Error("expected RAW edge from op_a to recall_a via common field")
	}
}

// --- Merge sources edges ---

func TestMergeSourcesEdges(t *testing.T) {
	seq := []string{"recall_a", "recall_b", "merge"}
	ops := map[string]config.OperatorConfig{
		"recall_a": recallOp(nil, []string{"item_id"}),
		"recall_b": recallOp(nil, []string{"item_id"}),
		"merge":    mergeOp([]string{"recall_a", "recall_b"}, []string{"item_id"}),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPred(g, "merge", "recall_a") {
		t.Error("expected sources edge recall_a -> merge")
	}
	if !hasPred(g, "merge", "recall_b") {
		t.Error("expected sources edge recall_b -> merge")
	}
}

// --- Read-modify-write chain ---

func TestReadModifyWriteChain(t *testing.T) {
	// op_a writes score, op_b reads+writes score, op_c reads score
	// Expected: a->b (RAW+WAW), b->c (RAW)
	seq := []string{"op_a", "op_b", "op_c"}
	ops := map[string]config.OperatorConfig{
		"op_a": op(nil, nil, nil, []string{"score"}),
		"op_b": op(nil, nil, []string{"score"}, []string{"score"}),
		"op_c": op(nil, nil, []string{"score"}, nil),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPred(g, "op_b", "op_a") {
		t.Error("expected edge a->b")
	}
	if !hasPred(g, "op_c", "op_b") {
		t.Error("expected edge b->c")
	}
	// op_c should not directly depend on op_a (op_b is the last writer)
	if hasPred(g, "op_c", "op_a") {
		t.Error("op_c should not directly depend on op_a (op_b is intermediary)")
	}
}

// --- Diamond dependency ---

func TestDiamondDependency(t *testing.T) {
	// op_a writes foo, op_b reads foo writes bar, op_c reads foo writes baz, op_d reads bar+baz
	seq := []string{"op_a", "op_b", "op_c", "op_d"}
	ops := map[string]config.OperatorConfig{
		"op_a": op(nil, []string{"foo"}, nil, nil),
		"op_b": op([]string{"foo"}, []string{"bar"}, nil, nil),
		"op_c": op([]string{"foo"}, []string{"baz"}, nil, nil),
		"op_d": op([]string{"bar", "baz"}, nil, nil, nil),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	// a -> b, a -> c (RAW on foo)
	if !hasPred(g, "op_b", "op_a") {
		t.Error("expected a->b")
	}
	if !hasPred(g, "op_c", "op_a") {
		t.Error("expected a->c")
	}
	// b -> d (RAW on bar), c -> d (RAW on baz)
	if !hasPred(g, "op_d", "op_b") {
		t.Error("expected b->d")
	}
	if !hasPred(g, "op_d", "op_c") {
		t.Error("expected c->d")
	}
	// b and c should be independent
	if hasPred(g, "op_c", "op_b") || hasPred(g, "op_b", "op_c") {
		t.Error("b and c should be independent")
	}
}

// --- Topological sort ---

func TestTopologicalSortLinear(t *testing.T) {
	seq := []string{"a", "b", "c"}
	ops := map[string]config.OperatorConfig{
		"a": op(nil, []string{"x"}, nil, nil),
		"b": op([]string{"x"}, []string{"y"}, nil, nil),
		"c": op([]string{"y"}, nil, nil, nil),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	order, err := TopologicalSort(g)
	if err != nil {
		t.Fatal(err)
	}
	// Must be [0, 1, 2] since it's a strict chain
	for i, idx := range order {
		if idx != i {
			t.Errorf("order[%d] = %d, want %d", i, idx, i)
		}
	}
}

// --- Invalid sources reference ---

func TestBuildInvalidSourcesRef(t *testing.T) {
	seq := []string{"merge"}
	ops := map[string]config.OperatorConfig{
		"merge": mergeOp([]string{"ghost"}, nil),
	}
	_, err := Build(seq, ops)
	if err == nil {
		t.Error("expected error for invalid sources reference")
	}
}

// --- Self-read-write does not create self-edge ---

func TestSelfReadWriteNoSelfEdge(t *testing.T) {
	seq := []string{"op_a"}
	ops := map[string]config.OperatorConfig{
		"op_a": op([]string{"foo"}, []string{"foo"}, nil, nil),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes[0].Preds) != 0 || len(g.Nodes[0].Succs) != 0 {
		t.Error("single op with self read-write should have no edges")
	}
}
