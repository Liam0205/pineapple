package page.liam.pine;

import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

/**
 * Pins {@code storage_mode} dispatch to pine-go's rule (issue #179).
 *
 * <p>pine-go's {@code NewFrame} is a {@code switch} on a string-typed
 * {@code StorageMode} with {@code default: newRowFrame}, so exactly one value —
 * the literal {@code "column"} — reaches the column store and everything else
 * reaches the row store. All three runtimes must agree on that, casing included.
 *
 * <p>Before #179 they did not: this runtime matched with
 * {@code equalsIgnoreCase} so {@code "Column"} selected the column store, and
 * pine-cpp's factory fell back to column so a misspelling like {@code "colunm"}
 * did too. One hand-written config therefore selected a different physical store
 * in each runtime.
 *
 * <p>Row and column stores are output-equivalent by design — cross-validate
 * section 4 asserts that — so the divergence never changed a response byte, only
 * the memory and performance profile. That is precisely why it needs a unit test:
 * no end-to-end channel can observe which store was chosen.
 */
class StorageModeDispatchTest {

    private static List<Map<String, Object>> oneItem() {
        List<Map<String, Object>> items = new ArrayList<>();
        Map<String, Object> item = new HashMap<>();
        item.put("id", "a");
        items.add(item);
        return items;
    }

    @Test
    void exactLowercaseColumnSelectsTheColumnStore() {
        Frame f = Frame.create("column", new HashMap<>(), oneItem());
        assertInstanceOf(ColumnFrame.class, f, "exact \"column\" must select the column store");
        assertTrue(f.itemCount() == 1);
    }

    @Test
    void everythingElseSelectsTheRowStore() {
        // Includes the two values that diverged in #179: "Column" (this runtime
        // used equalsIgnoreCase) and "colunm" (pine-cpp fell back to column).
        String[] rowModes = {
            "row", "", "colunm", "Column", "COLUMN", "cOlUmN",
            "column ", " column", "columns", "col", "rows", "unknown",
        };
        for (String mode : rowModes) {
            Frame f = Frame.create(mode, new HashMap<>(), oneItem());
            assertInstanceOf(DataFrame.class, f,
                    "storage_mode \"" + mode + "\" must select the row store");
            assertTrue(f.itemCount() == 1);
        }
    }

    @Test
    void nullStorageModeSelectsTheRowStore() {
        // Reachable when a hand-written config omits the key entirely. Go's
        // switch on the zero value likewise falls to default.
        Frame f = Frame.create(null, new HashMap<>(), oneItem());
        assertInstanceOf(DataFrame.class, f, "absent storage_mode must select the row store");
    }
}
