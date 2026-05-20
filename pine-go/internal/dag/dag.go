package dag

import (
	"fmt"

	"github.com/Liam0205/pineapple/pine-go/internal/config"
	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

// rowSetSentinel is an implicit field name used to track item-collection-level
// (row-level) dependencies. See design_doc/02_flow_abstraction.md.
const rowSetSentinel = "_row_set_"

// Node represents an operator in the DAG.
type Node struct {
	Name    string
	Index   int
	SubFlow string // SubFlow membership; empty if ungrouped
	Config  config.OperatorConfig
	Preds   []int // predecessor node indexes
	Succs   []int // successor node indexes
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
//   - ConsumesRowSet: reads _row_set_ sentinel (waits for row set to stabilize)
//   - MutatesRowSet: mutating write to _row_set_ sentinel (serializes row-set mutations)
//   - Observe: read-only RAW dependencies, does not block downstream
func Build(sequence []string, operators map[string]config.OperatorConfig, opToSubFlow map[string]string) (*Graph, error) {
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
			Name:    name,
			Index:   i,
			SubFlow: opToSubFlow[name],
			Config:  opCfg,
		}
		g.Nodes = append(g.Nodes, node)
		g.NameToIndex[name] = i
	}

	// Apply data hazards for common and item fields separately
	addEdges(g, sequence, operators, true)  // common fields
	addEdges(g, sequence, operators, false) // item fields

	// Add explicit edges for merge sources
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

	// Transitive reduction — remove edges implied by longer paths.
	reduce(g)

	// Validate: no cycles
	if _, err := TopologicalSort(g); err != nil {
		return nil, err
	}

	return g, nil
}

// fieldTracker tracks the writers and active readers for a single field.
// It distinguishes additive writes (recall AddItem) from mutating writes
// (regular SetItem). Additive writes (A) follow the rule: "A acts as W
// toward R/W, acts as R toward other A" — i.e., A-A has no ordering
// constraint, but A still participates in WAR/WAW edges with R and W.
type fieldTracker struct {
	lastMutWriter   int   // last mutating (SetItem) writer; -1 if none
	additiveWriters []int // AddItem writers (recall) since last mutating write
	activeReaders   []int // readers since last mutating write
}

// addEdges scans the operator sequence and adds data-hazard edges.
// If isCommon=true, processes common_input/common_output; otherwise item_input/item_output.
//
// Additive write hazard rules (A = Additive, R = Read, W = mutating Write):
//   AAR=WAR, AAW=WAW, RAA=RAW, WAA=WAW, AAA=RAR(no-op)
//
// ConsumesRowSet operators read _row_set_ sentinel (waits for row set to stabilize).
// MutatesRowSet operators perform mutating writes to _row_set_ (serializes mutations).
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
		meta := opCfg.Meta

		var readFields, writeFields []string
		if isCommon {
			readFields = meta.CommonInput
			writeFields = meta.CommonOutput
		} else {
			readFields = meta.ItemInput
			writeFields = meta.ItemOutput
		}

		isAdditiveWrite := !isCommon && opCfg.AdditiveWritesRowSet

		// Inject implicit row-set tracking for item pass.
		// AdditiveWritesRowSet → additive writer on _row_set_ (parallel with other additive writers).
		// ConsumesRowSet → reader of _row_set_ (waits for additive writers and row-set mutators).
		// Any operator with item fields that is not AdditiveWritesRowSet and not
		// already ConsumesRowSet → auto-inject _row_set_ read (item-field access
		// requires stable row-set indices for both GetItem and SetItem).
		if !isCommon {
			if isAdditiveWrite {
				writeFields = append(writeFields[:len(writeFields):len(writeFields)], rowSetSentinel)
			}
			if opCfg.ConsumesRowSet {
				readFields = append(readFields[:len(readFields):len(readFields)], rowSetSentinel)
			}
			if !opCfg.ConsumesRowSet && !isAdditiveWrite {
				if len(readFields) > 0 || len(writeFields) > 0 {
					readFields = append(readFields[:len(readFields):len(readFields)], rowSetSentinel)
				}
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
			ft.activeReaders = append(ft.activeReaders, i)
		}

		// Process writes
		for _, field := range writeFields {
			ft := getOrCreate(field)

			if isAdditiveWrite {
				// Additive write (recall AddItem): wait for preceding mutators
				// and active readers, then record as additive writer.
				if ft.lastMutWriter >= 0 {
					addEdge(g, ft.lastMutWriter, i)
				}
				for _, reader := range ft.activeReaders {
					if reader != i {
						addEdge(g, reader, i)
					}
				}
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

		// MutatesRowSet: mutating write to _row_set_
		if !isCommon && opCfg.MutatesRowSet {
			ft := getOrCreate(rowSetSentinel)
			if ft.lastMutWriter >= 0 {
				addEdge(g, ft.lastMutWriter, i)
			}
			for _, aw := range ft.additiveWriters {
				addEdge(g, aw, i)
			}
			for _, reader := range ft.activeReaders {
				if reader != i {
					addEdge(g, reader, i)
				}
			}
			ft.lastMutWriter = i
			ft.additiveWriters = ft.additiveWriters[:0]
			ft.activeReaders = ft.activeReaders[:0]
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
		var cycleNodes []string
		for i, d := range inDegree {
			if d > 0 {
				cycleNodes = append(cycleNodes, g.Nodes[i].Name)
			}
		}
		return nil, &types.ConfigError{
			Message: fmt.Sprintf("DAG contains a cycle involving operators: %v", cycleNodes),
		}
	}
	return order, nil
}

// reduce replaces the graph's edge set with its transitive reduction —
// the minimal edge set that preserves the same reachability relation.
func reduce(g *Graph) {
	kept := reducedEdges(g)

	for _, node := range g.Nodes {
		node.Preds = node.Preds[:0]
		node.Succs = node.Succs[:0]
	}

	for _, e := range kept {
		g.Nodes[e[0]].Succs = append(g.Nodes[e[0]].Succs, e[1])
		g.Nodes[e[1]].Preds = append(g.Nodes[e[1]].Preds, e[0])
	}
}

// reducedEdges returns the transitive reduction of the DAG — the minimal
// edge set that preserves the same reachability relation. For each edge
// u→v, if v is reachable from u via another path, the edge is redundant.
func reducedEdges(g *Graph) [][2]int {
	n := len(g.Nodes)

	adj := make([][]int, n)
	for i, node := range g.Nodes {
		adj[i] = node.Succs
	}

	var edges [][2]int
	for u := 0; u < n; u++ {
		for _, v := range adj[u] {
			if !reachableWithout(adj, n, u, v) {
				edges = append(edges, [2]int{u, v})
			}
		}
	}
	return edges
}

// reachableWithout checks if dst is reachable from src via BFS,
// excluding the direct edge src→dst.
func reachableWithout(adj [][]int, n, src, dst int) bool {
	visited := make([]bool, n)
	visited[src] = true
	queue := make([]int, 0, n)

	for _, next := range adj[src] {
		if next == dst {
			continue
		}
		if !visited[next] {
			visited[next] = true
			queue = append(queue, next)
		}
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == dst {
			return true
		}
		for _, next := range adj[cur] {
			if !visited[next] {
				visited[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}
