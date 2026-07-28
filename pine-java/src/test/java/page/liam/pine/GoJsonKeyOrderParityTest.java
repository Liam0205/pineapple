package page.liam.pine;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.fasterxml.jackson.databind.ObjectMapper;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

/**
 * Pins pine-java's JSON object key order to Go's, byte for byte (issue #183).
 *
 * <p>Two rules, and they are different rules: Go's encoding/json SORTS map keys
 * but leaves STRUCT fields in declaration order. pine-go's response envelope is
 * a struct while common/items payloads are maps, so the envelope keeps
 * declaration order and everything inside it sorts.
 *
 * <p>The sort is by UTF-8 bytes, not by String.compareTo. Those differ above the
 * BMP, so using Java's natural String ordering would have moved the divergence
 * from ASCII keys to emoji keys rather than removing it.
 */
class GoJsonKeyOrderParityTest {

    private static final ObjectMapper MAPPER = GoFormat.createGoCompatMapper();

    @Test
    @SuppressWarnings("unchecked")
    void payloadMapsSortTheirKeys() throws Exception {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("c10", "x");
        m.put("c2", "y");
        m.put("c1", "z");
        // Lexicographic, so c1 < c10 < c2 — not numeric, and not insertion order.
        assertEquals("{\"c1\":\"z\",\"c10\":\"x\",\"c2\":\"y\"}",
                MAPPER.writeValueAsString(GoFormat.sorted(m)));
    }

    @Test
    void sortIsByUtf8BytesNotUtf16CodeUnits() {
        // U+FFFD is 3 UTF-8 bytes starting ef; U+10000 is 4 starting f0, but in
        // UTF-16 it is a surrogate pair starting d800, which is BELOW fffd. So
        // the two orderings disagree, and Go follows UTF-8.
        String bmp = "�";
        String nonBmp = "𐀀";
        assertTrue(GoFormat.compareUtf8(bmp, nonBmp) < 0,
                "UTF-8 order must put U+FFFD before U+10000");
        assertTrue(bmp.compareTo(nonBmp) > 0,
                "String.compareTo disagrees — this is why it cannot be used");
        assertTrue(GoFormat.compareUtf8("z", bmp) < 0, "ASCII sorts before multi-byte");
    }

    @Test
    void nonBmpKeysMatchGoOutput() throws Exception {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("�", 1);
        m.put("𐀀", 2);
        m.put("z", 3);
        // Verified against Go: json.Marshal gives {"z":3,"�":1,"\U00010000":2}.
        assertEquals("{\"z\":3,\"�\":1,\"𐀀\":2}",
                MAPPER.writeValueAsString(GoFormat.sorted(m)));
    }

    @Test
    void sortAppliesAtEveryDepth() throws Exception {
        Map<String, Object> inner = new LinkedHashMap<>();
        inner.put("z", true);
        inner.put("a", false);
        Map<String, Object> outer = new LinkedHashMap<>();
        outer.put("y", inner);
        outer.put("x", "hi");
        // A fix that only sorted the top level would pass a flat check.
        assertEquals("{\"x\":\"hi\",\"y\":{\"a\":false,\"z\":true}}",
                MAPPER.writeValueAsString(GoFormat.sorted(outer)));
    }

    @Test
    void sortReachesMapsNestedInsideLists() throws Exception {
        Map<String, Object> item = new LinkedHashMap<>();
        item.put("b", 1);
        item.put("a", 2);
        List<Object> items = new ArrayList<>();
        items.add(item);
        assertEquals("[{\"a\":2,\"b\":1}]",
                MAPPER.writeValueAsString(GoFormat.wrapPayload(items)));
    }

    @Test
    void envelopeKeepsDeclarationOrderAndDoesNotSort() throws Exception {
        // Go's executeResponse is a struct: common, items, warnings, trace, error.
        // Sorting this would put "error" before "items" — which is exactly what
        // the first attempt at #183 did, breaking the partial-error fixture.
        Map<String, Object> envelope = new LinkedHashMap<>();
        envelope.put("common", GoFormat.sorted(new LinkedHashMap<>()));
        envelope.put("items", new ArrayList<>());
        envelope.put("error", "boom");
        String json = MAPPER.writeValueAsString(envelope);
        assertEquals("{\"common\":{},\"items\":[],\"error\":\"boom\"}", json);
        assertTrue(json.indexOf("\"items\"") < json.indexOf("\"error\""),
                "items must precede error, as in Go's struct declaration");
    }

    @Test
    void compareUtf8AgreesWithAsciiOrderingAndHandlesPrefixes() {
        assertTrue(GoFormat.compareUtf8("a", "b") < 0);
        assertTrue(GoFormat.compareUtf8("b", "a") > 0);
        assertEquals(0, GoFormat.compareUtf8("same", "same"));
        // A prefix sorts before the longer string under both encodings.
        assertTrue(GoFormat.compareUtf8("ab", "abc") < 0);
        assertTrue(GoFormat.compareUtf8("c1", "c10") < 0);
        assertTrue(GoFormat.compareUtf8("c10", "c2") < 0);
    }
}
