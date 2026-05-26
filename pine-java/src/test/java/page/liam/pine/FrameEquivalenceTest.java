package page.liam.pine;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.DisplayName;

import java.util.*;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Dual-impl Frame equivalence.
 *
 * <p>DataFrame (row-major) and ColumnFrame must produce byte-identical
 * Result for the same (common, items, OperatorOutput) input. Without
 * this test, the two physical impls can drift in any of the five
 * apply_output stages (common write / item write / remove / reorder /
 * additions) and only end-to-end fixture diffs would catch it.
 *
 * <p>Mirrors pine-go FuzzApplyOutputStorageEquivalence,
 * pine-python test_frame_equivalence, and pine-cpp test_frame_equivalence.
 */
public class FrameEquivalenceTest {

    private static Map<String, Object> copyCommon(Map<String, Object> src) {
        return new LinkedHashMap<>(src);
    }

    private static List<Map<String, Object>> copyItems(List<Map<String, Object>> src) {
        List<Map<String, Object>> out = new ArrayList<>(src.size());
        for (Map<String, Object> r : src) out.add(new LinkedHashMap<>(r));
        return out;
    }

    private static void applyBoth(Frame row, Frame col, OperatorOutput out,
                                   String opName, boolean recall) {
        row.applyOutput(out, opName, recall);
        col.applyOutput(out, opName, recall);
    }

    private static void assertResultsEqual(Frame row, Frame col,
                                            List<String> commonOut,
                                            List<String> itemOut,
                                            String label) {
        assertEquals(row.toResultCommon(commonOut), col.toResultCommon(commonOut),
                label + " — common mismatch");
        assertEquals(row.toResultItems(itemOut), col.toResultItems(itemOut),
                label + " — items mismatch");
    }

    @Test
    @DisplayName("Initial-state projection equivalence")
    void initialState() {
        Map<String, Object> common = new LinkedHashMap<>();
        common.put("region", "us");
        List<Map<String, Object>> items = new ArrayList<>();
        items.add(new LinkedHashMap<>(Map.of("id", 1, "score", 10)));
        items.add(new LinkedHashMap<>(Map.of("id", 2, "score", 20)));
        Frame row = new DataFrame(copyCommon(common), copyItems(items));
        Frame col = new ColumnFrame(copyCommon(common), copyItems(items));
        assertEquals(row.itemCount(), col.itemCount());
        assertResultsEqual(row, col, List.of("region"), List.of("id", "score"), "initial");
    }

    @Test
    @DisplayName("Common writes equivalence")
    void commonWrites() {
        Frame row = new DataFrame(new LinkedHashMap<>(Map.of("region", "us")),
                                   new ArrayList<>(List.of(new LinkedHashMap<>(Map.of("id", 1)))));
        Frame col = new ColumnFrame(new LinkedHashMap<>(Map.of("region", "us")),
                                     new ArrayList<>(List.of(new LinkedHashMap<>(Map.of("id", 1)))));
        OperatorOutput out = new OperatorOutput();
        out.setCommon("region", "eu");
        out.setCommon("ts", 1234L);
        applyBoth(row, col, out, "op", false);
        assertResultsEqual(row, col, List.of("region", "ts"), List.of("id"), "common_writes");
    }

    @Test
    @DisplayName("Item writes equivalence")
    void itemWrites() {
        List<Map<String, Object>> items = new ArrayList<>();
        items.add(new LinkedHashMap<>(Map.of("id", 1, "score", 10)));
        items.add(new LinkedHashMap<>(Map.of("id", 2, "score", 20)));
        Frame row = new DataFrame(new LinkedHashMap<>(), copyItems(items));
        Frame col = new ColumnFrame(new LinkedHashMap<>(), copyItems(items));
        OperatorOutput out = new OperatorOutput();
        out.setItem(0, "score", 99);
        out.setItem(1, "bonus", true);
        applyBoth(row, col, out, "op", false);
        assertResultsEqual(row, col, List.of(), List.of("id", "score", "bonus"), "item_writes");
    }

    @Test
    @DisplayName("Remove equivalence")
    void remove() {
        List<Map<String, Object>> items = new ArrayList<>();
        for (int i = 0; i < 5; i++) items.add(new LinkedHashMap<>(Map.of("id", i)));
        Frame row = new DataFrame(new LinkedHashMap<>(), copyItems(items));
        Frame col = new ColumnFrame(new LinkedHashMap<>(), copyItems(items));
        OperatorOutput out = new OperatorOutput();
        out.removeItem(1);
        out.removeItem(3);
        applyBoth(row, col, out, "op", false);
        assertEquals(row.itemCount(), col.itemCount());
        assertEquals(3, row.itemCount());
        assertResultsEqual(row, col, List.of(), List.of("id"), "remove");
    }

