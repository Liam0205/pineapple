package page.liam.pine;

import java.util.*;

/**
 * DAGVisualizer renders a DAG in DOT (Graphviz) and Mermaid formats,
 * with optional collapsed SubFlow grouping.
 */
public class DAGVisualizer {

    // Color map for DOT format (matches Go implementation)
    private static final Map<OperatorType, String> DOT_COLORS;
    static {
        Map<OperatorType, String> m = new EnumMap<>(OperatorType.class);
        m.put(OperatorType.RECALL, "#E8F5E9");
        m.put(OperatorType.TRANSFORM, "#E3F2FD");
        m.put(OperatorType.FILTER, "#FFF3E0");
        m.put(OperatorType.MERGE, "#F3E5F5");
        m.put(OperatorType.REORDER, "#FFFDE7");
        m.put(OperatorType.OBSERVE, "#F5F5F5");
        DOT_COLORS = Collections.unmodifiableMap(m);
    }

    // Mermaid class map: [fill, stroke] (matches Go implementation)
    private static final Map<OperatorType, String[]> MERMAID_CLASSES;
    static {
        Map<OperatorType, String[]> m = new EnumMap<>(OperatorType.class);
        m.put(OperatorType.RECALL, new String[]{"#E8F5E9", "#4CAF50"});
        m.put(OperatorType.TRANSFORM, new String[]{"#E3F2FD", "#2196F3"});
        m.put(OperatorType.FILTER, new String[]{"#FFF3E0", "#FF9800"});
        m.put(OperatorType.MERGE, new String[]{"#F3E5F5", "#9C27B0"});
        m.put(OperatorType.REORDER, new String[]{"#FFFDE7", "#FFC107"});
        m.put(OperatorType.OBSERVE, new String[]{"#F5F5F5", "#9E9E9E"});
        MERMAID_CLASSES = Collections.unmodifiableMap(m);
    }

    private DAGVisualizer() {}

    /**
     * Renders the DAG as a Graphviz DOT string.
     */
    public static String renderDot(DAG dag) {
        StringBuilder b = new StringBuilder();
        b.append("digraph pipeline {\n");
        b.append("    rankdir=TB;\n");
        b.append("    node [shape=box, style=filled, fontname=\"Helvetica\"];\n\n");

        for (DAG.Node node : dag.nodes) {
            OperatorType opType = resolveType(node.config.operatorType);
            String color = DOT_COLORS.getOrDefault(opType, "#FFFFFF");
            b.append("    ")
             .append(quote(node.name))
             .append(" [label=").append(quote(node.name))
             .append(", fillcolor=").append(quote(color))
             .append("];\n");
        }

        b.append("\n");

        for (DAG.Node node : dag.nodes) {
            for (int succ : node.succs) {
                b.append("    ")
                 .append(quote(node.name))
                 .append(" -> ")
                 .append(quote(dag.nodes.get(succ).name))
                 .append(";\n");
            }
        }

        b.append("}\n");
        return b.toString();
    }

    /**
     * Renders the DAG as a Mermaid flowchart string.
     */
    public static String renderMermaid(DAG dag) {
        StringBuilder b = new StringBuilder();
        b.append("graph TB\n");

        for (DAG.Node node : dag.nodes) {
            OperatorType opType = resolveType(node.config.operatorType);
            String className = opType.name().toLowerCase();
            String id = sanitizeMermaidID(node.name);
            b.append("    ")
             .append(id)
             .append("[\"").append(node.name).append("\"]")
             .append(":::").append(className)
             .append("\n");
        }

        b.append("\n");

        for (DAG.Node node : dag.nodes) {
            String fromID = sanitizeMermaidID(node.name);
            for (int succ : node.succs) {
                String toID = sanitizeMermaidID(dag.nodes.get(succ).name);
                b.append("    ")
                 .append(fromID)
                 .append(" --> ")
                 .append(toID)
                 .append("\n");
            }
        }

        b.append("\n");

        for (OperatorType opType : OperatorType.values()) {
            String[] colors = MERMAID_CLASSES.get(opType);
            if (colors != null) {
                String className = opType.name().toLowerCase();
                b.append("    classDef ").append(className)
                 .append(" fill:").append(colors[0])
                 .append(",stroke:").append(colors[1])
                 .append("\n");
            }
        }

        return b.toString();
    }

