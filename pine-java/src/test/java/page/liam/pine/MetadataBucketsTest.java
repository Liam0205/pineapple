package page.liam.pine;

import org.junit.jupiter.api.Test;

import java.util.*;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Tests for the 3-bucket common_input split introduced for issue #74:
 * common_input (business) / common_input_skip / common_input_template.
 *
 * Mirrors {@code pine-go internal/config/types_test.go} and the
 * corresponding DAG cases so the runtimes stay in lockstep.
 */
public class MetadataBucketsTest {

    @Test
    void commonReadFields_noOptionalBuckets_returnsBusiness() {
        Config.Metadata m = new Config.Metadata();
        m.commonInput = List.of("a", "b");
        assertEquals(List.of("a", "b"), m.commonReadFields());
        // Identity short-circuit: nothing to union, same backing list.
        assertSame(m.commonInput, m.commonReadFields());
    }

    @Test
    void commonReadFields_unionAndDedup() {
        Config.Metadata m = new Config.Metadata();
        m.commonInput = List.of("uid");
        m.commonInputSkip = List.of("_if_branch", "uid"); // overlap
        m.commonInputTemplate = List.of("tenant_id", "uid"); // overlap
        assertEquals(List.of("uid", "_if_branch", "tenant_id"), m.commonReadFields());
    }

    @Test
    void inputFieldSpec_excludesSkipBucket() {
        Config.Metadata m = new Config.Metadata();
        m.commonInput = List.of("uid");
        m.commonInputSkip = List.of("_if_branch");
        InputFieldSpec spec = InputFieldSpec.compute(
                m, Map.of(), Map.of(), List.of(), List.of(), List.of());
        assertEquals(List.of("uid"), spec.nullableCommon);
    }

    @Test
    void inputFieldSpec_excludesTemplateBucket() {
        Config.Metadata m = new Config.Metadata();
        m.commonInput = List.of("uid");
        m.commonInputTemplate = List.of("tenant_id");
        InputFieldSpec spec = InputFieldSpec.compute(
                m, Map.of(), Map.of(), List.of(), List.of(), List.of());
        assertEquals(List.of("uid"), spec.nullableCommon);
    }

    @Test
    void inputFieldSpec_legacySkipArgStillFilters() {
        // Backward-compat path: skip field placed directly into common_input,
        // with the field name passed via the skip argument.
        Config.Metadata m = new Config.Metadata();
        m.commonInput = List.of("uid", "_if_branch");
        InputFieldSpec spec = InputFieldSpec.compute(
                m, Map.of(), Map.of(), List.of(), List.of(), List.of("_if_branch"));
        assertEquals(List.of("uid"), spec.nullableCommon);
    }

    @Test
    void dag_rawEdge_fromCommonInputSkipBucket() throws Exception {
        // op_a writes _if_branch, op_b declares it only in common_input_skip.
        // The DAG must still wire RAW edge a->b.
        List<String> seq = List.of("op_a", "op_b");
        Map<String, Config.OperatorConfig> ops = new LinkedHashMap<>();
        ops.put("op_a", txOp(null, List.of("_if_branch")));
        Config.OperatorConfig b = txOp(null, null);
        b.metadata.commonInputSkip = List.of("_if_branch");
        ops.put("op_b", b);

        DAG g = DAG.build(seq, ops, Collections.emptyMap());
        assertTrue(hasPred(g, "op_b", "op_a"),
                "expected RAW edge from common_input_skip bucket");
    }

    @Test
    void dag_rawEdge_fromCommonInputTemplateBucket() throws Exception {
        // op_a writes tenant_id, op_b declares it only in
        // common_input_template. The DAG must still wire RAW edge a->b.
        List<String> seq = List.of("op_a", "op_b");
        Map<String, Config.OperatorConfig> ops = new LinkedHashMap<>();
        ops.put("op_a", txOp(null, List.of("tenant_id")));
        Config.OperatorConfig b = txOp(List.of("uid"), null);
        b.metadata.commonInputTemplate = List.of("tenant_id");
        ops.put("op_b", b);

        DAG g = DAG.build(seq, ops, Collections.emptyMap());
        assertTrue(hasPred(g, "op_b", "op_a"),
                "expected RAW edge from common_input_template bucket");
    }

    private static Config.OperatorConfig txOp(List<String> commonIn, List<String> commonOut) {
        Config.OperatorConfig cfg = new Config.OperatorConfig();
        cfg.typeName = "test";
        cfg.operatorType = OperatorType.TRANSFORM.name().toLowerCase();
        cfg.metadata = new Config.Metadata();
        cfg.metadata.commonInput = commonIn != null ? commonIn : Collections.emptyList();
        cfg.metadata.commonOutput = commonOut != null ? commonOut : Collections.emptyList();
        cfg.metadata.itemInput = Collections.emptyList();
        cfg.metadata.itemOutput = Collections.emptyList();
        cfg.skip = Collections.emptyList();
        cfg.sources = Collections.emptyList();
        cfg.rawParams = Collections.emptyMap();
        return cfg;
    }

    private static boolean hasPred(DAG g, String name, String pred) {
        int idx = g.nameToIndex.get(name);
        int predIdx = g.nameToIndex.get(pred);
        for (int p : g.nodes.get(idx).preds) {
            if (p == predIdx) return true;
        }
        return false;
    }
}
