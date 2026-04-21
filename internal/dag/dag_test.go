package dag

import (
	"testing"

	"github.com/Liam0205/pineapple/internal/config"
	"github.com/Liam0205/pineapple/internal/types"
)

// transformOp creates a Transform-type operator config for testing.
func transformOp(commonIn, commonOut, itemIn, itemOut []string) config.OperatorConfig {
	return config.OperatorConfig{
		TypeName:     "test",
		OperatorType: string(types.OpTypeTransform),
		Meta: config.Metadata{
			CommonInput:  commonIn,
			CommonOutput: commonOut,
			ItemInput:    itemIn,
			ItemOutput:   itemOut,
		},
	}
}

// recallOp creates a Recall-type operator config for testing.
func recallOp(commonIn []string, itemOut []string) config.OperatorConfig {
	return config.OperatorConfig{
		TypeName:     "test",
		OperatorType: string(types.OpTypeRecall),
		Recall:       true,
		Meta: config.Metadata{
			CommonInput: commonIn,
			ItemOutput:  itemOut,
		},
	}
}

// filterOp creates a Filter-type (barrier) operator config for testing.
func filterOp(itemIn, itemOut []string) config.OperatorConfig {
	return config.OperatorConfig{
		TypeName:     "test",
		OperatorType: string(types.OpTypeFilter),
		Meta: config.Metadata{
			ItemInput:  itemIn,
			ItemOutput: itemOut,
		},
	}
}

// mergeOp creates a Merge-type (barrier) operator config for testing.
func mergeOp(sources []string, itemOut []string) config.OperatorConfig {
	return config.OperatorConfig{
		TypeName:     "test",
		OperatorType: string(types.OpTypeMerge),
		Sources:      sources,
		Meta: config.Metadata{
			ItemOutput: itemOut,
		},
	}
}

// reorderOp creates a Reorder-type (barrier) operator config for testing.
func reorderOp(itemIn []string) config.OperatorConfig {
	return config.OperatorConfig{
		TypeName:     "test",
		OperatorType: string(types.OpTypeReorder),
		Meta: config.Metadata{
			ItemInput: itemIn,
		},
	}
}

// observeOp creates an Observe-type (read-only, non-blocking) operator config.
func observeOp(commonIn, itemIn []string) config.OperatorConfig {
	return config.OperatorConfig{
		TypeName:     "test",
		OperatorType: string(types.OpTypeObserve),
		Meta: config.Metadata{
			CommonInput: commonIn,
			ItemInput:   itemIn,
		},
	}
}

