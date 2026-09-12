package page.liam.pine;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;

/**
 * Tests for {@link OutputContract#validateDeclaredOutputs}, the write-side
 * half of operator honesty (issue #205): every field an operator writes must
 * appear in its {@code $metadata} output lists, or the hazard graph carries no
 * edge for it. Mirrors pine-go {@code types.ValidateDeclaredOutputs} tests and
 * the pine-cpp {@code test_engine.cpp} cases so the runtimes enforce the same
 * matrix with byte-identical messages.
 *
 * <p>Covers all four write paths — setCommon / setItem / setItemColumnDouble /
 * addItem — on both the violating and the compliant side, plus the engine-level
 * wrapping that produces the full user-visible message.
 */
class OutputContractTest {
    private static final ObjectMapper MAPPER = new ObjectMapper();

    // ---- unit level: the four write paths -------------------------------

    @Test
    void setCommonUndeclaredIsRejected() {
        OperatorOutput out = new OperatorOutput();
        out.setCommon("undeclared_common", 1.0);
        assertEquals("operator wrote undeclared common output field(s) [undeclared_common]",
                OutputContract.validateDeclaredOutputs(out, List.of("declared"), List.of()));
    }

    @Test
    void setCommonDeclaredIsAccepted() {
        OperatorOutput out = new OperatorOutput();
        out.setCommon("declared", 1.0);
        assertNull(OutputContract.validateDeclaredOutputs(out, List.of("declared"), List.of()));
    }

    @Test
    void setItemUndeclaredIsRejected() {
        OperatorOutput out = new OperatorOutput();
        out.setItem(0, "undeclared_item", 1.0);
        assertEquals("operator wrote undeclared item output field(s) [undeclared_item]",
                OutputContract.validateDeclaredOutputs(out, List.of(), List.of("declared")));
    }

    @Test
    void setItemDeclaredIsAccepted() {
        OperatorOutput out = new OperatorOutput();
        out.setItem(0, "declared", 1.0);
        assertNull(OutputContract.validateDeclaredOutputs(out, List.of(), List.of("declared")));
    }

    @Test
    void columnWriteUndeclaredIsRejected() {
        OperatorOutput out = new OperatorOutput();
        out.setItemColumnDouble("undeclared_col", new double[]{1.0, 2.0});
        assertEquals("operator wrote undeclared item output field(s) [undeclared_col]",
                OutputContract.validateDeclaredOutputs(out, List.of(), List.of("declared")));
    }

    @Test
    void columnWriteDeclaredIsAccepted() {
        OperatorOutput out = new OperatorOutput();
        out.setItemColumnDouble("declared", new double[]{1.0, 2.0});
        assertNull(OutputContract.validateDeclaredOutputs(out, List.of(), List.of("declared")));
    }

    @Test
    void addItemUndeclaredIsRejected() {
        OperatorOutput out = new OperatorOutput();
        out.addItem(Map.of("undeclared_added", 1.0));
        assertEquals("operator wrote undeclared item output field(s) [undeclared_added]",
                OutputContract.validateDeclaredOutputs(out, List.of(), List.of("declared")));
    }

    @Test
    void addItemDeclaredIsAccepted() {
        OperatorOutput out = new OperatorOutput();
        out.addItem(Map.of("declared", 1.0));
        assertNull(OutputContract.validateDeclaredOutputs(out, List.of(), List.of("declared")));
    }

    // ---- message shape ---------------------------------------------------

    @Test
    void commonChannelIsReportedAloneAndFirst() {
        // Both channels violate; Go returns on the common one without looking
        // at item writes, so the item field must not appear in the message.
        OperatorOutput out = new OperatorOutput();
        out.setCommon("bad_common", 1.0);
        out.setItem(0, "bad_item", 2.0);
        assertEquals("operator wrote undeclared common output field(s) [bad_common]",
                OutputContract.validateDeclaredOutputs(out, List.of(), List.of()));
    }

