package page.liam.pine;

import com.code_intelligence.jazzer.junit.FuzzTest;

import java.util.*;

public class JazzerFuzzTest {

    @FuzzTest(maxDuration = "30s")
    public void fuzzConfigLoad(byte[] data) {
        if (data.length > 64 * 1024) return;

        Config cfg;
        try {
            cfg = Config.load(data);
        } catch (Exception e) {
            return;
        }

        if (cfg.pipelineConfig != null && cfg.pipelineConfig.operators != null) {
            for (Map.Entry<String, Config.OperatorConfig> entry : cfg.pipelineConfig.operators.entrySet()) {
                Config.OperatorConfig opCfg = entry.getValue();
                if (opCfg.rawParams != null) {
                    for (String key : opCfg.rawParams.keySet()) {
                        if (isReservedKey(key)) {
                            throw new AssertionError("operator \"" + entry.getKey()
                                    + "\" rawParams contains reserved key \"" + key + "\"");
                        }
                    }
                }
            }
        }

        try {
            List<String> sequence = cfg.expandOperatorSequence();
            if (cfg.pipelineConfig != null && cfg.pipelineConfig.operators != null) {
                for (String opName : sequence) {
                    if (!cfg.pipelineConfig.operators.containsKey(opName)) {
                        throw new AssertionError("expanded sequence references missing operator \"" + opName + "\"");
                    }
                }
            }
        } catch (Exception e) {
            // expansion failure is acceptable
        }
    }

    @FuzzTest(maxDuration = "30s")
    public void fuzzDAGBuild(byte[] data) {
        if (data.length > 256) return;

        int[] cursor = {0};
        int n = (nextByte(data, cursor, (byte) 2) & 0xFF) % 8 + 1;

        List<String> sequence = new ArrayList<>(n);
        Map<String, Config.OperatorConfig> operators = new LinkedHashMap<>();

        for (int i = 0; i < n; i++) {
            String name = "op_" + i;
            sequence.add(name);
            operators.put(name, fuzzOperatorConfig(data, cursor, n));
        }

        DAG dag;
        try {
            dag = DAG.build(sequence, operators, Collections.emptyMap());
        } catch (Config.ConfigException e) {
            return;
        }

        if (dag.nodes.size() != n) {
            throw new AssertionError("expected " + n + " nodes, got " + dag.nodes.size());
        }

        try {
            List<Integer> order = dag.topologicalOrder();
            if (order.size() != n) {
                throw new AssertionError("topological order size mismatch");
            }
            Set<Integer> seen = new HashSet<>();
            for (int idx : order) {
                DAG.Node node = dag.nodes.get(idx);
                for (int pred : node.preds) {
                    if (!seen.contains(pred)) {
                        throw new AssertionError("topological order violation: "
                                + idx + " before predecessor " + pred);
                    }
                }
                seen.add(idx);
            }
        } catch (Config.ConfigException e) {
            throw new AssertionError("DAG.build succeeded but topologicalOrder failed: " + e.getMessage());
        }

        // Row-set safety invariant
        boolean[][] closure = transitiveClosureForFuzz(dag);
        int lastMutWriter = -1;
        List<Integer> additiveWriters = new ArrayList<>();

        for (int i = 0; i < n; i++) {
            Config.OperatorConfig opCfg = operators.get(sequence.get(i));
            boolean hasItemFields = (opCfg.metadata.itemInput != null && !opCfg.metadata.itemInput.isEmpty())
                    || (opCfg.metadata.itemOutput != null && !opCfg.metadata.itemOutput.isEmpty());
            boolean readsRowSet = opCfg.consumesRowSet || (hasItemFields && !opCfg.additiveWritesRowSet);

            if (readsRowSet) {
                if (lastMutWriter >= 0 && !closure[lastMutWriter][i]) {
                    throw new AssertionError("row-set safety: " + sequence.get(i)
                            + " reads _row_set_ but unreachable from MutatesRowSet " + sequence.get(lastMutWriter));
                }
                for (int aw : additiveWriters) {
                    if (!closure[aw][i]) {
                        throw new AssertionError("row-set safety: " + sequence.get(i)
                                + " reads _row_set_ but unreachable from AdditiveWritesRowSet " + sequence.get(aw));
                    }
                }
            }

            if (opCfg.additiveWritesRowSet) additiveWriters.add(i);
            if (opCfg.mutatesRowSet) {
                lastMutWriter = i;
                additiveWriters.clear();
            }
        }
    }

    @FuzzTest(maxDuration = "30s")
    public void fuzzEngineCreate(byte[] data) {
        if (data.length > 64 * 1024) return;

        try {
            Engine engine = Engine.create(data);
            engine.renderDAG("dot", 0);
            engine.renderDAG("mermaid", 0);
        } catch (Exception e) {
            // any exception during create is acceptable
        }
    }

    // --- Helpers ---

