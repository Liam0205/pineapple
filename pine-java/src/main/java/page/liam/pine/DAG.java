package page.liam.pine;

import java.util.*;

public class DAG {
    private static final String ROW_SET_SENTINEL = "_row_set_";

    public final List<Node> nodes;
    public final Map<String, Integer> nameToIndex;

    private DAG(List<Node> nodes, Map<String, Integer> nameToIndex) {
        this.nodes = nodes;
        this.nameToIndex = nameToIndex;
    }

    public static DAG build(List<String> sequence, Map<String, Config.OperatorConfig> operators,
                            Map<String, String> opToSubFlow) throws Config.ConfigException {
        List<Node> nodes = new ArrayList<>(sequence.size());
        Map<String, Integer> nameToIndex = new LinkedHashMap<>();

        for (int i = 0; i < sequence.size(); i++) {
            String name = sequence.get(i);
            Config.OperatorConfig opCfg = operators.get(name);
            if (opCfg == null) {
                throw new Config.ConfigException("operator \"" + name + "\" not found");
            }
            Node node = new Node(name, i, opToSubFlow.getOrDefault(name, ""), opCfg);
            nodes.add(node);
            nameToIndex.put(name, i);
        }

        DAG g = new DAG(nodes, nameToIndex);

        addEdges(g, sequence, operators, true);  // common fields
        addEdges(g, sequence, operators, false); // item fields

        // Explicit sources edges
        for (int i = 0; i < sequence.size(); i++) {
            Config.OperatorConfig opCfg = operators.get(sequence.get(i));
            for (String src : opCfg.sources) {
                Integer srcIdx = nameToIndex.get(src);
                if (srcIdx == null) {
                    throw new Config.ConfigException("operator \"" + sequence.get(i) + "\" sources references unknown operator \"" + src + "\"");
                }
                addEdge(g, srcIdx, i);
            }
        }

        // Validate no cycles (must run before reduce — transitive reduction
        // on a cyclic graph can mask the cycle by removing "redundant" edges).
        topologicalSort(g);

        reduce(g);

        return g;
    }

    public List<Integer> topologicalOrder() throws Config.ConfigException {
        return topologicalSort(this);
    }

    private static void addEdges(DAG g, List<String> sequence, Map<String, Config.OperatorConfig> operators, boolean isCommon) {
        Map<String, FieldTracker> fields = new HashMap<>();

        for (int i = 0; i < sequence.size(); i++) {
            Config.OperatorConfig opCfg = operators.get(sequence.get(i));
            Config.Metadata meta = opCfg.metadata;

            List<String> readFields = new ArrayList<>(isCommon ? meta.commonInput : meta.itemInput);
            List<String> writeFields = new ArrayList<>(isCommon ? meta.commonOutput : meta.itemOutput);
            boolean isAdditiveWrite = !isCommon && opCfg.additiveWritesRowSet;

            if (!isCommon) {
                if (isAdditiveWrite) {
                    writeFields.add(ROW_SET_SENTINEL);
                }
                if (opCfg.consumesRowSet) {
                    readFields.add(ROW_SET_SENTINEL);
                }
                if (!opCfg.consumesRowSet && !isAdditiveWrite) {
                    if (!readFields.isEmpty() || !writeFields.isEmpty()) {
                        readFields.add(ROW_SET_SENTINEL);
                    }
                }
            }

            // Process reads — RAW
            for (String field : readFields) {
                FieldTracker ft = fields.computeIfAbsent(field, k -> new FieldTracker());
                if (ft.lastMutWriter >= 0) {
                    addEdge(g, ft.lastMutWriter, i);
                }
                for (int aw : ft.additiveWriters) {
                    addEdge(g, aw, i);
                }
                ft.activeReaders.add(i);
            }

            // Process writes
            for (String field : writeFields) {
                FieldTracker ft = fields.computeIfAbsent(field, k -> new FieldTracker());
                if (isAdditiveWrite) {
                    if (ft.lastMutWriter >= 0) {
                        addEdge(g, ft.lastMutWriter, i);
                    }
                    for (int reader : ft.activeReaders) {
                        if (reader != i) {
                            addEdge(g, reader, i);
                        }
                    }
                    ft.additiveWriters.add(i);
                } else {
                    if (ft.lastMutWriter >= 0) {
                        addEdge(g, ft.lastMutWriter, i);
                    }
                    for (int aw : ft.additiveWriters) {
                        addEdge(g, aw, i);
                    }
                    for (int reader : ft.activeReaders) {
                        if (reader != i) {
                            addEdge(g, reader, i);
                        }
                    }
                    ft.lastMutWriter = i;
                    ft.additiveWriters.clear();
                    ft.activeReaders.clear();
                }
            }

            // MutatesRowSet: mutating write to _row_set_ sentinel
            if (!isCommon && opCfg.mutatesRowSet) {
                FieldTracker ft = fields.computeIfAbsent(ROW_SET_SENTINEL, k -> new FieldTracker());
                if (ft.lastMutWriter >= 0) {
                    addEdge(g, ft.lastMutWriter, i);
                }
                for (int aw : ft.additiveWriters) {
                    addEdge(g, aw, i);
                }
                for (int reader : ft.activeReaders) {
                    if (reader != i) {
                        addEdge(g, reader, i);
                    }
                }
                ft.lastMutWriter = i;
                ft.additiveWriters.clear();
                ft.activeReaders.clear();
            }
        }
    }

