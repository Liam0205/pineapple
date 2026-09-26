package page.liam.pine;

import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;

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
     * hang or overflow the stack: the scan gives up at the depth bound. Two
     * self-keys make an unbounded scan exponential, so this also pins that
     * hitting the bound aborts the whole scan.
     */
    @Test
    void selfReferencingMapTerminates() {
        Map<String, Object> m = new HashMap<>();
        m.put("a", m);
        m.put("b", m);
        assertNull(FrameValues.checkValue("f", m));
    }
}