    private static boolean[][] transitiveClosureForFuzz(DAG dag) {
        int n = dag.nodes.size();
        boolean[][] reach = new boolean[n][n];
        for (int i = 0; i < n; i++) {
            boolean[] visited = new boolean[n];
            visited[i] = true;
            Deque<Integer> queue = new ArrayDeque<>();
            for (int s : dag.nodes.get(i).succs) {
                if (!visited[s]) {
                    visited[s] = true;
                    reach[i][s] = true;
                    queue.add(s);
                }
            }
            while (!queue.isEmpty()) {
                int cur = queue.poll();
                for (int s : dag.nodes.get(cur).succs) {
                    if (!visited[s]) {
                        visited[s] = true;
                        reach[i][s] = true;
                        queue.add(s);
                    }
                }
            }
        }
        return reach;
    }

    private static final Set<String> RESERVED = new HashSet<>(Arrays.asList(
            "type_name", "$metadata", "$code_info", "skip", "recall", "sources",
            "debug", "consumes_row_set", "mutates_row_set", "additive_writes_row_set",
            "common_defaults", "item_defaults", "for_branch_control", "data_parallel"
    ));

    private static boolean isReservedKey(String key) {
        return RESERVED.contains(key);
    }

    private static byte nextByte(byte[] data, int[] cursor, byte defaultValue) {
        if (cursor[0] >= data.length) return defaultValue;
        return data[cursor[0]++];
    }

    private static String fieldName(byte b) {
        return "f" + ((b & 0xFF) % 8);
    }

    private static List<String> fuzzFields(byte[] data, int[] cursor, int max) {
        int count = (nextByte(data, cursor, (byte) 0) & 0xFF) % (max + 1);
        Set<String> seen = new LinkedHashSet<>();
        for (int attempts = 0; seen.size() < count && attempts < count + 8; attempts++) {
            seen.add(fieldName(nextByte(data, cursor, (byte) attempts)));
        }
        return new ArrayList<>(seen);
    }

    private static Config.OperatorConfig fuzzOperatorConfig(byte[] data, int[] cursor, int n) {
        Config.OperatorConfig cfg = new Config.OperatorConfig();
        cfg.sources = Collections.emptyList();
        cfg.rawParams = Collections.emptyMap();

        int variant = (nextByte(data, cursor, (byte) 0) & 0xFF) % 6;
        switch (variant) {
            case 0:
                cfg.typeName = "recall_static";
                cfg.recall = true;
                cfg.additiveWritesRowSet = true;
                cfg.metadata = new Config.Metadata();
                cfg.metadata.itemOutput = fuzzFields(data, cursor, 4);
                break;
            case 1:
                cfg.typeName = "transform_copy";
                cfg.metadata = new Config.Metadata();
                cfg.metadata.itemInput = fuzzFields(data, cursor, 4);
                cfg.metadata.itemOutput = fuzzFields(data, cursor, 4);
                break;
            case 2:
                cfg.typeName = "filter_truncate";
                cfg.consumesRowSet = true;
                cfg.mutatesRowSet = true;
                cfg.metadata = new Config.Metadata();
                cfg.metadata.itemInput = fuzzFields(data, cursor, 4);
                break;
            case 3:
                cfg.typeName = "reorder_sort";
                cfg.consumesRowSet = true;
                cfg.mutatesRowSet = true;
                cfg.metadata = new Config.Metadata();
                cfg.metadata.itemInput = fuzzFields(data, cursor, 4);
                break;
            case 4:
                cfg.typeName = "transform_copy";
                cfg.metadata = new Config.Metadata();
                cfg.metadata.commonInput = fuzzFields(data, cursor, 4);
                cfg.metadata.commonOutput = fuzzFields(data, cursor, 4);
                break;
            default:
                cfg.typeName = "transform_copy";
                cfg.metadata = new Config.Metadata();
                cfg.metadata.itemInput = fuzzFields(data, cursor, 4);
                int srcCount = (nextByte(data, cursor, (byte) 0) & 0xFF) % 3;
                List<String> sources = new ArrayList<>();
                for (int s = 0; s < srcCount; s++) {
                    int srcIdx = (nextByte(data, cursor, (byte) 0) & 0xFF) % n;
                    sources.add("op_" + srcIdx);
                }
                cfg.sources = sources;
                break;
        }

        if (cfg.metadata == null) {
            cfg.metadata = new Config.Metadata();
        }
        if (cfg.metadata.commonInput == null) cfg.metadata.commonInput = Collections.emptyList();
        if (cfg.metadata.commonOutput == null) cfg.metadata.commonOutput = Collections.emptyList();
        if (cfg.metadata.itemInput == null) cfg.metadata.itemInput = Collections.emptyList();
        if (cfg.metadata.itemOutput == null) cfg.metadata.itemOutput = Collections.emptyList();

        return cfg;
    }
}
