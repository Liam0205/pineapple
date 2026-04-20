package dag

import (
	"fmt"

	"github.com/Liam0205/pineapple/internal/config"
	"github.com/Liam0205/pineapple/internal/types"
)

// rowSetSentinel is an implicit field name used to track item-collection-level
// (row-level) dependencies. See design_doc/02_flow_abstraction.md.
const rowSetSentinel = "_row_set_"

// Node represents an operator in the DAG.
type Node struct {
	Name   string
	Index  int
	Config config.OperatorConfig
	Preds  []int // predecessor node indexes
	Succs  []int // successor node indexes
}

// Graph is the compiled DAG.
type Graph struct {
	Nodes       []*Node
	NameToIndex map[string]int
}

// Build constructs a DAG from the flattened operator sequence and their configs.
// It applies type-aware dependency rules:
//   - Transform: field-level RAW/WAW/WAR hazard tracking
//   - Recall: additive writes (parallel with other Recalls), RAW from upstream Transforms
//   - Filter/Merge/Reorder: barrier semantics (all prior ops finish before, all later ops wait)
//   - Observe: read-only RAW dependencies, does not block downstream
func Build(sequence []string, operators map[string]config.OperatorConfig) (*Graph, error) {
	g := &Graph{
		NameToIndex: make(map[string]int, len(sequence)),
	}

	// Create nodes
	for i, name := range sequence {
		opCfg, ok := operators[name]
		if !ok {
			return nil, &types.ConfigError{Message: fmt.Sprintf("operator %q not found", name)}
		}
		node := &Node{
			Name:   name,
			Index:  i,
			Config: opCfg,
		}
		g.Nodes = append(g.Nodes, node)
		g.NameToIndex[name] = i
	}

	// Phase 1: Add barrier edges for Filter/Merge/Reorder
	addBarrierEdges(g, sequence, operators)

	// Phase 2: Apply data hazards for common and item fields separately
	// (only for non-barrier operators; barrier edges already enforce ordering)
	addEdges(g, sequence, operators, true)  // common fields
	addEdges(g, sequence, operators, false) // item fields

	// Phase 3: Add explicit edges for merge sources
	for i, name := range sequence {
		opCfg := operators[name]
		for _, src := range opCfg.Sources {
			srcIdx, ok := g.NameToIndex[src]
			if !ok {
				return nil, &types.ConfigError{
					Message: fmt.Sprintf("operator %q sources references unknown operator %q", name, src),
				}
			}
			addEdge(g, srcIdx, i)
		}
	}

	// Validate: no cycles
	if _, err := TopologicalSort(g); err != nil {
		return nil, err
	}

	return g, nil
}

// addBarrierEdges adds barrier edges for Filter, Merge, and Reorder operators.
// A barrier operator requires all preceding operators to complete before it starts,
// and all subsequent operators to wait for it to complete.
func addBarrierEdges(g *Graph, sequence []string, operators map[string]config.OperatorConfig) {
	n := len(sequence)

	for i, name := range sequence {
		opCfg := operators[name]
		opType := types.OperatorType(opCfg.OperatorType)
		if !opType.IsBarrier() {
			continue
		}

		// All preceding operators (in DSL order) must finish before this barrier
		for j := 0; j < i; j++ {
			addEdge(g, j, i)
		}

		// All subsequent operators (in DSL order) must wait for this barrier
		for j := i + 1; j < n; j++ {
			addEdge(g, i, j)
		}
	}
}

// fieldTracker tracks the writers and active readers for a single field.
// It distinguishes additive writes (recall AddItem) from mutating writes
// (regular SetItem) — additive writes don't conflict with each other but
// still create RAW dependencies for downstream readers.
type fieldTracker struct {
	lastMutWriter   int   // last mutating (SetItem) writer; -1 if none
	additiveWriters []int // AddItem writers (recall) since last mutating write
	activeReaders   []int // readers since last mutating write
}