    private static void addEdge(DAG g, int from, int to) {
        if (from == to) return;
        Node fromNode = g.nodes.get(from);
        if (fromNode.succs.contains(to)) return;
        fromNode.succs.add(to);
        g.nodes.get(to).preds.add(from);
    }

    private static List<Integer> topologicalSort(DAG g) throws Config.ConfigException {
        int n = g.nodes.size();
        int[] inDegree = new int[n];
        for (Node node : g.nodes) {
            inDegree[node.index] = node.preds.size();
        }

        Deque<Integer> queue = new ArrayDeque<>();
        for (int i = 0; i < n; i++) {
            if (inDegree[i] == 0) queue.add(i);
        }

        List<Integer> order = new ArrayList<>(n);
        while (!queue.isEmpty()) {
            int curr = queue.poll();
            order.add(curr);
            for (int succ : g.nodes.get(curr).succs) {
                inDegree[succ]--;
                if (inDegree[succ] == 0) queue.add(succ);
            }
        }

        if (order.size() != n) {
            List<String> cycleNodes = new ArrayList<>();
            for (int i = 0; i < n; i++) {
                if (inDegree[i] > 0) cycleNodes.add(g.nodes.get(i).name);
            }
            throw new Config.ConfigException("DAG contains a cycle involving operators: " + cycleNodes);
        }
        return order;
    }

    private static void reduce(DAG g) {
        int n = g.nodes.size();
        List<int[]> kept = new ArrayList<>();

        for (int u = 0; u < n; u++) {
            for (int v : g.nodes.get(u).succs) {
                if (!reachableWithout(g, u, v)) {
                    kept.add(new int[]{u, v});
                }
            }
        }

        for (Node node : g.nodes) {
            node.preds.clear();
            node.succs.clear();
        }
        for (int[] e : kept) {
            g.nodes.get(e[0]).succs.add(e[1]);
            g.nodes.get(e[1]).preds.add(e[0]);
        }
    }

    private static boolean reachableWithout(DAG g, int src, int dst) {
        int n = g.nodes.size();
        boolean[] visited = new boolean[n];
        visited[src] = true;
        Deque<Integer> queue = new ArrayDeque<>();

        for (int next : g.nodes.get(src).succs) {
            if (next == dst) continue;
            if (!visited[next]) {
                visited[next] = true;
                queue.add(next);
            }
        }

        while (!queue.isEmpty()) {
            int cur = queue.poll();
            if (cur == dst) return true;
            for (int next : g.nodes.get(cur).succs) {
                if (!visited[next]) {
                    visited[next] = true;
                    queue.add(next);
                }
            }
        }
        return false;
    }

    // --- Inner types ---

    public static class Node {
        public final String name;
        public final int index;
        public final String subFlow;
        public final Config.OperatorConfig config;
        public final List<Integer> preds = new ArrayList<>();
        public final List<Integer> succs = new ArrayList<>();

        public Node(String name, int index, String subFlow, Config.OperatorConfig config) {
            this.name = name;
            this.index = index;
            this.subFlow = subFlow;
            this.config = config;
        }
    }

    private static class FieldTracker {
        int lastMutWriter = -1;
        List<Integer> additiveWriters = new ArrayList<>();
        List<Integer> activeReaders = new ArrayList<>();
    }
}
