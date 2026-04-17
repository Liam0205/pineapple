package dag

import (
	"fmt"

	"github.com/Liam0205/pineapple/internal/config"
	"github.com/Liam0205/pineapple/internal/types"
)

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
// It applies data-hazard rules (RAW, WAW, WAR) and special handling for
// recall operators and merge sources.
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

	// Validate: no cycles
	if _, err := TopologicalSort(g); err != nil {
		return nil, err
	}

	return g, nil
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

		isAdditiveWrite := !isCommon && opCfg.Recall

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