    /**
     * Renders the DAG as a DOT string with SubFlow nodes collapsed at the specified level.
     * Level 0 = full detail (same as renderDot).
     */
    public static String renderCollapsedDot(DAG dag, int level) {
        if (level <= 0) {
            return renderDot(dag);
        }

        CollapsedGraph cg = buildCollapsed(dag, level);
        StringBuilder b = new StringBuilder();
        b.append("digraph pipeline {\n");
        b.append("    rankdir=TB;\n");
        b.append("    node [shape=box, style=filled, fontname=\"Helvetica\"];\n\n");

        for (int i = 0; i < cg.groups.size(); i++) {
            CollapsedNode group = cg.groups.get(i);
            String color = "#E0E0E0";
            if (group.isGroup) {
                color = "#BBDEFB";
            } else if (group.operatorType != null && !group.operatorType.isEmpty()) {
                OperatorType resolved = resolveType(group.operatorType);
                String typeColor = DOT_COLORS.get(resolved);
                if (typeColor != null) {
                    color = typeColor;
                }
            }
            String id = "g" + i;
            b.append("    ")
             .append(quote(id))
             .append(" [label=").append(quote(group.name))
             .append(", fillcolor=").append(quote(color))
             .append("];\n");
        }

        b.append("\n");

        for (CollapsedEdge e : cg.edges) {
            b.append("    ")
             .append(quote("g" + e.from))
             .append(" -> ")
             .append(quote("g" + e.to))
             .append(";\n");
        }

        b.append("}\n");
        return b.toString();
    }

    /**
     * Renders the DAG as a Mermaid string with SubFlow nodes collapsed at the specified level.
     * Level 0 = full detail (same as renderMermaid).
     */
    public static String renderCollapsedMermaid(DAG dag, int level) {
        if (level <= 0) {
            return renderMermaid(dag);
        }

        CollapsedGraph cg = buildCollapsed(dag, level);
        StringBuilder b = new StringBuilder();
        b.append("graph TB\n");

        for (int i = 0; i < cg.groups.size(); i++) {
            CollapsedNode group = cg.groups.get(i);
            String id = "g" + i;
            String cls = "standalone";
            if (group.isGroup) {
                cls = "subflow";
            } else if (group.operatorType != null && !group.operatorType.isEmpty()) {
                cls = group.operatorType;
            }
            b.append("    ")
             .append(id)
             .append("[\"").append(group.name).append("\"]")
             .append(":::").append(cls)
             .append("\n");
        }

        b.append("\n");

        for (CollapsedEdge e : cg.edges) {
            b.append("    g").append(e.from)
             .append(" --> g").append(e.to)
             .append("\n");
        }

        b.append("\n");
        b.append("    classDef subflow fill:#BBDEFB,stroke:#1976D2\n");
        b.append("    classDef standalone fill:#E0E0E0,stroke:#616161\n");
        for (OperatorType opType : OperatorType.values()) {
            String[] colors = MERMAID_CLASSES.get(opType);
            if (colors != null) {
                String className = opType.name().toLowerCase();
                b.append("    classDef ").append(className)
                 .append(" fill:").append(colors[0])
                 .append(",stroke:").append(colors[1])
                 .append("\n");
            }
        }

        return b.toString();
    }

    // --- Internal helpers ---

