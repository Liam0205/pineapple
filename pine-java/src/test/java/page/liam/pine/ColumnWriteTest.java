package page.liam.pine;

import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Batch column write (setItemColumnDouble) semantics: adopt on column
 * store, scatter on row store, length check, NaN validation parity,
 * ordering vs per-element writes. Mirrors pine-go's column_write_test.go.
 */
class ColumnWriteTest {

    private static Map<String, Object> row(Object... kv) {
        Map<String, Object> m = new LinkedHashMap<>();
        for (int i = 0; i < kv.length; i += 2) {
            m.put((String) kv[i], kv[i + 1]);
        }
        return m;
    }

    private static List<Map<String, Object>> items(Map<String, Object>... rows) {
        List<Map<String, Object>> out = new ArrayList<>();
        for (Map<String, Object> r : rows) {
            out.add(new LinkedHashMap<>(r));
        }
        return out;
    }

    private static final String[] MODES = {"row", "column"};

    @Test
    void bothModesWriteWholeColumn() throws Exception {
        for (String mode : MODES) {
            Frame f = Frame.create(mode, new HashMap<>(),
                items(row("id", "a"), row("id", "b"), row("id", "c")));
            OperatorOutput out = new OperatorOutput();
            out.setItemColumnDouble("score", new double[]{1.5, 2.5, 3.5});
            f.applyOutput(out, "op", false);

            double[] want = {1.5, 2.5, 3.5};
            for (int i = 0; i < want.length; i++) {
                assertEquals(want[i], f.item(i, "score"), mode + ": item " + i);
            }
            // Result projection includes the new field on every row.
            List<Map<String, Object>> result = f.toResultItems(List.of("id", "score"));
            for (int i = 0; i < result.size(); i++) {
                assertTrue(result.get(i).containsKey("score"),
                    mode + ": item " + i + " missing score in result");
            }
        }
    }

    @Test
    void lengthMismatchFails() {
        for (String mode : MODES) {
            Frame f = Frame.create(mode, new HashMap<>(), items(row("id", "a"), row("id", "b")));
            OperatorOutput out = new OperatorOutput();
            out.setItemColumnDouble("score", new double[]{1.0}); // wrong length
            Exception e = assertThrows(PineErrors.ExecutionError.class,
                () -> f.applyOutput(out, "op", false), mode);
            assertTrue(e.getMessage().contains("does not match item count 2"),
                mode + ": unexpected error: " + e.getMessage());
        }
    }

    @Test
    void nanValidationMessageParity() {
        for (String mode : MODES) {
            Frame f = Frame.create(mode, new HashMap<>(), items(row("id", "a"), row("id", "b")));
            OperatorOutput out = new OperatorOutput();
            out.setItemColumnDouble("score", new double[]{1.0, Double.NaN});
            Exception e = assertThrows(PineErrors.ExecutionError.class,
                () -> f.applyOutput(out, "op", false), mode);
            // Same first-error message shape as the per-element path.
            assertTrue(e.getMessage().contains(
                "item[1] write: field \"score\": NaN/Inf is not a valid JSON value"),
                mode + ": unexpected error: " + e.getMessage());
        }
    }

    @Test
    void columnWriteOverridesPerElement() throws Exception {
        for (String mode : MODES) {
            Frame f = Frame.create(mode, new HashMap<>(), items(row("id", "a"), row("id", "b")));
            OperatorOutput out = new OperatorOutput();
            out.setItem(0, "score", 99.0); // per-element first
            out.setItemColumnDouble("score", new double[]{1.0, 2.0});
            f.applyOutput(out, "op", false);
            // Column write applies after per-element → wins on collision.
            assertEquals(1.0, f.item(0, "score"), mode + ": column write must win");
        }
    }

    @Test
    void columnStoreAdoptsZeroCopy() throws Exception {
        // Column store must adopt the array (typed fast-path readable,
        // zero-copy) — pinned via the read view aliasing the written array.
        ColumnFrame f = new ColumnFrame(new HashMap<>(), items(row("id", "a"), row("id", "b")));
        double[] vals = {1.0, 2.0};
        OperatorOutput out = new OperatorOutput();
        out.setItemColumnDouble("score", vals);
        f.applyOutput(out, "op", false);

        double[] view = f.itemColumnDoubleView("score", 0, 2);
        assertNotNull(view, "adopted column must serve the typed read view");
        assertSame(vals, view, "expected zero-copy adoption (view aliases the written array)");
    }

    @Test
    void rowColumnResultParity() throws Exception {
        List<Map<String, Object>> src = items(
            row("id", "a", "old", 1.0), row("id", "b", "old", 2.0));
        Frame rowF = Frame.create("row", new HashMap<>(), items(row("id", "a", "old", 1.0), row("id", "b", "old", 2.0)));
        Frame colF = Frame.create("column", new HashMap<>(), src);
        for (Frame f : new Frame[]{rowF, colF}) {
            OperatorOutput out = new OperatorOutput();
            out.setItemColumnDouble("norm", new double[]{0.25, 0.75});
            f.applyOutput(out, "op", false);
        }
        List<String> fields = List.of("id", "old", "norm");
        assertEquals(rowF.toResultItems(fields).toString(), colF.toResultItems(fields).toString());
    }

    @Test
    void itemWriteMapFoldsColumnWrites() {
        OperatorOutput out = new OperatorOutput();
        out.setItem(0, "score", 99.0);
        out.setItem(1, "other", "x");
        out.setItemColumnDouble("score", new double[]{1.0, 2.0});
        Map<Integer, Map<String, Object>> m = out.itemWriteMap();
        assertEquals(1.0, m.get(0).get("score"), "column write overrides per-element in the view");
        assertEquals(2.0, m.get(1).get("score"));
        assertEquals("x", m.get(1).get("other"), "per-element writes on other fields preserved");
    }
}
