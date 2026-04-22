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
		label := fmt.Sprintf("%s\\n[%s]", node.Name, opType)
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
		label := fmt.Sprintf("%s<br/>[%s]", node.Name, opType)
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
