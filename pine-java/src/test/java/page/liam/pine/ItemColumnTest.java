package page.liam.pine;

import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Tests for OperatorInput.itemColumn: element i must be identical to
 * item(i, field) across both storage modes, materialized mode, defaults
 * substitution and absent fields. Mirrors pine-go's item_column_test.go.
 */
class ItemColumnTest {

    private static List<Map<String, Object>> sampleItems() {
        List<Map<String, Object>> items = new ArrayList<>();
        Map<String, Object> r0 = new LinkedHashMap<>();
        r0.put("a", 1.0);
        r0.put("b", "x");
        items.add(r0);
        Map<String, Object> r1 = new LinkedHashMap<>();
        r1.put("a", 2.0); // b missing
        items.add(r1);
        Map<String, Object> r2 = new LinkedHashMap<>();
        r2.put("a", null);
        r2.put("b", "z");
        items.add(r2);
        return items;
    }

    private static InputFieldSpec spec(List<String> nullableItem,
                                       List<InputFieldSpec.DefaultedField> defaultedItem) {
        return new InputFieldSpec(
                new ArrayList<>(), new ArrayList<>(), new ArrayList<>(),
                new ArrayList<>(), defaultedItem, nullableItem);
    }

    private static void forBothModes(FrameConsumer consumer) throws Exception {
        for (String mode : new String[]{"row", "column"}) {
            consumer.accept(mode, Frame.create(mode, new HashMap<>(), sampleItems()));
        }
    }

    interface FrameConsumer {
        void accept(String mode, Frame frame) throws Exception;
    }

    @Test
    void itemColumnMatchesItem() throws Exception {
        forBothModes((mode, frame) -> {
            List<String> nullable = new ArrayList<>();
            nullable.add("a");
            OperatorInput in = frame.buildInput("op", spec(nullable, new ArrayList<>()));
            for (String field : new String[]{"a", "b", "absent"}) {
                Object[] col = in.itemColumn(field);
                assertEquals(in.itemCount(), col.length, mode + ": field " + field);
                for (int i = 0; i < col.length; i++) {
                    assertEquals(in.item(i, field), col[i],
                            mode + ": field " + field + " item " + i);
                }
            }
        });
    }

    @Test
    void itemColumnAppliesDefaults() throws Exception {
        List<Map<String, Object>> items = new ArrayList<>();
        Map<String, Object> r0 = new LinkedHashMap<>();
        r0.put("score", 1.0);
        items.add(r0);
        Map<String, Object> r1 = new LinkedHashMap<>();
        r1.put("score", null);
        items.add(r1);
        items.add(new LinkedHashMap<>()); // missing entirely

        for (String mode : new String[]{"row", "column"}) {
            Frame frame = Frame.create(mode, new HashMap<>(), items);
            List<InputFieldSpec.DefaultedField> defaulted = new ArrayList<>();
            defaulted.add(new InputFieldSpec.DefaultedField("score", -1.0));
            OperatorInput in = frame.buildInput("op", spec(new ArrayList<>(), defaulted));
            Object[] col = in.itemColumn("score");
            Object[] want = new Object[]{1.0, -1.0, -1.0};
            for (int i = 0; i < want.length; i++) {
                assertEquals(want[i], col[i], mode + ": item " + i);
                assertEquals(want[i], in.item(i, "score"), mode + ": item() " + i);
            }
        }
    }

    @Test
    void itemColumnMaterializedMode() {
        List<Map<String, Object>> items = new ArrayList<>();
        Map<String, Object> r0 = new LinkedHashMap<>();
        r0.put("a", 1.0);
        items.add(r0);
        Map<String, Object> r1 = new LinkedHashMap<>();
        r1.put("a", 2.0);
        items.add(r1);
        OperatorInput in = new OperatorInput(null, items);
        Object[] col = in.itemColumn("a");
        assertEquals(2, col.length);
        assertEquals(1.0, col[0]);
        assertEquals(2.0, col[1]);
    }

    @Test
    void itemColumnViewWindow() {
        List<Map<String, Object>> items = new ArrayList<>();
        for (int i = 0; i < 10; i++) {
            Map<String, Object> row = new LinkedHashMap<>();
            row.put("v", (double) i);
            items.add(row);
        }
        for (String mode : new String[]{"row", "column"}) {
            Frame frame = Frame.create(mode, new HashMap<>(), items);
            Object[] col = frame.itemColumnView("v", 3, 4);
            assertNotNull(col, mode);
            assertEquals(4, col.length, mode);
            for (int i = 0; i < 4; i++) {
                assertEquals((double) (3 + i), col[i], mode + ": slot " + i);
            }
            assertNull(frame.itemColumnView("v", 8, 4), mode + ": out-of-range window");
            assertNull(frame.itemColumnView("v", -1, 2), mode + ": negative offset");
        }
    }

    @Test
    void itemColumnAbsentField() throws Exception {
        forBothModes((mode, frame) -> {
            OperatorInput in = frame.buildInput("op", spec(new ArrayList<>(), new ArrayList<>()));
            Object[] col = in.itemColumn("nope");
            assertEquals(in.itemCount(), col.length, mode);
            for (Object v : col) {
                assertNull(v, mode);
            }
        });
    }
}
