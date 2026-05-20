package dag

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Liam0205/pineapple/pine-go/internal/config"
	"github.com/Liam0205/pineapple/pine-go/internal/types"
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
		TypeName:             "test",
		OperatorType:         string(types.OpTypeRecall),
		Recall:               true,
		AdditiveWritesRowSet: true,
		Meta: config.Metadata{
			CommonInput: commonIn,
			ItemOutput:  itemOut,
		},
	}
}

// filterOp creates a Filter-type operator config for testing.
// Filter operators consume and mutate the row set.
func filterOp(itemIn, itemOut []string) config.OperatorConfig {
	return config.OperatorConfig{
		TypeName:       "test",
		OperatorType:   string(types.OpTypeFilter),
		ConsumesRowSet: true,
		MutatesRowSet:  true,
		Meta: config.Metadata{
			ItemInput:  itemIn,
			ItemOutput: itemOut,
		},
	}
}

// mergeOp creates a Merge-type operator config for testing.
// Merge operators consume and mutate the row set.
func mergeOp(sources []string, itemOut []string) config.OperatorConfig {
	return config.OperatorConfig{
		TypeName:       "test",
		OperatorType:   string(types.OpTypeMerge),
		Sources:        sources,
		ConsumesRowSet: true,
		MutatesRowSet:  true,
		Meta: config.Metadata{
			ItemOutput: itemOut,
		},
	}
}