// addEdges scans the operator sequence and adds data-hazard edges.
// If isCommon=true, processes common_input/common_output; otherwise item_input/item_output.
// For item fields, recall operators use additive write semantics (AddItem):
// they don't conflict with each other (no WAW/WAR), but downstream readers
// still depend on them (RAW).
//
// Barrier operators (Filter/Merge/Reorder) are skipped here since their
// ordering is fully determined by addBarrierEdges.
func addEdges(g *Graph, sequence []string, operators map[string]config.OperatorConfig, isCommon bool) {
	fields := make(map[string]*fieldTracker)

	getOrCreate := func(field string) *fieldTracker {
		ft, ok := fields[field]
		if !ok {
			ft = &fieldTracker{lastMutWriter: -1}
			fields[field] = ft
		}
		return ft
	}

	for i, name := range sequence {
		opCfg := operators[name]
		opType := types.OperatorType(opCfg.OperatorType)
		meta := opCfg.Meta

		// Barrier operators already have full ordering via addBarrierEdges
		if opType.IsBarrier() {
			// Still need to update field tracking state so that post-barrier
			// operators see the barrier as writer/reader where appropriate.
			var writeFields []string
			if isCommon {
				writeFields = meta.CommonOutput
			} else {
				writeFields = meta.ItemOutput
			}
			for _, field := range writeFields {
				ft := getOrCreate(field)
				ft.lastMutWriter = i
				ft.additiveWriters = nil
				ft.activeReaders = nil
			}
			var readFields []string
			if isCommon {
				readFields = meta.CommonInput
			} else {
				readFields = meta.ItemInput
			}
			for _, field := range readFields {
				ft := getOrCreate(field)
				// Reset readers: barrier consumed everything before it
				ft.activeReaders = []int{i}
			}
			// Reset row-set sentinel: barrier mutates the item collection
			if !isCommon {
				ft := getOrCreate(rowSetSentinel)
				ft.lastMutWriter = i
				ft.additiveWriters = nil
				ft.activeReaders = nil
			}
			continue
		}

		var readFields, writeFields []string
		if isCommon {
			readFields = meta.CommonInput
			writeFields = meta.CommonOutput
		} else {
			readFields = meta.ItemInput
			writeFields = meta.ItemOutput
		}

		isAdditiveWrite := !isCommon && opType == types.OpTypeRecall

		// Inject implicit row-set tracking for item pass.
		// Recall → additive writer on _row_set_ (parallel with other recalls).
		// RowDependency → reader of _row_set_ (waits for recalls and barriers).
		if !isCommon {
			if isAdditiveWrite {
				writeFields = append(writeFields[:len(writeFields):len(writeFields)], rowSetSentinel)
			}
			if opCfg.RowDependency {
				readFields = append(readFields[:len(readFields):len(readFields)], rowSetSentinel)
			}
		}

		// Process reads — RAW dependencies
		for _, field := range readFields {
			ft := getOrCreate(field)
			// RAW from last mutating writer
			if ft.lastMutWriter >= 0 {
				addEdge(g, ft.lastMutWriter, i)
			}
			// RAW from all additive writers
			for _, aw := range ft.additiveWriters {
				addEdge(g, aw, i)
			}
			// Observe is read-only and non-blocking: it gets RAW deps but does
			// not register as an active reader, so later writers won't create
			// WAR edges waiting for the Observe to finish.
			if opType != types.OpTypeObserve {
				ft.activeReaders = append(ft.activeReaders, i)
			}
		}

		// Process writes
		for _, field := range writeFields {
			ft := getOrCreate(field)

			if isAdditiveWrite {
				// Additive write (recall AddItem): no incoming WAW/WAR edges.
				// Just record as an additive writer for future RAW edges.
				ft.additiveWriters = append(ft.additiveWriters, i)
			} else {
				// Mutating write (regular SetItem): full WAW + WAR handling.
				// WAW from last mutating writer
				if ft.lastMutWriter >= 0 {
					addEdge(g, ft.lastMutWriter, i)
				}
				// WAW from all additive writers
				for _, aw := range ft.additiveWriters {
					addEdge(g, aw, i)
				}
				// WAR from all active readers
				for _, reader := range ft.activeReaders {
					if reader != i {
						addEdge(g, reader, i)
					}
				}
				// Reset: new mutating writer takes over
				ft.lastMutWriter = i
				ft.additiveWriters = nil
				ft.activeReaders = nil
			}
		}
	}
}

// addEdge adds a directed edge from -> to, avoiding duplicates.
func addEdge(g *Graph, from, to int) {
	if from == to {
		return
	}
	// Check for duplicate
	for _, s := range g.Nodes[from].Succs {
		if s == to {
			return
		}
	}
	g.Nodes[from].Succs = append(g.Nodes[from].Succs, to)
	g.Nodes[to].Preds = append(g.Nodes[to].Preds, from)
}

// TopologicalSort returns a valid topological ordering, or an error if cycles exist.
func TopologicalSort(g *Graph) ([]int, error) {
	n := len(g.Nodes)
	inDegree := make([]int, n)
	for _, node := range g.Nodes {
		inDegree[node.Index] = len(node.Preds)
	}

	queue := make([]int, 0)
	for i, d := range inDegree {
		if d == 0 {
			queue = append(queue, i)
		}
	}

	var order []int
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		order = append(order, curr)
		for _, succ := range g.Nodes[curr].Succs {
			inDegree[succ]--
			if inDegree[succ] == 0 {
				queue = append(queue, succ)
			}
		}
	}

	if len(order) != n {
		return nil, &types.ConfigError{Message: "DAG contains a cycle"}
	}
	return order, nil
}
