package dag

import (
	"fmt"
	"strings"

	"github.com/Liam0205/pineapple/internal/types"
)

var dotColors = map[types.OperatorType]string{
	types.OpTypeRecall:    "#E8F5E9",
	types.OpTypeTransform: "#E3F2FD",
	types.OpTypeFilter:    "#FFF3E0",
	types.OpTypeMerge:     "#F3E5F5",
	types.OpTypeReorder:   "#FFFDE7",
	types.OpTypeObserve:   "#F5F5F5",
}

var mermaidClasses = map[types.OperatorType][2]string{
	types.OpTypeRecall:    {"#E8F5E9", "#4CAF50"},
	types.OpTypeTransform: {"#E3F2FD", "#2196F3"},
	types.OpTypeFilter:    {"#FFF3E0", "#FF9800"},
	types.OpTypeMerge:     {"#F3E5F5", "#9C27B0"},
	types.OpTypeReorder:   {"#FFFDE7", "#FFC107"},
	types.OpTypeObserve:   {"#F5F5F5", "#9E9E9E"},
}

// reducedEdges returns the transitive reduction of the DAG — the minimal
// edge set that preserves the same reachability relation. For each edge
// u→v, if v is reachable from u via another path, the edge is redundant.
func reducedEdges(g *Graph) [][2]int {
	n := len(g.Nodes)

	// Build adjacency list (excluding self-loops, which addEdge already prevents).
	adj := make([][]int, n)
	for i, node := range g.Nodes {
		adj[i] = node.Succs
	}

	// For each edge u→v, check if v is reachable from u without the direct edge.
	// We do BFS from u, skipping the direct u→v edge.
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
			continue // skip the direct edge
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

// RenderDOT renders the DAG as a Graphviz DOT string.
// Edges are transitively reduced for readability.
func RenderDOT(g *Graph) string {
	var b strings.Builder
	b.WriteString("digraph pipeline {\n")
	b.WriteString("    rankdir=LR;\n")
	b.WriteString("    node [shape=box, style=filled, fontname=\"Helvetica\"];\n\n")

	for _, node := range g.Nodes {
		opType := types.OperatorType(node.Config.OperatorType)
		color := dotColors[opType]
		if color == "" {
			color = "#FFFFFF"
		}
		label := fmt.Sprintf("%s\\n[%s]", node.Name, opType)
		fmt.Fprintf(&b, "    %q [label=%q, fillcolor=%q];\n", node.Name, label, color)
	}

	b.WriteString("\n")

	for _, edge := range reducedEdges(g) {
		fmt.Fprintf(&b, "    %q -> %q;\n", g.Nodes[edge[0]].Name, g.Nodes[edge[1]].Name)
	}

	b.WriteString("}\n")
	return b.String()
}

// RenderMermaid renders the DAG as a Mermaid flowchart string.
// Edges are transitively reduced for readability.
func RenderMermaid(g *Graph) string {
	var b strings.Builder
	b.WriteString("graph LR\n")

	for _, node := range g.Nodes {
		opType := types.OperatorType(node.Config.OperatorType)
		className := strings.ToLower(string(opType))
		id := sanitizeMermaidID(node.Name)
		label := fmt.Sprintf("%s<br/>[%s]", node.Name, opType)
		fmt.Fprintf(&b, "    %s[\"%s\"]:::%s\n", id, label, className)
	}

	b.WriteString("\n")

	for _, edge := range reducedEdges(g) {
		fromID := sanitizeMermaidID(g.Nodes[edge[0]].Name)
		toID := sanitizeMermaidID(g.Nodes[edge[1]].Name)
		fmt.Fprintf(&b, "    %s --> %s\n", fromID, toID)
	}

	b.WriteString("\n")

	for opType, colors := range mermaidClasses {
		className := strings.ToLower(string(opType))
		fmt.Fprintf(&b, "    classDef %s fill:%s,stroke:%s\n", className, colors[0], colors[1])
	}

	return b.String()
}

func sanitizeMermaidID(name string) string {
	return strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(name)
}
