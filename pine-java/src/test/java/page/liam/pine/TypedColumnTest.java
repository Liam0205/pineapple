package page.liam.pine;

import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Typed-column behavior tests: construction-time inference, promotion on
 * mixed writes / present-null, and the itemColumnDouble fast path.
 * Mirrors pine-go's typed_column_test.go.
 */
class TypedColumnTest {

    private static Map<String, Object> row(Object... kv) {
        Map<String, Object> m = new LinkedHashMap<>();
        for (int i = 0; i < kv.length; i += 2) {
            m.put((String) kv[i], kv[i + 1]);
        }
        return m;
    }

    @Test
    void itemSemanticsAcrossAbsentAndPresentNull() {
        List<Map<String, Object>> items = new ArrayList<>();
        items.add(row("f", 1.5));
        items.add(row()); // absent
        items.add(row("f", null)); // present-null → json fallback
        items.add(row("f", 2.5));
        ColumnFrame f = new ColumnFrame(new HashMap<>(), items);

        assertEquals(1.5, f.item(0, "f"));
        assertNull(f.item(1, "f"));
        assertNull(f.item(2, "f"));
        assertEquals(2.5, f.item(3, "f"));
    }

    @Test
    void promotionOnMixedTypeWrite() {
        List<Map<String, Object>> items = new ArrayList<>();
        items.add(row("f", 1.0));
        items.add(row("f", 2.0));
        ColumnFrame f = new ColumnFrame(new HashMap<>(), items);

        OperatorOutput out = new OperatorOutput();
        out.setItem(0, "f", "now-a-string");
        f.applyOutput(out, "op", false);

        assertEquals("now-a-string", f.item(0, "f"));
        assertEquals(2.0, f.item(1, "f"), "other slots preserved through promotion");
    }

    @Test
    void promotionOnNullWrite() {
        List<Map<String, Object>> items = new ArrayList<>();
        items.add(row("g", 1.0));
        ColumnFrame f = new ColumnFrame(new HashMap<>(), items);

        OperatorOutput out = new OperatorOutput();
        out.setItem(0, "g", null);
        f.applyOutput(out, "op", false);

        assertNull(f.item(0, "g"));
        // Present-null must survive projection presence semantics: the
        // field was explicitly written, so toResultItems includes it.
        List<Map<String, Object>> result = f.toResultItems(List.of("g"));
        assertTrue(result.get(0).containsKey("g"), "present-null slot must project");
        assertNull(result.get(0).get("g"));
    }

    @Test
    void doubleFastPath() throws Exception {
        List<Map<String, Object>> items = new ArrayList<>();
        items.add(row("f", 1.0, "s", "x"));
        items.add(row("f", 2.0, "s", "y"));
        ColumnFrame f = new ColumnFrame(new HashMap<>(), items);
        OperatorInput in = f.buildInput("op", new InputFieldSpec(
                new ArrayList<>(), new ArrayList<>(), new ArrayList<>(),
                new ArrayList<>(), new ArrayList<>(), new ArrayList<>()));

        double[] raw = in.itemColumnDouble("f");
        assertNotNull(raw, "fully-present double column should serve the fast path");
        assertArrayEquals(new double[]{1.0, 2.0}, raw);

        assertNull(in.itemColumnDouble("s"), "string column must not serve the fast path");
        assertNull(in.itemColumnDouble("absent"), "absent column must not serve the fast path");

        // Column with a hole → no fast path (defaults could apply).
        List<Map<String, Object>> items2 = new ArrayList<>();
        items2.add(row("f", 1.0));
        items2.add(row());
        ColumnFrame f2 = new ColumnFrame(new HashMap<>(), items2);
        OperatorInput in2 = f2.buildInput("op", new InputFieldSpec(
                new ArrayList<>(), new ArrayList<>(), new ArrayList<>(),
                new ArrayList<>(), new ArrayList<>(), new ArrayList<>()));
        assertNull(in2.itemColumnDouble("f"), "column with absent slot must not serve the fast path");

        // Row frame → no fast path.
        DataFrame df = new DataFrame(new HashMap<>(), items);
        OperatorInput inR = df.buildInput("op", new InputFieldSpec(
                new ArrayList<>(), new ArrayList<>(), new ArrayList<>(),
                new ArrayList<>(), new ArrayList<>(), new ArrayList<>()));
        assertNull(inR.itemColumnDouble("f"), "row frame must not serve the fast path");
    }

    @Test
    void resultParityWithRowStoreThroughFullWriteLog() {
        List<Map<String, Object>> mkItems = new ArrayList<>();
        mkItems.add(row("id", "a", "score", 3.0));
        mkItems.add(row("id", "b", "score", 1.0));
        mkItems.add(row("id", "c", "score", 2.0));

        Frame rowF = Frame.create("row", new HashMap<>(), deepCopy(mkItems));
        Frame colF = Frame.create("column", new HashMap<>(), deepCopy(mkItems));

        for (Frame f : new Frame[]{rowF, colF}) {
            OperatorOutput out = new OperatorOutput();
            out.setItem(0, "rank", 1.0);
            out.setItem(1, "rank", 2.0);
            out.setItem(2, "rank", 3.0);
            f.applyOutput(out, "op1", false);

            OperatorOutput out2 = new OperatorOutput();
            out2.removeItem(1);
            f.applyOutput(out2, "op2", false);

            OperatorOutput out3 = new OperatorOutput();
            out3.setItemOrder(List.of(1, 0));
            f.applyOutput(out3, "op3", false);

            OperatorOutput out4 = new OperatorOutput();
            out4.addItem(row("id", "d", "score", 9.0, "extra", true));
            f.applyOutput(out4, "op4", true);
        }

        List<String> fields = List.of("id", "score", "rank", "extra", "_source");
        assertEquals(rowF.toResultItems(fields).toString(), colF.toResultItems(fields).toString());
    }

    private static List<Map<String, Object>> deepCopy(List<Map<String, Object>> items) {
        List<Map<String, Object>> out = new ArrayList<>(items.size());
        for (Map<String, Object> item : items) {
            out.add(new LinkedHashMap<>(item));
        }
        return out;
    }
}