// rowDepOp creates a Transform-type operator with row_dependency=true for testing.
// It only declares common output (no item fields), relying on row-set dependency.
func rowDepOp(commonOut []string) config.OperatorConfig {
	return config.OperatorConfig{
		TypeName:      "test",
		OperatorType:  string(types.OpTypeTransform),
		RowDependency: true,
		Meta:          config.Metadata{CommonOutput: commonOut},
	}
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
		"op_a": transformOp(nil, []string{"common_foo"}, nil, nil),
		"op_b": transformOp([]string{"common_foo"}, nil, nil, nil),
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
		"op_a": transformOp(nil, nil, nil, []string{"item_score"}),
		"op_b": transformOp(nil, nil, []string{"item_score"}, nil),
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
		"op_a": transformOp(nil, []string{"common_foo"}, nil, nil),
		"op_b": transformOp(nil, []string{"common_foo"}, nil, nil),
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
		"op_a": transformOp([]string{"foo"}, nil, nil, nil),
		"op_b": transformOp(nil, []string{"foo"}, nil, nil),
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
		"op_a": transformOp(nil, []string{"foo"}, nil, nil),
		"op_b": transformOp(nil, []string{"bar"}, nil, nil),
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
	// (AddItem semantics: no WAW between recalls)
	seq := []string{"recall_a", "recall_b"}
	ops := map[string]config.OperatorConfig{
		"recall_a": recallOp([]string{"user_id"}, []string{"item_id", "item_score"}),
		"recall_b": recallOp([]string{"user_id"}, []string{"item_id", "item_score"}),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	if !hasNoPreds(g, "recall_a") {
		t.Error("recall_a should have no preds")
	}
	if !hasNoPreds(g, "recall_b") {
		t.Error("recall_b should have no preds")
	}
}

func TestRecallToDownstreamRAW(t *testing.T) {
	// recall writes item_price, downstream reads item_price -> RAW edge
	seq := []string{"recall_a", "op_b"}
	ops := map[string]config.OperatorConfig{
		"recall_a": recallOp(nil, []string{"item_id", "item_price"}),
		"op_b":     transformOp(nil, nil, []string{"item_price"}, []string{"item_score"}),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPred(g, "op_b", "recall_a") {
		t.Error("expected RAW edge recall_a -> op_b via item_price")
	}
}

func TestMultiRecallToDownstreamRAW(t *testing.T) {
	// Two recalls both write item_id, downstream reads item_id -> depends on BOTH
	seq := []string{"recall_a", "recall_b", "op_c"}
	ops := map[string]config.OperatorConfig{
		"recall_a": recallOp(nil, []string{"item_id", "item_score"}),
		"recall_b": recallOp(nil, []string{"item_id", "item_score"}),
		"op_c":     transformOp(nil, nil, []string{"item_id"}, nil),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	// op_c depends on both recalls
	if !hasPred(g, "op_c", "recall_a") {
		t.Error("expected RAW edge recall_a -> op_c")
	}
	if !hasPred(g, "op_c", "recall_b") {
		t.Error("expected RAW edge recall_b -> op_c")
	}
	// recalls remain independent of each other
	if hasPred(g, "recall_b", "recall_a") || hasPred(g, "recall_a", "recall_b") {
		t.Error("recalls should be independent of each other")
	}
}

func TestRecallThenMutatingWriter(t *testing.T) {
	// recall writes item_id, regular op also writes item_id -> WAW edge
	seq := []string{"recall_a", "op_b"}
	ops := map[string]config.OperatorConfig{
		"recall_a": recallOp(nil, []string{"item_id"}),
		"op_b":     transformOp(nil, nil, nil, []string{"item_id"}),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPred(g, "op_b", "recall_a") {
		t.Error("expected WAW edge recall_a -> op_b")
	}
}

func TestRecallCommonInputStillTracked(t *testing.T) {
	// op_a writes user_embedding, recall reads user_embedding
	seq := []string{"op_a", "recall_a"}
	ops := map[string]config.OperatorConfig{
		"op_a":    transformOp(nil, []string{"user_embedding"}, nil, nil),
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
		"op_a": transformOp(nil, nil, nil, []string{"score"}),
		"op_b": transformOp(nil, nil, []string{"score"}, []string{"score"}),
		"op_c": transformOp(nil, nil, []string{"score"}, nil),
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
		"op_a": transformOp(nil, []string{"foo"}, nil, nil),
		"op_b": transformOp([]string{"foo"}, []string{"bar"}, nil, nil),
		"op_c": transformOp([]string{"foo"}, []string{"baz"}, nil, nil),
		"op_d": transformOp([]string{"bar", "baz"}, nil, nil, nil),
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
		"a": transformOp(nil, []string{"x"}, nil, nil),
		"b": transformOp([]string{"x"}, []string{"y"}, nil, nil),
		"c": transformOp([]string{"y"}, nil, nil, nil),
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
		"op_a": transformOp([]string{"foo"}, []string{"foo"}, nil, nil),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes[0].Preds) != 0 || len(g.Nodes[0].Succs) != 0 {
		t.Error("single op with self read-write should have no edges")
	}
}

// --- Barrier semantics ---

func TestFilterBarrierSemantics(t *testing.T) {
	// transform_a and transform_b run independently,
	// filter is a barrier: both must finish before filter, everything after waits for filter
	seq := []string{"transform_a", "transform_b", "filter", "transform_c"}
	ops := map[string]config.OperatorConfig{
		"transform_a": transformOp(nil, nil, nil, []string{"score"}),
		"transform_b": transformOp(nil, nil, nil, []string{"rank"}),
		"filter":      filterOp([]string{"score"}, nil),
		"transform_c": transformOp(nil, nil, []string{"score"}, nil),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	// Both transforms must precede filter (barrier)
	if !hasPred(g, "filter", "transform_a") {
		t.Error("expected barrier edge transform_a -> filter")
	}
	if !hasPred(g, "filter", "transform_b") {
		t.Error("expected barrier edge transform_b -> filter")
	}
	// transform_c must wait for filter
	if !hasPred(g, "transform_c", "filter") {
		t.Error("expected barrier edge filter -> transform_c")
	}
}

func TestReorderBarrierSemantics(t *testing.T) {
	seq := []string{"transform_a", "reorder", "transform_b"}
	ops := map[string]config.OperatorConfig{
		"transform_a": transformOp(nil, nil, nil, []string{"score"}),
		"reorder":     reorderOp([]string{"score"}),
		"transform_b": transformOp(nil, nil, []string{"score"}, nil),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPred(g, "reorder", "transform_a") {
		t.Error("expected barrier edge transform_a -> reorder")
	}
	if !hasPred(g, "transform_b", "reorder") {
		t.Error("expected barrier edge reorder -> transform_b")
	}
}

func TestMergeBarrierSemantics(t *testing.T) {
	// merge is a barrier; all prior ops finish before it, all later ops wait
	seq := []string{"recall_a", "recall_b", "merge", "transform_c"}
	ops := map[string]config.OperatorConfig{
		"recall_a":    recallOp(nil, []string{"item_id"}),
		"recall_b":    recallOp(nil, []string{"item_id"}),
		"merge":       mergeOp([]string{"recall_a", "recall_b"}, []string{"item_id"}),
		"transform_c": transformOp(nil, nil, []string{"item_id"}, nil),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	// Barrier edges
	if !hasPred(g, "merge", "recall_a") {
		t.Error("expected edge recall_a -> merge")
	}
	if !hasPred(g, "merge", "recall_b") {
		t.Error("expected edge recall_b -> merge")
	}
	if !hasPred(g, "transform_c", "merge") {
		t.Error("expected edge merge -> transform_c")
	}
}

func TestMultipleBarriersChain(t *testing.T) {
	// Two barriers in sequence: filter then reorder
	seq := []string{"transform_a", "filter", "transform_b", "reorder", "transform_c"}
	ops := map[string]config.OperatorConfig{
		"transform_a": transformOp(nil, nil, nil, []string{"score"}),
		"filter":      filterOp([]string{"score"}, nil),
		"transform_b": transformOp(nil, nil, []string{"score"}, []string{"rank"}),
		"reorder":     reorderOp([]string{"rank"}),
		"transform_c": transformOp(nil, nil, []string{"rank"}, nil),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	// First barrier chain
	if !hasPred(g, "filter", "transform_a") {
		t.Error("expected transform_a -> filter")
	}
	if !hasPred(g, "transform_b", "filter") {
		t.Error("expected filter -> transform_b")
	}
	// Second barrier chain
	if !hasPred(g, "reorder", "transform_b") {
		t.Error("expected transform_b -> reorder")
	}
	if !hasPred(g, "transform_c", "reorder") {
		t.Error("expected reorder -> transform_c")
	}
}

// --- Observe semantics ---

func TestObserveNonBlocking(t *testing.T) {
	// Observe reads score but does not block downstream transform that writes score
	seq := []string{"transform_a", "observe", "transform_b"}
	ops := map[string]config.OperatorConfig{
		"transform_a": transformOp(nil, nil, nil, []string{"score"}),
		"observe":     observeOp(nil, []string{"score"}),
		"transform_b": transformOp(nil, nil, []string{"score"}, []string{"rank"}),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	// observe depends on transform_a (RAW on score)
	if !hasPred(g, "observe", "transform_a") {
		t.Error("expected RAW edge transform_a -> observe")
	}
	// transform_b depends on transform_a (RAW on score), NOT on observe
	if !hasPred(g, "transform_b", "transform_a") {
		t.Error("expected RAW edge transform_a -> transform_b")
	}
}

func TestObserveDoesNotCreateWAR(t *testing.T) {
	// Observe reads foo; a later transform writes foo. Because Observe is
	// read-only and non-blocking, the transform should NOT wait for Observe
	// to finish (no WAR edge from observe to transform_b).
	seq := []string{"transform_a", "observe", "transform_b"}
	ops := map[string]config.OperatorConfig{
		"transform_a": transformOp(nil, []string{"foo"}, nil, nil),
		"observe":     observeOp([]string{"foo"}, nil),
		"transform_b": transformOp(nil, []string{"foo"}, nil, nil),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	// transform_b should depend on transform_a (WAW on foo)
	if !hasPred(g, "transform_b", "transform_a") {
		t.Error("expected WAW edge transform_a -> transform_b")
	}
	// observe should depend on transform_a (RAW on foo)
	if !hasPred(g, "observe", "transform_a") {
		t.Error("expected RAW edge transform_a -> observe")
	}
	// transform_b should NOT depend on observe (Observe is non-blocking)
	if hasPred(g, "transform_b", "observe") {
		t.Error("transform_b should NOT depend on observe (non-blocking)")
	}
}

// --- Recall depending on Transform ---

func TestRecallDependsOnTransform_ParallelAfter(t *testing.T) {
	// transform writes user_vec, two recalls read it -> both depend on transform,
	// but remain independent of each other (additive item writes, no WAW/WAR)
	seq := []string{"transform", "recall_a", "recall_b"}
	ops := map[string]config.OperatorConfig{
		"transform": transformOp(nil, []string{"user_vec"}, nil, nil),
		"recall_a":  recallOp([]string{"user_vec"}, []string{"item_id", "item_score"}),
		"recall_b":  recallOp([]string{"user_vec"}, []string{"item_id", "item_score"}),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	// Both recalls depend on transform (RAW on user_vec)
	if !hasPred(g, "recall_a", "transform") {
		t.Error("expected RAW edge transform -> recall_a")
	}
	if !hasPred(g, "recall_b", "transform") {
		t.Error("expected RAW edge transform -> recall_b")
	}
	// Recalls remain independent (additive item writes)
	if hasPred(g, "recall_b", "recall_a") || hasPred(g, "recall_a", "recall_b") {
		t.Error("recalls should be independent despite same item outputs")
	}
}

func TestRecallDependsOnDifferentTransforms(t *testing.T) {
	// t_a writes feature_x, t_b reads feature_x writes feature_y.
	// recall_a reads feature_x, recall_b reads feature_y.
	// recall_a can start after t_a; recall_b must wait for t_b (which waits for t_a).
	// So recall_a could potentially run in parallel with t_b.
	seq := []string{"t_a", "t_b", "recall_a", "recall_b"}
	ops := map[string]config.OperatorConfig{
		"t_a":      transformOp(nil, []string{"feature_x"}, nil, nil),
		"t_b":      transformOp([]string{"feature_x"}, []string{"feature_y"}, nil, nil),
		"recall_a": recallOp([]string{"feature_x"}, []string{"item_id"}),
		"recall_b": recallOp([]string{"feature_y"}, []string{"item_id"}),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	// t_a -> t_b (RAW on feature_x)
	if !hasPred(g, "t_b", "t_a") {
		t.Error("expected RAW edge t_a -> t_b")
	}
	// t_a -> recall_a (RAW on feature_x)
	if !hasPred(g, "recall_a", "t_a") {
		t.Error("expected RAW edge t_a -> recall_a")
	}
	// t_b -> recall_b (RAW on feature_y)
	if !hasPred(g, "recall_b", "t_b") {
		t.Error("expected RAW edge t_b -> recall_b")
	}
	// recall_a does NOT depend on t_b (different common field)
	if hasPred(g, "recall_a", "t_b") {
		t.Error("recall_a should NOT depend on t_b")
	}
	// recall_a does NOT depend on recall_b (additive item writes)
	if hasPred(g, "recall_a", "recall_b") || hasPred(g, "recall_b", "recall_a") {
		t.Error("recalls should be independent")
	}
}

func TestRecallIndependentParallelWithTransformRecallChain(t *testing.T) {
	// recall_a has no dependencies at all.
	// transform_b reads a request field, writes bbb.
	// recall_c and recall_d both read bbb.
	//
	// Expected:
	//   - recall_a: zero predecessors
	//   - transform_b: zero predecessors (reads request-supplied field, no upstream writer)
	//   - recall_c: depends on transform_b only
	//   - recall_d: depends on transform_b only
	//   - NO edges between any recall pair (additive item writes)
	//   - recall_a is fully independent of transform_b/recall_c/recall_d
	seq := []string{"recall_a", "transform_b", "recall_c", "recall_d"}
	ops := map[string]config.OperatorConfig{
		"recall_a":    recallOp(nil, []string{"item_id"}),
		"transform_b": transformOp([]string{"req_field"}, []string{"bbb"}, nil, nil),
		"recall_c":    recallOp([]string{"bbb"}, []string{"item_id"}),
		"recall_d":    recallOp([]string{"bbb"}, []string{"item_id"}),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}

	// recall_a: zero predecessors
	if !hasNoPreds(g, "recall_a") {
		t.Error("recall_a should have no predecessors")
	}
	// transform_b: zero predecessors (req_field has no upstream writer)
	if !hasNoPreds(g, "transform_b") {
		t.Error("transform_b should have no predecessors")
	}
	// recall_c depends on transform_b
	if !hasPred(g, "recall_c", "transform_b") {
		t.Error("expected RAW edge transform_b -> recall_c")
	}
	// recall_d depends on transform_b
	if !hasPred(g, "recall_d", "transform_b") {
		t.Error("expected RAW edge transform_b -> recall_d")
	}
	// No edges among any recall pair
	recalls := []string{"recall_a", "recall_c", "recall_d"}
	for i := 0; i < len(recalls); i++ {
		for j := i + 1; j < len(recalls); j++ {
			if hasPred(g, recalls[j], recalls[i]) || hasPred(g, recalls[i], recalls[j]) {
				t.Errorf("expected no edge between %s and %s", recalls[i], recalls[j])
			}
		}
	}
	// recall_a has no edge to/from transform_b
	if hasPred(g, "recall_a", "transform_b") || hasPred(g, "transform_b", "recall_a") {
		t.Error("recall_a and transform_b should be fully independent")
	}
}

func TestRecallChainThenTransformReadsItems(t *testing.T) {
	// transform_embed writes user_vec (common), recall reads user_vec and outputs items,
	// then a downstream transform reads the item fields.
	// The downstream transform must depend on the recall (RAW on item_score),
	// and transitively on transform_embed.
	seq := []string{"transform_embed", "recall_a", "transform_score"}
	ops := map[string]config.OperatorConfig{
		"transform_embed": transformOp(nil, []string{"user_vec"}, nil, nil),
		"recall_a":        recallOp([]string{"user_vec"}, []string{"item_id", "item_score"}),
		"transform_score": transformOp(nil, nil, []string{"item_score"}, []string{"item_adjusted"}),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	// transform_embed -> recall_a (RAW on user_vec)
	if !hasPred(g, "recall_a", "transform_embed") {
		t.Error("expected edge transform_embed -> recall_a")
	}
	// recall_a -> transform_score (RAW on item_score — additive writer is still a writer)
	if !hasPred(g, "transform_score", "recall_a") {
		t.Error("expected edge recall_a -> transform_score via item_score")
	}
	// transform_score should NOT directly depend on transform_embed
	// (recall_a is the intermediary for item fields; no common field dependency)
	if hasPred(g, "transform_score", "transform_embed") {
		t.Error("transform_score should NOT directly depend on transform_embed")
	}
}

// --- Row dependency ---

func TestRowDependency_WaitsForRecalls(t *testing.T) {
	// Two recalls write items, row_dep op needs item set stable.
	// row_dep should depend on both recalls via _row_set_ RAW.
	seq := []string{"recall_a", "recall_b", "size"}
	ops := map[string]config.OperatorConfig{
		"recall_a": recallOp(nil, []string{"item_id"}),
		"recall_b": recallOp(nil, []string{"item_id"}),
		"size":     rowDepOp([]string{"item_count"}),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPred(g, "size", "recall_a") {
		t.Error("expected row-dep edge recall_a -> size")
	}
	if !hasPred(g, "size", "recall_b") {
		t.Error("expected row-dep edge recall_b -> size")
	}
	// recalls remain independent
	if hasPred(g, "recall_b", "recall_a") || hasPred(g, "recall_a", "recall_b") {
		t.Error("recalls should be independent")
	}
}

func TestRowDependency_WaitsForRecallsAfterBarrier(t *testing.T) {
	// recall_a, recall_b -> filter (barrier) -> recall_c -> size (row_dep)
	// size should depend on filter (barrier, also lastMutWriter of _row_set_) AND recall_c.
	seq := []string{"recall_a", "recall_b", "filter", "recall_c", "size"}
	ops := map[string]config.OperatorConfig{
		"recall_a": recallOp(nil, []string{"item_id"}),
		"recall_b": recallOp(nil, []string{"item_id"}),
		"filter":   filterOp([]string{"item_id"}, nil),
		"recall_c": recallOp(nil, []string{"item_id"}),
		"size":     rowDepOp([]string{"item_count"}),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	// size depends on filter (barrier is lastMutWriter of _row_set_)
	if !hasPred(g, "size", "filter") {
		t.Error("expected row-dep edge filter -> size")
	}
	// size depends on recall_c (post-barrier additive writer of _row_set_)
	if !hasPred(g, "size", "recall_c") {
		t.Error("expected row-dep edge recall_c -> size")
	}
	// size should NOT directly depend on pre-barrier recalls (barrier reset the tracker)
	// Note: barrier edges already make recall_a/b -> filter -> size transitive,
	// but the _row_set_ tracker should not create direct recall_a -> size edges.
	// Since filter is a barrier that forces all pre-filter ops to complete,
	// size already transitively depends on recall_a/b via filter.
	// The key point: _row_set_ tracker was reset by the barrier.
}

func TestRowDependency_DoesNotBlockDownstream(t *testing.T) {
	// recall_a -> size (row_dep, writes common item_count)
	//          -> transform_b (reads item_id, no row_dep)
	// transform_b should depend on recall_a (RAW on item_id), NOT on size.
	// size should not block unrelated downstream operators.
	seq := []string{"recall_a", "size", "transform_b"}
	ops := map[string]config.OperatorConfig{
		"recall_a":    recallOp(nil, []string{"item_id"}),
		"size":        rowDepOp([]string{"item_count"}),
		"transform_b": transformOp(nil, nil, []string{"item_id"}, []string{"item_score"}),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	// transform_b depends on recall_a (RAW on item_id)
	if !hasPred(g, "transform_b", "recall_a") {
		t.Error("expected RAW edge recall_a -> transform_b via item_id")
	}
	// transform_b should NOT depend on size (no field overlap)
	if hasPred(g, "transform_b", "size") {
		t.Error("transform_b should NOT depend on size (no field overlap)")
	}
	// size depends on recall_a (row-dep)
	if !hasPred(g, "size", "recall_a") {
		t.Error("expected row-dep edge recall_a -> size")
	}
}

func TestRowDependency_WithBarrier(t *testing.T) {
	// recall_a, recall_b -> filter -> size (row_dep)
	// No post-barrier recalls. size should depend on filter only via _row_set_.
	seq := []string{"recall_a", "recall_b", "filter", "size"}
	ops := map[string]config.OperatorConfig{
		"recall_a": recallOp(nil, []string{"item_id"}),
		"recall_b": recallOp(nil, []string{"item_id"}),
		"filter":   filterOp([]string{"item_id"}, nil),
		"size":     rowDepOp([]string{"item_count"}),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	// size depends on filter (barrier is lastMutWriter of _row_set_)
	if !hasPred(g, "size", "filter") {
		t.Error("expected row-dep edge filter -> size")
	}
}

func TestRowDependency_IndependentOfColumnOnlyTransform(t *testing.T) {
	// recall_a writes item_id, transform_b writes item_score (column-only transform).
	// size (row_dep) should depend on recall_a but NOT on transform_b.
	// transform_b only modifies columns, not the row set.
	seq := []string{"recall_a", "transform_b", "size"}
	ops := map[string]config.OperatorConfig{
		"recall_a":    recallOp(nil, []string{"item_id"}),
		"transform_b": transformOp(nil, nil, []string{"item_id"}, []string{"item_score"}),
		"size":        rowDepOp([]string{"item_count"}),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	// size depends on recall_a (additive writer of _row_set_)
	if !hasPred(g, "size", "recall_a") {
		t.Error("expected row-dep edge recall_a -> size")
	}
	// size should NOT depend on transform_b (column-only, no row-set mutation)
	if hasPred(g, "size", "transform_b") {
		t.Error("size should NOT depend on transform_b (column-only transform)")
	}
}

func TestRowDependency_MultipleRowDepOps(t *testing.T) {
	// Two row_dep ops reading the same row set should be independent of each other.
	// Both depend on recalls, but not on each other.
	seq := []string{"recall_a", "size_a", "size_b"}
	ops := map[string]config.OperatorConfig{
		"recall_a": recallOp(nil, []string{"item_id"}),
		"size_a":   rowDepOp([]string{"item_count"}),
		"size_b":   rowDepOp([]string{"item_total"}),
	}

	g, err := Build(seq, ops)
	if err != nil {
		t.Fatal(err)
	}
	// Both depend on recall_a
	if !hasPred(g, "size_a", "recall_a") {
		t.Error("expected row-dep edge recall_a -> size_a")
	}
	if !hasPred(g, "size_b", "recall_a") {
		t.Error("expected row-dep edge recall_a -> size_b")
	}
	// Independent of each other (different common output fields, both just read _row_set_)
	if hasPred(g, "size_b", "size_a") || hasPred(g, "size_a", "size_b") {
		t.Error("size_a and size_b should be independent")
	}
}

func FuzzBuild(f *testing.F) {
	f.Add("op_a", "op_b", "x", "y")

	f.Fuzz(func(_ *testing.T, nameA, nameB, fieldA, fieldB string) {
		ops := map[string]config.OperatorConfig{
			nameA: transformOp([]string{fieldA}, []string{fieldB}, nil, nil),
			nameB: transformOp([]string{fieldB}, nil, nil, nil),
		}
		Build([]string{nameA, nameB}, ops) //nolint:errcheck
	})
}
