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

// RenderDOT renders the DAG as a Graphviz DOT string.
func RenderDOT(g *Graph) string {
	var b strings.Builder
	b.WriteString("digraph pipeline {\n")
	b.WriteString("    rankdir=TB;\n")
	b.WriteString("    node [shape=box, style=filled, fontname=\"Helvetica\"];\n\n")

	for _, node := range g.Nodes {
		opType := types.OperatorType(node.Config.OperatorType)
		color := dotColors[opType]
		if color == "" {
			color = "#FFFFFF"
		}
		label := node.Name
		fmt.Fprintf(&b, "    %q [label=%q, fillcolor=%q];\n", node.Name, label, color)
	}

	b.WriteString("\n")

	for _, node := range g.Nodes {
		for _, succ := range node.Succs {
			fmt.Fprintf(&b, "    %q -> %q;\n", node.Name, g.Nodes[succ].Name)
		}
	}

	b.WriteString("}\n")
	return b.String()
}

// RenderMermaid renders the DAG as a Mermaid flowchart string.
func RenderMermaid(g *Graph) string {
	var b strings.Builder
	b.WriteString("graph TB\n")

	for _, node := range g.Nodes {
		opType := types.OperatorType(node.Config.OperatorType)
		className := strings.ToLower(string(opType))
		id := sanitizeMermaidID(node.Name)
		label := node.Name
		fmt.Fprintf(&b, "    %s[\"%s\"]:::%s\n", id, label, className)
	}

	b.WriteString("\n")

	for _, node := range g.Nodes {
		fromID := sanitizeMermaidID(node.Name)
		for _, succ := range node.Succs {
			toID := sanitizeMermaidID(g.Nodes[succ].Name)
			fmt.Fprintf(&b, "    %s --> %s\n", fromID, toID)
		}
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

// collapsedGraph computes the aggregated view for SubFlow-level rendering.
// Nodes with the same non-empty SubFlow are grouped into a single aggregate
// node. Nodes with empty SubFlow remain independent. Cross-group edges are
// deduped.
type collapsedNode struct {
	Name  string // SubFlow name or original node name
	Group bool   // true if this represents a SubFlow group
}

type collapsedEdge struct {
	From, To int
}

func buildCollapsed(g *Graph) ([]collapsedNode, []collapsedEdge) {
	nodeToGroup := make(map[int]int)
	var groups []collapsedNode
	groupIndex := make(map[string]int) // key → group index

	for _, node := range g.Nodes {
		key := node.SubFlow
		if key == "" {
			key = "\x00" + node.Name // unique per standalone node
		}
		if idx, ok := groupIndex[key]; ok {
			nodeToGroup[node.Index] = idx
		} else {
			idx := len(groups)
			if node.SubFlow != "" {
				groups = append(groups, collapsedNode{Name: node.SubFlow, Group: true})
			} else {
				groups = append(groups, collapsedNode{Name: node.Name, Group: false})
			}
			groupIndex[key] = idx
			nodeToGroup[node.Index] = idx
		}
	}

	edgeSet := make(map[[2]int]bool)
	var edges []collapsedEdge
	for _, node := range g.Nodes {
		fromG := nodeToGroup[node.Index]
		for _, succ := range node.Succs {
			toG := nodeToGroup[succ]
			if fromG == toG {
				continue // internal edge within same group
			}
			key := [2]int{fromG, toG}
			if !edgeSet[key] {
				edgeSet[key] = true
				edges = append(edges, collapsedEdge{From: fromG, To: toG})
			}
		}
	}

	return groups, edges
}

// RenderCollapsedDOT renders the DAG with SubFlow nodes collapsed into
// single aggregate nodes.
func RenderCollapsedDOT(g *Graph) string {
	groups, edges := buildCollapsed(g)
	var b strings.Builder
	b.WriteString("digraph pipeline {\n")
	b.WriteString("    rankdir=TB;\n")
	b.WriteString("    node [shape=box, style=filled, fontname=\"Helvetica\"];\n\n")

	for i, group := range groups {
		color := "#E0E0E0"
		if group.Group {
			color = "#BBDEFB"
		}
		fmt.Fprintf(&b, "    %q [label=%q, fillcolor=%q];\n",
			fmt.Sprintf("g%d", i), group.Name, color)
	}

	b.WriteString("\n")

	for _, e := range edges {
		fmt.Fprintf(&b, "    %q -> %q;\n",
			fmt.Sprintf("g%d", e.From), fmt.Sprintf("g%d", e.To))
	}

	b.WriteString("}\n")
	return b.String()
}

// RenderCollapsedMermaid renders the DAG with SubFlow nodes collapsed into
// single aggregate nodes.
func RenderCollapsedMermaid(g *Graph) string {
	groups, edges := buildCollapsed(g)
	var b strings.Builder
	b.WriteString("graph TB\n")

	for i, group := range groups {
		id := fmt.Sprintf("g%d", i)
		cls := "standalone"
		if group.Group {
			cls = "subflow"
		}
		fmt.Fprintf(&b, "    %s[\"%s\"]:::%s\n", id, group.Name, cls)
	}

	b.WriteString("\n")

	for _, e := range edges {
		fmt.Fprintf(&b, "    g%d --> g%d\n", e.From, e.To)
	}

	b.WriteString("\n")
	b.WriteString("    classDef subflow fill:#BBDEFB,stroke:#1976D2\n")
	b.WriteString("    classDef standalone fill:#E0E0E0,stroke:#616161\n")

	return b.String()
}