    @Test
    void itemFieldsAreDedupedAcrossWritePathsAndSorted() {
        // Same name reaching the check from three different paths must appear
        // once; the list is Go's `%v` of a sorted []string.
        OperatorOutput out = new OperatorOutput();
        out.setItem(0, "b", 1.0);
        out.setItem(1, "b", 1.0);
        out.setItemColumnDouble("b", new double[]{1.0, 2.0});
        out.addItem(Map.of("b", 1.0));
        out.setItem(0, "a", 1.0);
        out.addItem(Map.of("c", 1.0));
        assertEquals("operator wrote undeclared item output field(s) [a b c]",
                OutputContract.validateDeclaredOutputs(out, List.of(), List.of()));
    }

    @Test
    void sortIsByUtf8BytesNotUtf16CodeUnits() {
        // U+FFFD (EF BF BD) vs U+10000 (F0 90 80 80): UTF-8 byte order puts
        // U+FFFD first, UTF-16 code-unit order (String.compareTo) puts the
        // surrogate pair first. Go sorts bytes, so U+FFFD must come first.
        String replacement = "�";
        String astral = "𐀀";
        OperatorOutput out = new OperatorOutput();
        out.setItem(0, astral, 1.0);
        out.setItem(0, replacement, 1.0);
        assertEquals("operator wrote undeclared item output field(s) [" + replacement + " " + astral + "]",
                OutputContract.validateDeclaredOutputs(out, List.of(), List.of()));
    }

    @Test
    void emptyOutputIsClean() {
        assertNull(OutputContract.validateDeclaredOutputs(new OperatorOutput(), List.of(), List.of()));
    }

    // ---- engine level: full user-visible message -------------------------

    /** Recall that adds an item carrying a field the pipeline never declared. */
    public static final class UndeclaredAddingRecall implements Operator, AdditiveWritesRowSet {
        @Override
        public void init(OperatorParams params) {
        }

        @Override
        public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
            output.addItem(Map.of("undeclared_added", 1.0));
        }
    }

    private static boolean registered = false;

    private static synchronized void registerProbe() {
        if (registered) {
            return;
        }
        Registry.registerGlobal(
                new OperatorSchema("recall_undeclared_added_probe", OperatorType.RECALL,
                        "test-only recall that writes an undeclared item field", Map.of()),
                UndeclaredAddingRecall::new);
        registered = true;
    }

    private static byte[] probeConfig() throws Exception {
        Map<String, Object> meta = new LinkedHashMap<>();
        meta.put("common_input", List.of());
        meta.put("common_output", List.of());
        meta.put("item_input", List.of());
        meta.put("item_output", List.of("declared"));

        Map<String, Object> op = new LinkedHashMap<>();
        op.put("type_name", "recall_undeclared_added_probe");
        op.put("$metadata", meta);

        Map<String, Object> pipelineConfig = new LinkedHashMap<>();
        pipelineConfig.put("operators", Map.of("p_add", op));
        pipelineConfig.put("pipeline_map", Map.of("s", Map.of("pipeline", List.of("p_add"))));

        Map<String, Object> contract = new LinkedHashMap<>();
        contract.put("common_input", List.of());
        contract.put("common_output", List.of());
        contract.put("item_input", List.of());
        contract.put("item_output", List.of("declared"));

        Map<String, Object> config = new LinkedHashMap<>();
        config.put("pipeline_config", pipelineConfig);
        config.put("pipeline_group", Map.of("main", Map.of("pipeline", List.of("s"))));
        config.put("flow_contract", contract);
        return MAPPER.writeValueAsBytes(config);
    }

    @Test
    void engineReportsFullMessage() throws Exception {
        registerProbe();
        Engine engine = Engine.create(probeConfig());
        try {
            // pine-java surfaces operator failures on Result.error rather than
            // throwing (the accepted throw-vs-return split, issue #169); the
            // message itself is byte-identical to Go's.
            Engine.Result result = engine.execute(Map.of(), List.of());
            assertNotNull(result.error, "undeclared added field should fail the operator");
            assertEquals("pine: execution error in operator \"p_add\": output contract violation: "
                            + "operator wrote undeclared item output field(s) [undeclared_added]",
                    result.error.getMessage());
        } finally {
            engine.close();
        }
    }
}
