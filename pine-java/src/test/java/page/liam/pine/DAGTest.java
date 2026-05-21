package page.liam.pine;

import org.junit.jupiter.api.Test;

import java.util.*;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Unit tests for DAG.build — regression tests for bugs discovered by fuzz testing.
 */
public class DAGTest {

    /**
     * Regression: fuzz found that reduce() before cycle detection can mask cycles.
     * When sources create backwards edges forming a cycle, transitive reduction
     * may remove "redundant" cycle edges, causing topological sort to miss the cycle.
     */
    @Test
    void cycleMaskedByReduce() {
        List<String> seq = Arrays.asList("op_0", "op_1", "op_2", "op_3");
        Map<String, Config.OperatorConfig> ops = new LinkedHashMap<>();

        ops.put("op_0", makeOp(
                null, null, Arrays.asList("a"), null,
                false, false, false, false, Arrays.asList("op_3")));
        ops.put("op_1", makeOp(
                null, null, Arrays.asList("b"), null,
                true, true, false, false, null));
        ops.put("op_2", makeOp(
                null, null, Arrays.asList("a", "b"), null,
                false, false, false, false, Arrays.asList("op_0")));
        ops.put("op_3", makeOp(
                null, null, null, Arrays.asList("a"),
                false, false, true, true, null));

        Config.ConfigException ex = assertThrows(Config.ConfigException.class,
                () -> DAG.build(seq, ops, Collections.emptyMap()));
        assertTrue(ex.getMessage().contains("cycle"),
                "expected cycle error, got: " + ex.getMessage());
    }

    private static Config.OperatorConfig makeOp(
            List<String> commonIn, List<String> commonOut,
            List<String> itemIn, List<String> itemOut,
            boolean consumes, boolean mutates,
            boolean additive, boolean recall,
            List<String> sources) {
        Config.OperatorConfig cfg = new Config.OperatorConfig();
        cfg.typeName = "test";
        cfg.metadata = new Config.Metadata();
        cfg.metadata.commonInput = commonIn != null ? commonIn : Collections.emptyList();
        cfg.metadata.commonOutput = commonOut != null ? commonOut : Collections.emptyList();
        cfg.metadata.itemInput = itemIn != null ? itemIn : Collections.emptyList();
        cfg.metadata.itemOutput = itemOut != null ? itemOut : Collections.emptyList();
        cfg.consumesRowSet = consumes;
        cfg.mutatesRowSet = mutates;
        cfg.additiveWritesRowSet = additive;
        cfg.recall = recall;
        cfg.sources = sources != null ? sources : Collections.emptyList();
        cfg.rawParams = Collections.emptyMap();
        return cfg;
    }
}