// reorderOp creates a Reorder-type operator config for testing.
// Reorder operators consume and mutate the row set.
func reorderOp(itemIn []string) config.OperatorConfig {
	return config.OperatorConfig{
		TypeName:       "test",
		OperatorType:   string(types.OpTypeReorder),
		ConsumesRowSet: true,
		MutatesRowSet:  true,
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

// rowDepOp creates a Transform-type operator with ConsumesRowSet=true for testing.
// It only declares common output (no item fields), relying on row-set dependency.
func rowDepOp(commonOut []string) config.OperatorConfig {
	return config.OperatorConfig{
		TypeName:       "test",
		OperatorType:   string(types.OpTypeTransform),
		ConsumesRowSet: true,
		Meta:           config.Metadata{CommonOutput: commonOut},
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

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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
		"op_a":     transformOp(nil, []string{"user_embedding"}, nil, nil),
		"recall_a": recallOp([]string{"user_embedding"}, []string{"item_id"}),
	}

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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
	_, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes[0].Preds) != 0 || len(g.Nodes[0].Succs) != 0 {
		t.Error("single op with self read-write should have no edges")
	}
}

// --- Barrier semantics ---

func TestFilterBarrierSemantics(t *testing.T) {
	// transform_a writes score (item), transform_b writes rank (item).
	// Both have item fields → auto-inject _row_set_ read → both are _row_set_ readers.
	// filter consumes+mutates row set and reads score.
	// filter's MutatesRowSet creates WAR edges from all active _row_set_ readers,
	// including transform_b (even though no field overlap).
	// transform_c reads score (item) → auto-inject _row_set_ read → depends on
	// filter (RAW on _row_set_ from MutatesRowSet).
	seq := []string{"transform_a", "transform_b", "filter", "transform_c"}
	ops := map[string]config.OperatorConfig{
		"transform_a": transformOp(nil, nil, nil, []string{"score"}),
		"transform_b": transformOp(nil, nil, nil, []string{"rank"}),
		"filter":      filterOp([]string{"score"}, nil),
		"transform_c": transformOp(nil, nil, []string{"score"}, nil),
	}

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	// filter depends on transform_a (RAW on score + WAR on _row_set_)
	if !hasPred(g, "filter", "transform_a") {
		t.Error("expected edge transform_a -> filter")
	}
	// filter depends on transform_b (WAR on _row_set_: transform_b is an item-field
	// operator and thus an implicit _row_set_ reader; filter mutates _row_set_)
	if !hasPred(g, "filter", "transform_b") {
		t.Error("expected WAR edge transform_b -> filter via _row_set_")
	}
	// transform_c depends on filter (RAW on _row_set_: filter is lastMutWriter).
	// The direct edge transform_a -> transform_c is transitively reduced
	// (path exists via transform_a -> filter -> transform_c).
	if !hasPred(g, "transform_c", "filter") {
		t.Error("expected edge filter -> transform_c via _row_set_ RAW")
	}
	closure := transitiveClosure(g)
	aIdx := g.NameToIndex["transform_a"]
	cIdx := g.NameToIndex["transform_c"]
	if !closure[aIdx][cIdx] {
		t.Error("transform_c should be reachable from transform_a")
	}
}

func TestReorderBarrierSemantics(t *testing.T) {
	// transform_a writes score, reorder reads score and mutates row set,
	// transform_b reads score.
	// reorder depends on transform_a (RAW on score + WAR on _row_set_).
	// transform_b has item fields → auto-inject _row_set_ read →
	// depends on reorder (RAW on _row_set_: reorder is lastMutWriter).
	// The direct edge transform_a -> transform_b is transitively reduced
	// (path exists via transform_a -> reorder -> transform_b).
	seq := []string{"transform_a", "reorder", "transform_b"}
	ops := map[string]config.OperatorConfig{
		"transform_a": transformOp(nil, nil, nil, []string{"score"}),
		"reorder":     reorderOp([]string{"score"}),
		"transform_b": transformOp(nil, nil, []string{"score"}, nil),
	}

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPred(g, "reorder", "transform_a") {
		t.Error("expected edge transform_a -> reorder")
	}
	// transform_b depends on reorder via _row_set_ RAW
	if !hasPred(g, "transform_b", "reorder") {
		t.Error("expected edge reorder -> transform_b via _row_set_ RAW")
	}
	// transform_a -> transform_b is reachable (transitively via reorder)
	closure := transitiveClosure(g)
	aIdx := g.NameToIndex["transform_a"]
	bIdx := g.NameToIndex["transform_b"]
	if !closure[aIdx][bIdx] {
		t.Error("transform_b should be reachable from transform_a")
	}
}

func TestMergeBarrierSemantics(t *testing.T) {
	// merge consumes+mutates row set; all prior recalls contribute to _row_set_.
	// merge depends on both recalls (sources edges + _row_set_ RAW from additive writers).
	// transform_c reads item_id: depends on merge since merge is last mutating writer of _row_set_...
	// Actually, transform_c reads item_id field. Recall is the last writer of item_id (additive).
	// merge doesn't write item_id in its metadata but it mutates _row_set_.
	// transform_c depends on recalls (RAW on item_id).
	// Under new semantics: transform_c depends on recalls via item_id RAW,
	// and merge depends on recalls via sources + _row_set_.
	seq := []string{"recall_a", "recall_b", "merge", "transform_c"}
	ops := map[string]config.OperatorConfig{
		"recall_a":    recallOp(nil, []string{"item_id"}),
		"recall_b":    recallOp(nil, []string{"item_id"}),
		"merge":       mergeOp([]string{"recall_a", "recall_b"}, []string{"item_id"}),
		"transform_c": transformOp(nil, nil, []string{"item_id"}, nil),
	}

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	// merge depends on recalls (sources + _row_set_)
	if !hasPred(g, "merge", "recall_a") {
		t.Error("expected edge recall_a -> merge")
	}
	if !hasPred(g, "merge", "recall_b") {
		t.Error("expected edge recall_b -> merge")
	}
	// transform_c depends on merge (RAW on item_id — merge is the last mutating writer of item_id)
	if !hasPred(g, "transform_c", "merge") {
		t.Error("expected edge merge -> transform_c via item_id")
	}
}

func TestMultipleBarriersChain(t *testing.T) {
	// Two row-set mutators in sequence: filter then reorder.
	// With auto-inject, all item-field operators read _row_set_, creating a
	// clean linear chain after transitive reduction:
	//   transform_a -> filter -> transform_b -> reorder -> transform_c
	seq := []string{"transform_a", "filter", "transform_b", "reorder", "transform_c"}
	ops := map[string]config.OperatorConfig{
		"transform_a": transformOp(nil, nil, nil, []string{"score"}),
		"filter":      filterOp([]string{"score"}, nil),
		"transform_b": transformOp(nil, nil, []string{"score"}, []string{"rank"}),
		"reorder":     reorderOp([]string{"rank"}),
		"transform_c": transformOp(nil, nil, []string{"rank"}, nil),
	}

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	// filter depends on transform_a (RAW on score + WAR on _row_set_)
	if !hasPred(g, "filter", "transform_a") {
		t.Error("expected transform_a -> filter")
	}
	// transform_b depends on filter (RAW on _row_set_: filter is lastMutWriter)
	if !hasPred(g, "transform_b", "filter") {
		t.Error("expected filter -> transform_b via _row_set_ RAW")
	}
	// reorder depends on transform_b (RAW on rank + WAR on _row_set_)
	if !hasPred(g, "reorder", "transform_b") {
		t.Error("expected transform_b -> reorder")
	}
	// transform_c depends on reorder (RAW on _row_set_: reorder is lastMutWriter)
	if !hasPred(g, "transform_c", "reorder") {
		t.Error("expected reorder -> transform_c via _row_set_ RAW")
	}
	// Full chain reachability
	closure := transitiveClosure(g)
	aIdx := g.NameToIndex["transform_a"]
	cIdx := g.NameToIndex["transform_c"]
	if !closure[aIdx][cIdx] {
		t.Error("transform_c should be reachable from transform_a")
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

	g, err := Build(seq, ops, nil)
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

func TestObserveCreatesWAR(t *testing.T) {
	// Observe reads foo; a later transform writes foo. Observe registers as
	// activeReader, so transform_b must wait for observe (WAR edge).
	seq := []string{"transform_a", "observe", "transform_b"}
	ops := map[string]config.OperatorConfig{
		"transform_a": transformOp(nil, []string{"foo"}, nil, nil),
		"observe":     observeOp([]string{"foo"}, nil),
		"transform_b": transformOp(nil, []string{"foo"}, nil, nil),
	}

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	// observe should depend on transform_a (RAW on foo)
	if !hasPred(g, "observe", "transform_a") {
		t.Error("expected RAW edge transform_a -> observe")
	}
	// transform_b should depend on observe (WAR on foo)
	if !hasPred(g, "transform_b", "observe") {
		t.Error("expected WAR edge observe -> transform_b")
	}
	// WAW edge transform_a -> transform_b is transitively reduced
	// (path exists via observe), so no direct edge needed
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

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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
	// size should depend on recall_c (_row_set_ RAW) and transitively on filter.
	// After transitive reduction, the direct filter->size edge is removed
	// (implied by filter->recall_c->size).
	seq := []string{"recall_a", "recall_b", "filter", "recall_c", "size"}
	ops := map[string]config.OperatorConfig{
		"recall_a": recallOp(nil, []string{"item_id"}),
		"recall_b": recallOp(nil, []string{"item_id"}),
		"filter":   filterOp([]string{"item_id"}, nil),
		"recall_c": recallOp(nil, []string{"item_id"}),
		"size":     rowDepOp([]string{"item_count"}),
	}

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	// size depends on recall_c (post-barrier additive writer of _row_set_)
	if !hasPred(g, "size", "recall_c") {
		t.Error("expected row-dep edge recall_c -> size")
	}
	// filter -> size may be reduced (implied by filter -> recall_c -> size),
	// but size must still be reachable from filter.
	closure := transitiveClosure(g)
	filterIdx := g.NameToIndex["filter"]
	sizeIdx := g.NameToIndex["size"]
	if !closure[filterIdx][sizeIdx] {
		t.Error("size should be reachable from filter")
	}
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

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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

	g, err := Build(seq, ops, nil)
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

// --- Transitive reduction ---

// transitiveClosure computes the reachability matrix for a graph.
func transitiveClosure(g *Graph) [][]bool {
	n := len(g.Nodes)
	reach := make([][]bool, n)
	for i := range reach {
		reach[i] = make([]bool, n)
	}
	for i, node := range g.Nodes {
		visited := make([]bool, n)
		visited[i] = true
		queue := []int{i}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, s := range g.Nodes[cur].Succs {
				if !visited[s] {
					visited[s] = true
					reach[i][s] = true
					queue = append(queue, s)
				}
			}
		}
		_ = node
	}
	return reach
}

func totalEdges(g *Graph) int {
	count := 0
	for _, n := range g.Nodes {
		count += len(n.Succs)
	}
	return count
}

func TestReducePreservesReachability(t *testing.T) {
	// Manually build a graph: A->B, B->C, A->C (redundant)
	g := &Graph{
		NameToIndex: map[string]int{"A": 0, "B": 1, "C": 2},
		Nodes: []*Node{
			{Name: "A", Index: 0, Succs: []int{1, 2}, Preds: nil},
			{Name: "B", Index: 1, Succs: []int{2}, Preds: []int{0}},
			{Name: "C", Index: 2, Succs: nil, Preds: []int{0, 1}},
		},
	}

	closureBefore := transitiveClosure(g)

	reduce(g)

	closureAfter := transitiveClosure(g)

	// Reachability must be identical
	for i := range closureBefore {
		for j := range closureBefore[i] {
			if closureBefore[i][j] != closureAfter[i][j] {
				t.Errorf("reachability changed: [%d][%d] was %v, now %v",
					i, j, closureBefore[i][j], closureAfter[i][j])
			}
		}
	}

	// A->C should be removed (redundant via A->B->C)
	for _, s := range g.Nodes[0].Succs {
		if s == 2 {
			t.Error("expected redundant edge A->C to be removed")
		}
	}

	// Edge count should decrease: 3 -> 2
	if e := totalEdges(g); e != 2 {
		t.Errorf("expected 2 edges after reduction, got %d", e)
	}
}

func TestReduceDiamondGraph(t *testing.T) {
	// Diamond: A->{B,C}, B->D, C->D, plus redundant A->D
	g := &Graph{
		NameToIndex: map[string]int{"A": 0, "B": 1, "C": 2, "D": 3},
		Nodes: []*Node{
			{Name: "A", Index: 0, Succs: []int{1, 2, 3}, Preds: nil},
			{Name: "B", Index: 1, Succs: []int{3}, Preds: []int{0}},
			{Name: "C", Index: 2, Succs: []int{3}, Preds: []int{0}},
			{Name: "D", Index: 3, Succs: nil, Preds: []int{0, 1, 2}},
		},
	}

	closureBefore := transitiveClosure(g)
	edgesBefore := totalEdges(g)

	reduce(g)

	closureAfter := transitiveClosure(g)

	for i := range closureBefore {
		for j := range closureBefore[i] {
			if closureBefore[i][j] != closureAfter[i][j] {
				t.Errorf("reachability changed: [%d][%d] was %v, now %v",
					i, j, closureBefore[i][j], closureAfter[i][j])
			}
		}
	}

	edgesAfter := totalEdges(g)
	if edgesAfter >= edgesBefore {
		t.Errorf("expected fewer edges: before=%d, after=%d", edgesBefore, edgesAfter)
	}

	// A->D should be removed (reachable via A->B->D and A->C->D)
	for _, s := range g.Nodes[0].Succs {
		if s == 3 {
			t.Error("expected redundant edge A->D to be removed")
		}
	}
}

func TestReduceWithBarrierPipeline(t *testing.T) {
	// Build a real pipeline with row-set mutator and verify reduction happens.
	// recall_a, recall_b write item_id (additive). transform_c reads item_id, writes score.
	// filter reads score, consumes+mutates _row_set_.
	// transform_d reads score, writes rank. transform_e reads rank, writes final.
	//
	// Expected edges (before reduction):
	// - recall_a -> transform_c (RAW item_id), recall_b -> transform_c (RAW item_id)
	// - recall_a -> filter (_row_set_ additive -> consumer), recall_b -> filter (_row_set_)
	// - transform_c -> filter (RAW score), transform_c -> transform_d (RAW score)
	// - transform_d -> transform_e (RAW rank)
	// After reduction: some edges removed if implied by longer paths.
	seq := []string{"recall_a", "recall_b", "transform_c", "filter", "transform_d", "transform_e"}
	ops := map[string]config.OperatorConfig{
		"recall_a":    recallOp(nil, []string{"item_id"}),
		"recall_b":    recallOp(nil, []string{"item_id"}),
		"transform_c": transformOp(nil, nil, []string{"item_id"}, []string{"score"}),
		"filter":      filterOp([]string{"score"}, nil),
		"transform_d": transformOp(nil, nil, []string{"score"}, []string{"rank"}),
		"transform_e": transformOp(nil, nil, []string{"rank"}, []string{"final"}),
	}

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify reachability: filter should be reachable from recall_a, recall_b, transform_c
	closureMatrix := transitiveClosure(g)
	filterIdx := g.NameToIndex["filter"]
	for _, name := range []string{"recall_a", "recall_b", "transform_c"} {
		idx := g.NameToIndex[name]
		if !closureMatrix[idx][filterIdx] {
			t.Errorf("filter should be reachable from %s", name)
		}
	}

	// transform_e should be reachable from transform_c, transform_d
	eIdx := g.NameToIndex["transform_e"]
	for _, name := range []string{"transform_c", "transform_d"} {
		idx := g.NameToIndex[name]
		if !closureMatrix[idx][eIdx] {
			t.Errorf("transform_e should be reachable from %s", name)
		}
	}

	// Verify topological sort still works
	order, err := TopologicalSort(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != len(seq) {
		t.Errorf("expected %d nodes in topological order, got %d", len(seq), len(order))
	}
}

func TestReduceNoop(t *testing.T) {
	// Linear chain: no redundant edges, reduce should be a no-op
	g := &Graph{
		NameToIndex: map[string]int{"A": 0, "B": 1, "C": 2},
		Nodes: []*Node{
			{Name: "A", Index: 0, Succs: []int{1}, Preds: nil},
			{Name: "B", Index: 1, Succs: []int{2}, Preds: []int{0}},
			{Name: "C", Index: 2, Succs: nil, Preds: []int{1}},
		},
	}

	edgesBefore := totalEdges(g)
	reduce(g)
	edgesAfter := totalEdges(g)

	if edgesBefore != edgesAfter {
		t.Errorf("linear chain should not lose edges: before=%d, after=%d", edgesBefore, edgesAfter)
	}
}

func FuzzBuild(f *testing.F) {
	f.Add([]byte{2, 0, 1, 2, 3, 4, 5})
	f.Add([]byte{4, 1, 2, 3, 4, 5, 6, 7, 8})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 256 {
			t.Skip("DAG fuzz input exceeds CI budget")
		}
		seq, ops := fuzzDAGConfig(data)
		g, err := Build(seq, ops, nil)
		if err != nil {
			return
		}
		assertFuzzGraphInvariants(t, seq, ops, g)
	})
}

func fuzzDAGConfig(data []byte) ([]string, map[string]config.OperatorConfig) {
	next := func(defaultValue byte) byte {
		if len(data) == 0 {
			return defaultValue
		}
		b := data[0]
		data = data[1:]
		return b
	}
	field := func(b byte) string {
		return fmt.Sprintf("f%d", int(b)%8)
	}
	fields := func(max int) []string {
		count := int(next(0)) % (max + 1)
		out := make([]string, 0, count)
		seen := make(map[string]struct{}, count)
		for attempts := 0; len(out) < count && attempts < count+8; attempts++ {
			name := field(next(byte(attempts)))
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
		for candidate := 0; len(out) < count && candidate < 8; candidate++ {
			name := field(byte(candidate))
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
		return out
	}

	n := int(next(2))%8 + 1
	seq := make([]string, n)
	ops := make(map[string]config.OperatorConfig, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("op_%d", i)
		seq[i] = name
		switch next(0) % 6 {
		case 0:
			ops[name] = transformOp(fields(2), fields(2), fields(2), fields(2))
		case 1:
			ops[name] = recallOp(fields(2), fields(3))
		case 2:
			ops[name] = filterOp(fields(3), fields(1))
		case 3:
			sources := make([]string, 0)
			if i > 0 {
				sourceCount := int(next(0)) % (i + 1)
				seen := make(map[string]struct{}, sourceCount)
				for attempts := 0; len(sources) < sourceCount && attempts < sourceCount+i+1; attempts++ {
					src := seq[int(next(0))%i]
					if _, ok := seen[src]; ok {
						continue
					}
					seen[src] = struct{}{}
					sources = append(sources, src)
				}
				for candidate := 0; len(sources) < sourceCount && candidate < i; candidate++ {
					src := seq[candidate]
					if _, ok := seen[src]; ok {
						continue
					}
					seen[src] = struct{}{}
					sources = append(sources, src)
				}
			}
			ops[name] = mergeOp(sources, fields(2))
		case 4:
			ops[name] = reorderOp(fields(3))
		default:
			ops[name] = observeOp(fields(2), fields(2))
		}
		if next(0)%5 == 0 {
			op := ops[name]
			op.ConsumesRowSet = true
			ops[name] = op
		}
	}
	return seq, ops
}

func assertFuzzGraphInvariants(t *testing.T, seq []string, ops map[string]config.OperatorConfig, g *Graph) {
	t.Helper()
	if len(g.Nodes) != len(seq) {
		t.Fatalf("node count = %d, want %d", len(g.Nodes), len(seq))
	}
	for i, name := range seq {
		node := g.Nodes[i]
		if node.Index != i {
			t.Fatalf("node %d index = %d", i, node.Index)
		}
		if node.Name != name {
			t.Fatalf("node %d name = %q, want %q", i, node.Name, name)
		}
		if got, ok := g.NameToIndex[name]; !ok || got != i {
			t.Fatalf("NameToIndex[%q] = %d, %v; want %d, true", name, got, ok, i)
		}
	}
	for i, node := range g.Nodes {
		for _, pred := range node.Preds {
			if pred < 0 || pred >= len(g.Nodes) {
				t.Fatalf("node %d has out-of-range pred %d", i, pred)
			}
			if pred == i {
				t.Fatalf("node %d has self pred", i)
			}
			if !containsIndex(g.Nodes[pred].Succs, i) {
				t.Fatalf("node %d pred %d missing reverse succ", i, pred)
			}
		}
		for _, succ := range node.Succs {
			if succ < 0 || succ >= len(g.Nodes) {
				t.Fatalf("node %d has out-of-range succ %d", i, succ)
			}
			if succ == i {
				t.Fatalf("node %d has self succ", i)
			}
			if !containsIndex(g.Nodes[succ].Preds, i) {
				t.Fatalf("node %d succ %d missing reverse pred", i, succ)
			}
		}
	}
	order, err := TopologicalSort(g)
	if err != nil {
		t.Fatalf("TopologicalSort: %v", err)
	}
	position := make([]int, len(order))
	for pos, idx := range order {
		position[idx] = pos
	}
	for _, node := range g.Nodes {
		for _, succ := range node.Succs {
			if position[node.Index] >= position[succ] {
				t.Fatalf("edge %d -> %d violates topological order %v", node.Index, succ, order)
			}
		}
	}
	assertRowSetSafetyInvariant(t, seq, ops, g)
}

func assertRowSetSafetyInvariant(t *testing.T, seq []string, ops map[string]config.OperatorConfig, g *Graph) {
	t.Helper()
	closure := transitiveClosure(g)

	lastMutWriter := -1
	var additiveWriters []int

	for i, name := range seq {
		opCfg := ops[name]
		hasItemFields := len(opCfg.Meta.ItemInput) > 0 || len(opCfg.Meta.ItemOutput) > 0

		readsRowSet := opCfg.ConsumesRowSet || (hasItemFields && !opCfg.AdditiveWritesRowSet)

		if readsRowSet {
			if lastMutWriter >= 0 && !closure[lastMutWriter][i] {
				t.Errorf("row-set safety: %q (idx %d) reads _row_set_ but is not reachable from MutatesRowSet %q (idx %d)",
					name, i, seq[lastMutWriter], lastMutWriter)
			}
			for _, aw := range additiveWriters {
				if !closure[aw][i] {
					t.Errorf("row-set safety: %q (idx %d) reads _row_set_ but is not reachable from AdditiveWritesRowSet %q (idx %d)",
						name, i, seq[aw], aw)
				}
			}
		}

		if opCfg.AdditiveWritesRowSet {
			additiveWriters = append(additiveWriters, i)
		}
		if opCfg.MutatesRowSet {
			lastMutWriter = i
			additiveWriters = nil
		}
	}
}

func containsIndex(xs []int, target int) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}

// --- Cycle diagnostics (#9) ---

func TestTopologicalSortCycleReportsNodeNames(t *testing.T) {
	// Manually construct a graph with a cycle: A->B->C->A
	g := &Graph{
		NameToIndex: map[string]int{"alpha": 0, "beta": 1, "gamma": 2},
		Nodes: []*Node{
			{Name: "alpha", Index: 0, Succs: []int{1}, Preds: []int{2}},
			{Name: "beta", Index: 1, Succs: []int{2}, Preds: []int{0}},
			{Name: "gamma", Index: 2, Succs: []int{0}, Preds: []int{1}},
		},
	}

	_, err := TopologicalSort(g)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "alpha") {
		t.Errorf("error should mention 'alpha', got: %s", msg)
	}
	if !strings.Contains(msg, "beta") {
		t.Errorf("error should mention 'beta', got: %s", msg)
	}
	if !strings.Contains(msg, "gamma") {
		t.Errorf("error should mention 'gamma', got: %s", msg)
	}
}

func TestTopologicalSortPartialCycleReportsOnlyCycleNodes(t *testing.T) {
	// A->B->C, C->B (cycle between B and C only). A has no cycle.
	g := &Graph{
		NameToIndex: map[string]int{"entry": 0, "loop_x": 1, "loop_y": 2},
		Nodes: []*Node{
			{Name: "entry", Index: 0, Succs: []int{1}, Preds: nil},
			{Name: "loop_x", Index: 1, Succs: []int{2}, Preds: []int{0, 2}},
			{Name: "loop_y", Index: 2, Succs: []int{1}, Preds: []int{1}},
		},
	}

	_, err := TopologicalSort(g)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	msg := err.Error()
	// entry should NOT be in the cycle (it has in-degree 0, gets processed)
	if strings.Contains(msg, "entry") {
		t.Errorf("error should NOT mention 'entry' (not in cycle), got: %s", msg)
	}
	if !strings.Contains(msg, "loop_x") {
		t.Errorf("error should mention 'loop_x', got: %s", msg)
	}
	if !strings.Contains(msg, "loop_y") {
		t.Errorf("error should mention 'loop_y', got: %s", msg)
	}
}

// --- Combination tests (#10): nested SubFlow + barrier + sources + control ---

func TestNestedSubFlowWithBarrierAndSources(t *testing.T) {
	// Realistic pipeline:
	//   recall_a, recall_b (parallel) -> merge (ConsumesRowSet+MutatesRowSet, sources=[recall_a, recall_b])
	//   -> transform_post (reads merged items) -> filter (ConsumesRowSet+MutatesRowSet) -> transform_final
	// All in nested SubFlows.
	seq := []string{"recall_a", "recall_b", "merge", "transform_post", "filter", "transform_final"}
	ops := map[string]config.OperatorConfig{
		"recall_a":       recallOp(nil, []string{"item_id", "item_score"}),
		"recall_b":       recallOp(nil, []string{"item_id", "item_score"}),
		"merge":          mergeOp([]string{"recall_a", "recall_b"}, []string{"item_id", "item_score"}),
		"transform_post": transformOp(nil, nil, []string{"item_score"}, []string{"item_rank"}),
		"filter":         filterOp([]string{"item_rank"}, nil),
		"transform_final": transformOp(nil, nil, []string{"item_rank"}, []string{"item_output"}),
	}
	opToSubFlow := map[string]string{
		"recall_a":       "recall_stage",
		"recall_b":       "recall_stage",
		"merge":          "recall_stage",
		"transform_post": "process_stage",
		"filter":         "process_stage",
		"transform_final": "process_stage",
	}

	g, err := Build(seq, ops, opToSubFlow)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// recalls are parallel
	if hasPred(g, "recall_b", "recall_a") || hasPred(g, "recall_a", "recall_b") {
		t.Error("recalls should be independent")
	}
	// merge depends on both recalls (sources + _row_set_ consumer reads additive writers)
	if !hasPred(g, "merge", "recall_a") {
		t.Error("expected merge depends on recall_a")
	}
	if !hasPred(g, "merge", "recall_b") {
		t.Error("expected merge depends on recall_b")
	}
	// transform_post reads item_score: merge writes item_score (mutating writer), so depends on merge
	if !hasPred(g, "transform_post", "merge") {
		t.Error("expected transform_post depends on merge")
	}
	// filter reads item_rank: depends on transform_post (RAW on item_rank)
	if !hasPred(g, "filter", "transform_post") {
		t.Error("expected filter depends on transform_post")
	}
	// transform_final reads item_rank: depends on transform_post (last mutating writer of item_rank)
	// filter reads item_rank but doesn't write it, so transform_final depends on transform_post directly.
	closure := transitiveClosure(g)
	transformPostIdx := g.NameToIndex["transform_post"]
	transformFinalIdx := g.NameToIndex["transform_final"]
	if !closure[transformPostIdx][transformFinalIdx] {
		t.Error("transform_final should be reachable from transform_post")
	}

	// SubFlow membership is recorded
	for _, node := range g.Nodes {
		if node.SubFlow != opToSubFlow[node.Name] {
			t.Errorf("node %q SubFlow = %q, want %q", node.Name, node.SubFlow, opToSubFlow[node.Name])
		}
	}

	// Valid topological order
	order, err := TopologicalSort(g)
	if err != nil {
		t.Fatalf("TopologicalSort: %v", err)
	}
	if len(order) != len(seq) {
		t.Fatalf("order length = %d, want %d", len(order), len(seq))
	}
}

func TestControlFlowWithBarrierInteraction(t *testing.T) {
	// Control flow interleaved with row-set mutator:
	//   ctrl_if produces _if_1 (common), transform_branch reads _if_1 + item_score writes item_rank,
	//   filter (ConsumesRowSet+MutatesRowSet) reads item_rank, transform_post reads item_rank.
	// Ensures control fields don't break row-set semantics.
	seq := []string{"recall_init", "ctrl_if", "transform_branch", "filter", "transform_post"}
	ops := map[string]config.OperatorConfig{
		"recall_init": recallOp(nil, []string{"item_id", "item_score"}),
		"ctrl_if": {
			TypeName:     "test",
			OperatorType: string(types.OpTypeTransform),
			Meta: config.Metadata{
				CommonInput:  []string{"enabled"},
				CommonOutput: []string{"_if_1"},
			},
		},
		"transform_branch": transformOp([]string{"_if_1"}, nil, []string{"item_score"}, []string{"item_rank"}),
		"filter":           filterOp([]string{"item_rank"}, nil),
		"transform_post":   transformOp(nil, nil, []string{"item_rank"}, []string{"item_output"}),
	}

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// ctrl_if is independent of recall_init (different fields)
	if hasPred(g, "ctrl_if", "recall_init") {
		t.Error("ctrl_if should not depend on recall_init (no field overlap)")
	}

	// transform_branch depends on ctrl_if (RAW _if_1) and recall_init (RAW item_score)
	if !hasPred(g, "transform_branch", "ctrl_if") {
		t.Error("expected transform_branch depends on ctrl_if")
	}
	if !hasPred(g, "transform_branch", "recall_init") {
		t.Error("expected transform_branch depends on recall_init")
	}

	// filter reads item_rank: depends on transform_branch (RAW item_rank)
	if !hasPred(g, "filter", "transform_branch") {
		t.Error("expected filter depends on transform_branch")
	}

	// transform_post reads item_rank: depends on transform_branch (last mutating writer of item_rank).
	// With auto-inject, transform_post also reads _row_set_: depends on filter (MutatesRowSet lastMutWriter).
	// After transitive reduction, the direct edge transform_branch -> transform_post is removed
	// (implied by transform_branch -> filter -> transform_post).
	closure := transitiveClosure(g)
	branchIdx := g.NameToIndex["transform_branch"]
	postIdx := g.NameToIndex["transform_post"]
	if !closure[branchIdx][postIdx] {
		t.Error("transform_post should be reachable from transform_branch")
	}
	if !hasPred(g, "transform_post", "filter") {
		t.Error("expected filter -> transform_post via _row_set_ RAW")
	}
}

func TestRowDepWithRecallAndBarrierCombined(t *testing.T) {
	// recall_a, recall_b -> size (ConsumesRowSet) -> filter (ConsumesRowSet+MutatesRowSet) -> transform_final
	// Ensures row dependency + row-set mutation + recall all compose correctly.
	seq := []string{"recall_a", "recall_b", "size", "filter", "transform_final"}
	ops := map[string]config.OperatorConfig{
		"recall_a":       recallOp(nil, []string{"item_id"}),
		"recall_b":       recallOp(nil, []string{"item_id"}),
		"size":           rowDepOp([]string{"item_count"}),
		"filter":         filterOp([]string{"item_id"}, nil),
		"transform_final": transformOp([]string{"item_count"}, nil, []string{"item_id"}, []string{"item_output"}),
	}

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// size depends on both recalls (row-dep via _row_set_)
	if !hasPred(g, "size", "recall_a") {
		t.Error("expected size depends on recall_a")
	}
	if !hasPred(g, "size", "recall_b") {
		t.Error("expected size depends on recall_b")
	}
	// filter consumes _row_set_: depends on recalls (additive writers) and size (active reader via WAR)
	closure := transitiveClosure(g)
	sizeIdx := g.NameToIndex["size"]
	filterIdx := g.NameToIndex["filter"]
	if !closure[sizeIdx][filterIdx] {
		t.Error("filter should be reachable from size")
	}
	// transform_final depends on size (RAW item_count) and filter or recalls (RAW item_id)
	finalIdx := g.NameToIndex["transform_final"]
	if !closure[sizeIdx][finalIdx] {
		t.Error("transform_final should be reachable from size")
	}
}

func TestObserveInNestedSubFlowDoesNotBlock(t *testing.T) {
	// Observe node in a nested SubFlow reads item fields but must not block
	// downstream transforms that write to the same field.
	seq := []string{"recall_a", "observe_log", "transform_b"}
	ops := map[string]config.OperatorConfig{
		"recall_a":    recallOp(nil, []string{"item_id", "item_score"}),
		"observe_log": observeOp(nil, []string{"item_score"}),
		"transform_b": transformOp(nil, nil, []string{"item_score"}, []string{"item_rank"}),
	}
	opToSubFlow := map[string]string{
		"recall_a":    "stage1",
		"observe_log": "stage1",
		"transform_b": "stage2",
	}

	g, err := Build(seq, ops, opToSubFlow)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Both observe and transform_b depend on recall_a
	if !hasPred(g, "observe_log", "recall_a") {
		t.Error("expected observe_log depends on recall_a")
	}
	if !hasPred(g, "transform_b", "recall_a") {
		t.Error("expected transform_b depends on recall_a")
	}
	// transform_b must NOT depend on observe_log (observe is non-blocking)
	if hasPred(g, "transform_b", "observe_log") {
		t.Error("transform_b should NOT depend on observe_log")
	}
}

// TestBuildDeepNestedSubFlowDAG verifies DAG construction with 4+ level
// SubFlow nesting, per-level control ops, and mixed operators.
func TestBuildDeepNestedSubFlowDAG(t *testing.T) {
	// Mirrors the Python test: Flow -> L1 -> L2 -> L3, each with control + ops.
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
		"recall_top": recallOp(nil, []string{"item_id", "item_score"}),
		"ctrl_if": {
			TypeName:     "test",
			OperatorType: string(types.OpTypeTransform),
			Skip:         nil,
			Meta: config.Metadata{
				CommonInput:  []string{"enabled"},
				CommonOutput: []string{"_if_1"},
			},
		},
		"transform_l1": transformOp([]string{"_if_1"}, nil, []string{"item_score"}, []string{"item_score"}),
		"ctrl_l1_if": {
			TypeName:     "test",
			OperatorType: string(types.OpTypeTransform),
			Skip:         []string{"_if_1"},
			Meta: config.Metadata{
				CommonInput:  []string{"flag_l1", "_if_1"},
				CommonOutput: []string{"_L1::if_1"},
			},
		},
		"transform_l2": transformOp([]string{"_if_1", "_L1::if_1"}, nil, []string{"item_score"}, []string{"item_score"}),
		"ctrl_l1_l2_if": {
			TypeName:     "test",
			OperatorType: string(types.OpTypeTransform),
			Skip:         []string{"_if_1", "_L1::if_1"},
			Meta: config.Metadata{
				CommonInput:  []string{"flag_l2", "_if_1", "_L1::if_1"},
				CommonOutput: []string{"_L1::L2::if_1"},
			},
		},
		"transform_l3": transformOp([]string{"_if_1", "_L1::if_1", "_L1::L2::if_1"}, nil, []string{"item_score"}, []string{"item_score"}),
		"ctrl_l1_l2_l3_if": {
			TypeName:     "test",
			OperatorType: string(types.OpTypeTransform),
			Skip:         []string{"_if_1", "_L1::if_1", "_L1::L2::if_1"},
			Meta: config.Metadata{
				CommonInput:  []string{"flag_l3", "_if_1", "_L1::if_1", "_L1::L2::if_1"},
				CommonOutput: []string{"_L1::L2::L3::if_1"},
			},
		},
		"transform_leaf": transformOp([]string{"_L1::L2::L3::if_1", "_if_1", "_L1::if_1", "_L1::L2::if_1"}, nil, []string{"item_score"}, []string{"item_score"}),
		"ctrl_else": {
			TypeName:     "test",
			OperatorType: string(types.OpTypeTransform),
			Skip:         nil,
			Meta: config.Metadata{
				CommonInput:  []string{"enabled"},
				CommonOutput: []string{"_else_2"},
			},
		},
		"transform_else": transformOp([]string{"_else_2"}, nil, []string{"item_score"}, []string{"item_score"}),
	}
	opToSubFlow := map[string]string{
		"transform_l1": "L1",
		"ctrl_l1_if":   "L1",
		"transform_l2": "L1/L2",
		"ctrl_l1_l2_if": "L1/L2",
		"transform_l3": "L1/L2/L3",
		"ctrl_l1_l2_l3_if": "L1/L2/L3",
		"transform_leaf": "L1/L2/L3",
	}

	g, err := Build(sequence, operators, opToSubFlow)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if len(g.Nodes) != len(sequence) {
		t.Fatalf("node count = %d, want %d", len(g.Nodes), len(sequence))
	}

	// Verify basic graph invariants
	for i, node := range g.Nodes {
		if node.Name != sequence[i] {
			t.Errorf("node %d name = %q, want %q", i, node.Name, sequence[i])
		}
		if node.SubFlow != opToSubFlow[node.Name] {
			t.Errorf("node %q SubFlow = %q, want %q", node.Name, node.SubFlow, opToSubFlow[node.Name])
		}
	}

	// recall_top produces item_score; all transforms consume it → data dependency chain exists
	recallIdx := g.NameToIndex["recall_top"]
	if len(g.Nodes[recallIdx].Succs) == 0 {
		t.Error("recall_top should have successors (item_score dependency)")
	}

	// else branch op should NOT depend on inner control ops (independent branch)
	elseIdx := g.NameToIndex["transform_else"]
	for _, pred := range g.Nodes[elseIdx].Preds {
		predName := g.Nodes[pred].Name
		if predName == "ctrl_l1_if" || predName == "ctrl_l1_l2_if" || predName == "ctrl_l1_l2_l3_if" {
			t.Errorf("transform_else should not depend on control op %q (independent branch)", predName)
		}
	}

	// Topological sort must succeed (no cycles)
	order, err := TopologicalSort(g)
	if err != nil {
		t.Fatalf("TopologicalSort: %v", err)
	}
	position := make([]int, len(order))
	for pos, idx := range order {
		position[idx] = pos
	}
	for _, node := range g.Nodes {
		for _, succ := range node.Succs {
			if position[node.Index] >= position[succ] {
				t.Errorf("edge %s -> %s violates topological order", node.Name, g.Nodes[succ].Name)
			}
		}
	}

	t.Logf("Deep nested DAG built successfully with %d nodes, %d edges",
		len(g.Nodes), func() int {
			c := 0
			for _, n := range g.Nodes {
				c += len(n.Succs)
			}
			return c
		}())
}

// --- Implicit row-set dependency (auto-inject _row_set_ read for item-field operators) ---

func TestImplicitRowSetDep_FilterThenItemTransform(t *testing.T) {
	// Core bug scenario: recall writes items, filter truncates (MutatesRowSet),
	// transform reads a DIFFERENT item field than filter.
	// Without auto-inject, transform has no _row_set_ edge to filter.
	// With auto-inject, transform reads _row_set_ → RAW from filter (lastMutWriter).
	seq := []string{"recall_a", "filter", "transform_hydrate"}
	ops := map[string]config.OperatorConfig{
		"recall_a":          recallOp(nil, []string{"rel_id", "type", "score"}),
		"filter":            filterOp([]string{"score"}, nil),
		"transform_hydrate": transformOp(nil, nil, []string{"rel_id", "type"}, []string{"creator_id"}),
	}

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	// transform_hydrate must depend on filter (via _row_set_ RAW)
	if !hasPred(g, "transform_hydrate", "filter") {
		t.Error("expected filter -> transform_hydrate via _row_set_ RAW")
	}
	// filter must depend on recall_a
	if !hasPred(g, "filter", "recall_a") {
		t.Error("expected recall_a -> filter")
	}
}

func TestImplicitRowSetDep_ItemOutputOnlyTransform(t *testing.T) {
	// Transform with only item_output (no item_input) still needs row-set stability:
	// SetItem requires valid indices.
	seq := []string{"recall_a", "filter", "transform_enrich"}
	ops := map[string]config.OperatorConfig{
		"recall_a":         recallOp(nil, []string{"item_id"}),
		"filter":           filterOp([]string{"item_id"}, nil),
		"transform_enrich": transformOp(nil, nil, nil, []string{"enriched_field"}),
	}

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPred(g, "transform_enrich", "filter") {
		t.Error("expected filter -> transform_enrich via _row_set_ RAW (item_output needs stable indices)")
	}
}

func TestImplicitRowSetDep_RecallDependency(t *testing.T) {
	// Item-field transform must depend on all preceding Recalls via _row_set_,
	// even when there is no field-level overlap.
	seq := []string{"recall_a", "recall_b", "transform_x"}
	ops := map[string]config.OperatorConfig{
		"recall_a":    recallOp(nil, []string{"rel_id"}),
		"recall_b":    recallOp(nil, []string{"score"}),
		"transform_x": transformOp(nil, nil, []string{"score"}, []string{"adjusted"}),
	}

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	// transform_x depends on recall_b via field-level RAW (score)
	if !hasPred(g, "transform_x", "recall_b") {
		t.Error("expected recall_b -> transform_x via score RAW")
	}
	// transform_x also depends on recall_a via _row_set_ RAW (additive writer)
	if !hasPred(g, "transform_x", "recall_a") {
		t.Error("expected recall_a -> transform_x via _row_set_ RAW (additive writer)")
	}
}

func TestImplicitRowSetDep_CommonOnlyTransformUnaffected(t *testing.T) {
	// Transform with only common fields and no item fields should NOT get
	// _row_set_ read injected — it doesn't access items by index.
	seq := []string{"recall_a", "filter", "transform_common"}
	ops := map[string]config.OperatorConfig{
		"recall_a":         recallOp(nil, []string{"item_id"}),
		"filter":           filterOp([]string{"item_id"}, nil),
		"transform_common": transformOp([]string{"user_id"}, []string{"user_flag"}, nil, nil),
	}

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hasPred(g, "transform_common", "filter") {
		t.Error("common-only transform should NOT depend on filter (no item fields)")
	}
	if hasPred(g, "transform_common", "recall_a") {
		t.Error("common-only transform should NOT depend on recall_a (no item fields)")
	}
}

func TestImplicitRowSetDep_ObserveWithItemFields(t *testing.T) {
	// Observe with item fields also gets _row_set_ read injection.
	seq := []string{"recall_a", "filter", "observe_log"}
	ops := map[string]config.OperatorConfig{
		"recall_a":    recallOp(nil, []string{"item_id", "score"}),
		"filter":      filterOp([]string{"score"}, nil),
		"observe_log": observeOp(nil, []string{"item_id"}),
	}

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPred(g, "observe_log", "filter") {
		t.Error("expected filter -> observe_log via _row_set_ RAW")
	}
}

func TestImplicitRowSetDep_CommonOnlyAfterFilter(t *testing.T) {
	// Filter (MutatesRowSet) followed by common-only transform.
	// Common-only transform has no item fields → no _row_set_ injection → must NOT depend on filter.
	seq := []string{"recall_a", "filter", "common_calc"}
	ops := map[string]config.OperatorConfig{
		"recall_a":    recallOp(nil, []string{"item_id", "score"}),
		"filter":      filterOp([]string{"score"}, nil),
		"common_calc": transformOp([]string{"user_id"}, []string{"user_flag"}, nil, nil),
	}

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hasPred(g, "common_calc", "filter") {
		t.Error("common-only transform should NOT depend on filter (no item fields)")
	}
	if hasPred(g, "common_calc", "recall_a") {
		t.Error("common-only transform should NOT depend on recall_a (no item fields)")
	}
}

func TestImplicitRowSetDep_CommonOnlyAfterReorder(t *testing.T) {
	// Reorder (MutatesRowSet) followed by common-only transform.
	seq := []string{"recall_a", "reorder", "common_calc"}
	ops := map[string]config.OperatorConfig{
		"recall_a":    recallOp(nil, []string{"item_id", "score"}),
		"reorder":     reorderOp([]string{"score"}),
		"common_calc": transformOp([]string{"user_id"}, []string{"user_flag"}, nil, nil),
	}

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hasPred(g, "common_calc", "reorder") {
		t.Error("common-only transform should NOT depend on reorder (no item fields)")
	}
	if hasPred(g, "common_calc", "recall_a") {
		t.Error("common-only transform should NOT depend on recall_a (no item fields)")
	}
}

func TestImplicitRowSetDep_CommonOnlyBetweenFilterAndItemTransform(t *testing.T) {
	// Filter → common_calc → item_transform.
	// common_calc should NOT depend on filter (no item fields).
	// item_transform SHOULD depend on filter (via _row_set_ RAW).
	// item_transform should NOT depend on common_calc (no data dependency).
	seq := []string{"recall_a", "filter", "common_calc", "item_transform"}
	ops := map[string]config.OperatorConfig{
		"recall_a":       recallOp(nil, []string{"item_id", "score"}),
		"filter":         filterOp([]string{"score"}, nil),
		"common_calc":    transformOp([]string{"user_id"}, []string{"user_flag"}, nil, nil),
		"item_transform": transformOp(nil, nil, []string{"item_id"}, []string{"enriched"}),
	}

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hasPred(g, "common_calc", "filter") {
		t.Error("common_calc should NOT depend on filter")
	}
	if !hasPred(g, "item_transform", "filter") {
		t.Error("expected filter -> item_transform via _row_set_ RAW")
	}
	if hasPred(g, "item_transform", "common_calc") {
		t.Error("item_transform should NOT depend on common_calc (no data overlap)")
	}
}

func TestImplicitRowSetDep_WARBeforeMutatesRowSet(t *testing.T) {
	// Item-field transform before a MutatesRowSet: the mutator must wait
	// for the transform to finish (WAR on _row_set_).
	seq := []string{"recall_a", "transform_x", "filter"}
	ops := map[string]config.OperatorConfig{
		"recall_a":    recallOp(nil, []string{"item_id", "score"}),
		"transform_x": transformOp(nil, nil, []string{"score"}, []string{"adjusted"}),
		"filter":      filterOp([]string{"adjusted"}, nil),
	}

	g, err := Build(seq, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	// filter depends on transform_x (RAW on adjusted + WAR on _row_set_)
	if !hasPred(g, "filter", "transform_x") {
		t.Error("expected transform_x -> filter (RAW adjusted + WAR _row_set_)")
	}
}
