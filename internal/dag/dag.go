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

// fieldTracker tracks the last writer and active readers for a single field.
type fieldTracker struct {
	lastWriter    int   // -1 if no writer yet
	activeReaders []int // readers since last write
}

// addEdges scans the operator sequence and adds data-hazard edges.
// If isCommon=true, processes common_input/common_output; otherwise item_input/item_output.
// For recall operators (recall=true), item_output is excluded from field tracking.
func addEdges(g *Graph, sequence []string, operators map[string]config.OperatorConfig, isCommon bool) {
	fields := make(map[string]*fieldTracker)

	getOrCreate := func(field string) *fieldTracker {
		ft, ok := fields[field]
		if !ok {
			ft = &fieldTracker{lastWriter: -1}
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
			// Recall operators: item_output excluded from field-level DAG
			if opCfg.Recall {
				writeFields = nil
			} else {
				writeFields = meta.ItemOutput
			}
		}

		// Process reads
		for _, field := range readFields {
			ft := getOrCreate(field)
			// RAW: if there's a writer, reader depends on it
			if ft.lastWriter >= 0 {
				addEdge(g, ft.lastWriter, i)
			}
			ft.activeReaders = append(ft.activeReaders, i)
		}

		// Process writes
		for _, field := range writeFields {
			ft := getOrCreate(field)
			// WAW: if there's a previous writer, new writer depends on it
			if ft.lastWriter >= 0 {
				addEdge(g, ft.lastWriter, i)
			}
			// WAR: new writer must wait for all active readers
			for _, reader := range ft.activeReaders {
				if reader != i { // don't self-depend
					addEdge(g, reader, i)
				}
			}
			// Reset: new writer replaces old
			ft.lastWriter = i
			ft.activeReaders = nil
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