    /**
     * Sanitizes a name for use as a Mermaid node ID by replacing
     * non-alphanumeric/underscore characters with underscores.
     */
    static String sanitizeMermaidID(String name) {
        StringBuilder sb = new StringBuilder(name.length());
        for (int i = 0; i < name.length(); i++) {
            char c = name.charAt(i);
            if (Character.isLetterOrDigit(c) || c == '_') {
                sb.append(c);
            } else {
                sb.append('_');
            }
        }
        return sb.toString();
    }

    private static String quote(String s) {
        return "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"") + "\"";
    }

    private static OperatorType resolveType(String typeStr) {
        if (typeStr == null || typeStr.isEmpty()) {
            return OperatorType.TRANSFORM;
        }
        try {
            return OperatorType.valueOf(typeStr.toUpperCase());
        } catch (IllegalArgumentException e) {
            return OperatorType.TRANSFORM;
        }
    }

    /**
     * Returns the grouping key for a SubFlow path at a given level.
     * The SubFlow path is split by "/" and truncated to the first `level` segments.
     */
    static String collapseKey(String subFlow, int level) {
        if (subFlow == null || subFlow.isEmpty() || level <= 0) {
            return "";
        }
        String[] parts = subFlow.split("/");
        if (level >= parts.length) {
            return subFlow;
        }
        StringBuilder sb = new StringBuilder();
        for (int i = 0; i < level; i++) {
            if (i > 0) sb.append("/");
            sb.append(parts[i]);
        }
        return sb.toString();
    }

    private static CollapsedGraph buildCollapsed(DAG dag, int level) {
        Map<String, Integer> groupIndex = new LinkedHashMap<>();
        Map<Integer, Integer> nodeToGroup = new HashMap<>();
        List<CollapsedNode> groups = new ArrayList<>();

        for (DAG.Node node : dag.nodes) {
            String key = collapseKey(node.subFlow, level);
            if (key.isEmpty()) {
                // Standalone node: use unique key
                key = "\0" + node.name;
            }

            Integer idx = groupIndex.get(key);
            if (idx != null) {
                nodeToGroup.put(node.index, idx);
            } else {
                idx = groups.size();
                boolean isGroup = node.subFlow != null && !node.subFlow.isEmpty();
                String name = isGroup ? collapseKey(node.subFlow, level) : node.name;
                String opType = isGroup ? "" : (node.config != null ? node.config.operatorType : "");
                groups.add(new CollapsedNode(name, isGroup, opType));
                groupIndex.put(key, idx);
                nodeToGroup.put(node.index, idx);
            }
        }

        // Build deduplicated edges between groups
        Set<Long> edgeSet = new HashSet<>();
        List<CollapsedEdge> edges = new ArrayList<>();

        for (DAG.Node node : dag.nodes) {
            int fromG = nodeToGroup.get(node.index);
            for (int succ : node.succs) {
                int toG = nodeToGroup.get(succ);
                if (fromG == toG) {
                    continue; // internal edge within same group
                }
                long edgeKey = ((long) fromG << 32) | (toG & 0xFFFFFFFFL);
                if (edgeSet.add(edgeKey)) {
                    edges.add(new CollapsedEdge(fromG, toG));
                }
            }
        }

        return new CollapsedGraph(groups, edges);
    }

    // --- Internal data structures for collapsed rendering ---

    private static class CollapsedNode {
        final String name;
        final boolean isGroup;
        final String operatorType; // non-empty for standalone nodes (preserves type coloring)

        CollapsedNode(String name, boolean isGroup, String operatorType) {
            this.name = name;
            this.isGroup = isGroup;
            this.operatorType = operatorType;
        }
    }

    private static class CollapsedEdge {
        final int from;
        final int to;

        CollapsedEdge(int from, int to) {
            this.from = from;
            this.to = to;
        }
    }

    private static class CollapsedGraph {
        final List<CollapsedNode> groups;
        final List<CollapsedEdge> edges;

        CollapsedGraph(List<CollapsedNode> groups, List<CollapsedEdge> edges) {
            this.groups = groups;
            this.edges = edges;
        }
    }
}
