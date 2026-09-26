package page.liam.pine;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.Timeout;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Issue #210: NaN/±Inf nested inside a composite value used to pass the
 * write-time check (only scalars were inspected) and surfaced later as a
 * quoted "Infinity" in the response while pine-go failed to encode and
 * pine-cpp wrote bare inf. Composites are now scanned with the scalar
 * message. Mirrors pine-go internal/dataframe/nonfinite_composite_test.go.
 */
class NonFiniteCompositeTest {

    private static final String[] MODES = {"row", "column"};

    private static Map<String, Object> map(Object... kv) {
        Map<String, Object> m = new LinkedHashMap<>();
        for (int i = 0; i < kv.length; i += 2) {
            m.put((String) kv[i], kv[i + 1]);
        }
        return m;
    }

    private static List<Map<String, Object>> twoItems() {
        List<Map<String, Object>> out = new ArrayList<>();
        out.add(map("id", "a"));
        out.add(map("id", "b"));
        return out;
    }

    private static List<Object> composites() {
        List<Object> nested = new ArrayList<>();
        nested.add(map("x", new ArrayList<>(List.of(Double.NaN))));
        return List.of(
            new ArrayList<>(Arrays.asList(2.0, Double.POSITIVE_INFINITY)),
            map("a", 1.0, "b", Double.NEGATIVE_INFINITY),
            nested,
            new ArrayList<>(List.of(Float.POSITIVE_INFINITY)));
    }

    private static void assertRejected(String mode, Frame f, OperatorOutput out, boolean recall, String want) {
        Exception e = assertThrows(PineErrors.ExecutionError.class,
            () -> f.applyOutput(out, "op", recall), mode);
        assertEquals("pine: execution error in operator \"op\": " + want, e.getMessage(), mode);
    }

    @Test
    void rejectsNonFiniteInsideComposite() {
        for (String mode : MODES) {
            for (Object value : composites()) {
                Frame f = Frame.create(mode, new HashMap<>(), twoItems());

                OperatorOutput item = new OperatorOutput();
                item.setItem(1, "r", value);
                assertRejected(mode, f, item, false,
                    "item[1] write: field \"r\": NaN/Inf is not a valid JSON value");

                OperatorOutput common = new OperatorOutput();
                common.setCommon("c", value);
                assertRejected(mode, f, common, false,
                    "common write: field \"c\": NaN/Inf is not a valid JSON value");

                OperatorOutput added = new OperatorOutput();
                added.addItem(map("id", "c", "r", value));
                assertRejected(mode, f, added, true,
                    "added item write: field \"r\": NaN/Inf is not a valid JSON value");
            }
        }
    }

    @Test
    void acceptsFiniteComposite() throws Exception {
        for (String mode : MODES) {
            Frame f = Frame.create(mode, new HashMap<>(), new ArrayList<>(List.of(map("id", "a"))));
            OperatorOutput out = new OperatorOutput();
            out.setItem(0, "r", new ArrayList<>(Arrays.asList(1.0, "x", null, true, map("k", Double.MAX_VALUE))));
            f.applyOutput(out, "op", false);
        }
    }

    /**
     * A self-referencing map (only a custom operator can build one) must not
     * hang or overflow the stack. Two self-keys make a naive scan
     * exponential; the depth-aware seen set skips a composite already
     * scanned at the same or a shallower depth, while a NaN in a sibling is
     * still found.
     */
    @Test
    void selfReferencingMapTerminates() {
        Map<String, Object> m = new HashMap<>();
        m.put("a", m);
        m.put("b", m);
        assertNull(FrameValues.checkValue("f", m));
        m.put("bad", Double.NaN);
        assertEquals("field \"f\": NaN/Inf is not a valid JSON value", FrameValues.checkValue("f", m));
    }

    private static Object nest(Object leaf, int levels) {
        Object v = leaf;
        for (int i = 0; i < levels; i++) {
            List<Object> l = new ArrayList<>();
            l.add(v);
            v = l;
        }
        return v;
    }

    /** A too-deep sibling does not hide a shallow NaN/Inf, whatever the map order. */
    @Test
    void depthBoundDoesNotHideShallowSibling() {
        Object deep = nest(0.0, FrameValues.MAX_COMPOSITE_SCAN_DEPTH + 1);
        Map<String, Object> v = new HashMap<>();
        v.put("deep", deep);
        v.put("bad", Double.POSITIVE_INFINITY);
        assertEquals("field \"f\": NaN/Inf is not a valid JSON value", FrameValues.checkValue("f", v));
    }

    /** Same two boundary cases as pine-go and pine-cpp. */
    @Test
    void depthBoundary() {
        assertEquals("field \"f\": NaN/Inf is not a valid JSON value",
            FrameValues.checkValue("f", nest(Double.NaN, FrameValues.MAX_COMPOSITE_SCAN_DEPTH)));
        assertNull(FrameValues.checkValue("f", nest(Double.NaN, FrameValues.MAX_COMPOSITE_SCAN_DEPTH + 1)));
    }

    /**
     * A ring of k maps, each pointing twice at the next, has 2^k paths; a
     * per-path walk never finishes at k=40. Only a custom operator can build
     * it. Mirrors pine-go TestValidateValueSharedCyclicGraphIsLinear.
     */
    @Test
    @Timeout(value = 5, threadMode = Timeout.ThreadMode.SEPARATE_THREAD)
    void sharedCyclicGraphIsLinear() {
        int k = 40;
        List<Map<String, Object>> ms = new ArrayList<>();
        for (int i = 0; i < k; i++) {
            ms.add(new HashMap<>());
        }
        for (int i = 0; i < k; i++) {
            Map<String, Object> next = ms.get((i + 1) % k);
            ms.get(i).put("a", next);
            ms.get(i).put("b", next);
        }
        assertNull(FrameValues.checkValue("f", ms.get(0)));
        ms.get(k - 1).put("bad", Double.NaN);
        assertEquals("field \"f\": NaN/Inf is not a valid JSON value", FrameValues.checkValue("f", ms.get(0)));
    }

    /** A shared sub-value first reached deep and later shallow is rescanned from the shallower depth. */
    @Test
    void sharedSubvalueRescannedFromShallowerDepth() {
        List<Object> shared = new ArrayList<>();
        shared.add(nest(Double.NaN, 3));
        Object deepPath = nest(shared, FrameValues.MAX_COMPOSITE_SCAN_DEPTH - 2);
        List<Object> v = new ArrayList<>();
        v.add(deepPath);
        v.add(shared);
        assertEquals("field \"f\": NaN/Inf is not a valid JSON value", FrameValues.checkValue("f", v));
    }

    /**
     * A leaf list shared by many references is scanned once per depth, not
     * once per reference (20000 references to a 20000-element list would be
     * 4e8 element visits otherwise).
     */
    @Test
    @Timeout(value = 5, threadMode = Timeout.ThreadMode.SEPARATE_THREAD)
    void sharedLeafIsScannedOnce() {
        List<Object> leaf = new ArrayList<>();
        for (int i = 0; i < 20000; i++) {
            leaf.add((double) i);
        }
        List<Object> refs = new ArrayList<>();
        for (int i = 0; i < 20000; i++) {
            refs.add(leaf);
        }
        long start = System.nanoTime();
        assertNull(FrameValues.checkValue("f", refs));
        long ms = (System.nanoTime() - start) / 1_000_000;
        assertTrue(ms < 200, "shared leaf scan took " + ms + " ms");
    }
}
