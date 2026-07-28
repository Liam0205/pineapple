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
    void traceSnapshotsSortWhileTheTraceEntryKeepsDeclarationOrder() throws Exception {
        // Go's traceEntry is a struct (name, duration_ms, skipped,
        // input_snapshot, output_snapshot) so the entry keeps declaration order,
        // while both snapshots are map[string]any and sort. Asserted here as a
        // unit test because the cross-validate channel that sees trace can only
        // pin output_snapshot: an input_snapshot only ever contains the
        // operator's declared common_input, so no injected request key reaches it.
        Map<String, Object> snapshotCommon = new LinkedHashMap<>();
        snapshotCommon.put("zz", 1);
        snapshotCommon.put("aa", 2);
        Map<String, Object> inputSnapshot = new LinkedHashMap<>();
        inputSnapshot.put("common", snapshotCommon);

        Map<String, Object> entry = new LinkedHashMap<>();
        entry.put("name", "op");
        entry.put("duration_ms", 0.5);
        entry.put("input_snapshot", GoFormat.wrapPayload(inputSnapshot));

        String json = MAPPER.writeValueAsString(entry);
        assertEquals("{\"name\":\"op\",\"duration_ms\":0.5,"
                + "\"input_snapshot\":{\"common\":{\"aa\":2,\"zz\":1}}}", json);
    }

    @Test
    void integerKeyedMapsSortAsStringsLikeGo() throws Exception {
        // output_snapshot.item_writes is map[int]map[string]any in Go, and
        // encoding/json renders int keys as strings then sorts those — so index
        // 10 lands between 1 and 2. wrapPayload must therefore stringify keys
        // rather than cast them, which would also throw on Integer keys.
        Map<Integer, Object> itemWrites = new LinkedHashMap<>();
        for (int i = 0; i < 12; i++) {
            itemWrites.put(i, i);
        }
        String json = MAPPER.writeValueAsString(GoFormat.wrapPayload(itemWrites));
        assertEquals("{\"0\":0,\"1\":1,\"10\":10,\"11\":11,\"2\":2,\"3\":3,"
                + "\"4\":4,\"5\":5,\"6\":6,\"7\":7,\"8\":8,\"9\":9}", json);
    }

    @Test
    void sortedShallowSortsOnlyItsOwnLevel() throws Exception {
        // For trees that mix Go's two rules, like /stats: the top level is a map
        // and sorts, but `scheduler` beneath it is a struct and must not.
        Map<String, Object> struct = new LinkedHashMap<>();
        struct.put("run_count", 1);
        struct.put("peak_concurrency", 2);
        Map<String, Object> top = new LinkedHashMap<>();
        top.put("server", struct);
        top.put("operators", struct);
        String json = MAPPER.writeValueAsString(GoFormat.sortedShallow(top));
        // Top level sorted (operators before server); inner order preserved.
        assertEquals("{\"operators\":{\"run_count\":1,\"peak_concurrency\":2},"
                + "\"server\":{\"run_count\":1,\"peak_concurrency\":2}}", json);
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