    @Test
    @DisplayName("Reorder equivalence")
    void reorder() {
        List<Map<String, Object>> items = new ArrayList<>();
        for (int i = 0; i < 3; i++) items.add(new LinkedHashMap<>(Map.of("id", i)));
        Frame row = new DataFrame(new LinkedHashMap<>(), copyItems(items));
        Frame col = new ColumnFrame(new LinkedHashMap<>(), copyItems(items));
        OperatorOutput out = new OperatorOutput();
        out.setItemOrder(List.of(2, 0, 1));
        applyBoth(row, col, out, "op", false);
        assertResultsEqual(row, col, List.of(), List.of("id"), "reorder");
    }

    @Test
    @DisplayName("Additions + recall _source stamp equivalence")
    void additionsWithRecall() {
        List<Map<String, Object>> items = new ArrayList<>();
        items.add(new LinkedHashMap<>(Map.of("id", 0)));
        Frame row = new DataFrame(new LinkedHashMap<>(), copyItems(items));
        Frame col = new ColumnFrame(new LinkedHashMap<>(), copyItems(items));
        OperatorOutput out = new OperatorOutput();
        out.addItem(new LinkedHashMap<>(Map.of("id", 100, "name", "added-one")));
        out.addItem(new LinkedHashMap<>(Map.of("id", 200)));
        applyBoth(row, col, out, "op_recall", /*recall=*/true);
        assertEquals(3, row.itemCount());
        assertEquals(3, col.itemCount());
        assertResultsEqual(row, col, List.of(),
                List.of("id", "name", "_source"), "additions_recall");
    }

    @Test
    @DisplayName("Five-stage ordering equivalence")
    void fiveStageOrdering() {
        List<Map<String, Object>> items = new ArrayList<>();
        for (int i = 0; i < 4; i++) {
            Map<String, Object> r = new LinkedHashMap<>();
            r.put("id", i);
            r.put("score", i * 10);
            items.add(r);
        }
        Frame row = new DataFrame(new LinkedHashMap<>(Map.of("src", "v")), copyItems(items));
        Frame col = new ColumnFrame(new LinkedHashMap<>(Map.of("src", "v")), copyItems(items));
        OperatorOutput out = new OperatorOutput();
        out.setCommon("src", "w");
        out.setItem(0, "score", -1);
        out.removeItem(2);
        // after remove → 3 items; reorder needs len-3 permutation
        out.setItemOrder(List.of(2, 0, 1));
        out.addItem(new LinkedHashMap<>(Map.of("id", 99)));
        applyBoth(row, col, out, "op", false);
        assertResultsEqual(row, col, List.of("src"), List.of("id", "score"),
                "five_stage_ordering");
    }

    @Test
    @DisplayName("NaN/Inf rejection error-message equivalence")
    void nanInfErrorEquivalence() {
        Frame row = new DataFrame(new LinkedHashMap<>(),
                                   new ArrayList<>(List.of(new LinkedHashMap<>(Map.of("id", 1)))));
        Frame col = new ColumnFrame(new LinkedHashMap<>(),
                                     new ArrayList<>(List.of(new LinkedHashMap<>(Map.of("id", 1)))));
        OperatorOutput out = new OperatorOutput();
        out.setCommon("ratio", Double.NaN);
        String rowErr = null, colErr = null;
        try {
            row.applyOutput(out, "op", false);
        } catch (Exception e) {
            rowErr = e.getMessage();
        }
        try {
            col.applyOutput(out, "op", false);
        } catch (Exception e) {
            colErr = e.getMessage();
        }
        assertNotNull(rowErr);
        assertNotNull(colErr);
        assertEquals(rowErr, colErr);
    }

    @Test
    @DisplayName("Reorder duplicate-index error equivalence")
    void reorderDuplicateErrorEquivalence() {
        List<Map<String, Object>> items = new ArrayList<>();
        for (int i = 0; i < 3; i++) items.add(new LinkedHashMap<>(Map.of("id", i)));
        Frame row = new DataFrame(new LinkedHashMap<>(), copyItems(items));
        Frame col = new ColumnFrame(new LinkedHashMap<>(), copyItems(items));
        OperatorOutput out = new OperatorOutput();
        out.setItemOrder(List.of(0, 0, 0));
        String rowErr = null, colErr = null;
        try {
            row.applyOutput(out, "op", false);
        } catch (Exception e) {
            rowErr = e.getMessage();
        }
        try {
            col.applyOutput(out, "op", false);
        } catch (Exception e) {
            colErr = e.getMessage();
        }
        assertNotNull(rowErr);
        assertEquals(rowErr, colErr);
        assertTrue(rowErr.contains("duplicate"));
    }

    // ---- Differential rounds ----

    private static Object randValue(Random rng) {
        return switch (rng.nextInt(8)) {
            case 0 -> rng.nextInt(100);
            case 1 -> rng.nextDouble() * 1000.0;
            case 2 -> "s" + rng.nextInt(10);
            case 3 -> Boolean.TRUE;
            case 4 -> Boolean.FALSE;
            case 5 -> null;  // PRESENT-NULL
            case 6 -> List.of(1, 2);
            default -> Map.of("k", "v");
        };
    }

    private static OperatorOutput randOutput(Random rng, int nItems) {
        OperatorOutput out = new OperatorOutput();
        int nCw = rng.nextInt(4);
        for (int i = 0; i < nCw; i++) {
            out.setCommon("k" + rng.nextInt(5), randValue(rng));
        }
        if (nItems > 0) {
            int nIw = rng.nextInt(nItems * 2 + 1);
            for (int i = 0; i < nIw; i++) {
                int idx = rng.nextInt(nItems);
                out.setItem(idx, "f" + rng.nextInt(5), randValue(rng));
            }
            int nRm = rng.nextInt(nItems / 2 + 1);
            for (int i = 0; i < nRm; i++) {
                out.removeItem(rng.nextInt(nItems));
            }
        }
        int nAd = rng.nextInt(3);
        for (int i = 0; i < nAd; i++) {
            Map<String, Object> rowMap = new LinkedHashMap<>();
            int nF = 1 + rng.nextInt(3);
            for (int j = 0; j < nF; j++) {
                rowMap.put("f" + rng.nextInt(5), randValue(rng));
            }
            out.addItem(rowMap);
        }
        return out;
    }

    @Test
    @DisplayName("Differential fuzz: 50 seeded random rounds")
    void differentialFuzz() {
        for (int seed = 0; seed < 50; seed++) {
            Random rng = new Random(seed);
            int nItems = rng.nextInt(7);
            Map<String, Object> common = new LinkedHashMap<>();
            int nCommon = rng.nextInt(4);
            for (int i = 0; i < nCommon; i++) {
                common.put("c" + i, randValue(rng));
            }
            List<Map<String, Object>> items = new ArrayList<>(nItems);
            for (int i = 0; i < nItems; i++) {
                Map<String, Object> r = new LinkedHashMap<>();
                int nF = 1 + rng.nextInt(4);
                for (int j = 0; j < nF; j++) {
                    r.put("f" + j, randValue(rng));
                }
                items.add(r);
            }

            Frame row = new DataFrame(copyCommon(common), copyItems(items));
            Frame col = new ColumnFrame(copyCommon(common), copyItems(items));
            OperatorOutput out = randOutput(rng, nItems);

            String rowErr = null, colErr = null;
            try {
                row.applyOutput(out, "op", false);
            } catch (Exception e) {
                rowErr = e.getMessage();
            }
            try {
                col.applyOutput(out, "op", false);
            } catch (Exception e) {
                colErr = e.getMessage();
            }

            assertEquals(rowErr == null, colErr == null,
                    "seed=" + seed + " errs: row=" + rowErr + " col=" + colErr);
            if (rowErr != null) {
                String rowPre = rowErr.contains(":") ? rowErr.substring(0, rowErr.indexOf(':')) : rowErr;
                String colPre = colErr.contains(":") ? colErr.substring(0, colErr.indexOf(':')) : colErr;
                assertEquals(rowPre, colPre,
                        "seed=" + seed + " divergent error class: row=" + rowErr + " col=" + colErr);
                continue;
            }
            List<String> commonKeys = new ArrayList<>();
            for (int i = 0; i < 5; i++) commonKeys.add("k" + i);
            commonKeys.addAll(common.keySet());
            List<String> itemKeys = new ArrayList<>();
            for (int i = 0; i < 5; i++) itemKeys.add("f" + i);
            itemKeys.add("_source");
            assertEquals(row.toResultCommon(commonKeys), col.toResultCommon(commonKeys),
                    "seed=" + seed + " — common mismatch");
            assertEquals(row.toResultItems(itemKeys), col.toResultItems(itemKeys),
                    "seed=" + seed + " — items mismatch");
        }
    }
}
