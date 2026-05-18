package page.liam.pine;

import com.fasterxml.jackson.databind.ObjectMapper;
import page.liam.pine.operators.AllOperators;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.DynamicNode;
import org.junit.jupiter.api.DynamicTest;
import org.junit.jupiter.api.TestFactory;

import java.io.File;
import java.io.FilenameFilter;
import java.util.*;
import java.util.stream.Stream;

import static org.junit.jupiter.api.Assertions.*;
import static org.junit.jupiter.api.DynamicContainer.dynamicContainer;
import static org.junit.jupiter.api.DynamicTest.dynamicTest;

public class FixtureTest {
    private static final ObjectMapper mapper = new ObjectMapper();

    @BeforeAll
    static void setup() {
        AllOperators.ensureRegistered();
    }

    @TestFactory
    @SuppressWarnings("unchecked")
    Stream<DynamicNode> fixtureTests() throws Exception {
        File fixtureDir = new File("../fixtures");
        if (!fixtureDir.exists()) {
            return Stream.empty();
        }

        File[] files = fixtureDir.listFiles((dir, name) -> name.endsWith(".json"));
        if (files == null || files.length == 0) {
            return Stream.empty();
        }

        Arrays.sort(files);
        List<DynamicNode> nodes = new ArrayList<>();

        for (File file : files) {
            Map<String, Object> fixture = mapper.readValue(file, Map.class);
            String operatorName = (String) fixture.get("operator");
            List<Map<String, Object>> cases = (List<Map<String, Object>>) fixture.get("cases");

            // Skip operators not yet implemented in Java
            if (Registry.getType(operatorName) == null) {
                continue;
            }

            List<DynamicTest> tests = new ArrayList<>();
            for (Map<String, Object> tc : cases) {
                String caseName = (String) tc.get("name");
                tests.add(dynamicTest(caseName, () -> runCase(operatorName, tc)));
            }
            nodes.add(dynamicContainer(file.getName(), tests));
        }
        return nodes.stream();
    }

    @SuppressWarnings("unchecked")
    private void runCase(String operatorName, Map<String, Object> tc) throws Exception {
        Map<String, Object> params = (Map<String, Object>) tc.getOrDefault("params", Collections.emptyMap());
        Map<String, Object> metaMap = (Map<String, Object>) tc.getOrDefault("metadata", Collections.emptyMap());
        Map<String, Object> inputMap = (Map<String, Object>) tc.getOrDefault("input", Collections.emptyMap());
        Map<String, Object> expected = (Map<String, Object>) tc.getOrDefault("expected", Collections.emptyMap());

        // Build operator
        Operator op = Registry.buildOperator(operatorName, params);

        // Set metadata
        if (op instanceof MetadataAware) {
            ((MetadataAware) op).setMetadata(
                toStringList(metaMap.get("common_input")),
                toStringList(metaMap.get("common_output")),
                toStringList(metaMap.get("item_input")),
                toStringList(metaMap.get("item_output"))
            );
        }

        // Build input
        Map<String, Object> common = (Map<String, Object>) inputMap.getOrDefault("common", Collections.emptyMap());
        List<Map<String, Object>> items = (List<Map<String, Object>>) inputMap.getOrDefault("items", Collections.emptyList());
        OperatorInput input = new OperatorInput(common, items);

        // Execute
        OperatorOutput output = new OperatorOutput();
        op.execute(input, output);

        // Validate removed_indices
        if (expected.containsKey("removed_indices")) {
            List<Number> expectedRemoved = (List<Number>) expected.get("removed_indices");
            Set<Integer> removed = output.getRemovedItems();
            assertEquals(expectedRemoved.size(), removed.size(),
                "removed count mismatch");
            for (Number idx : expectedRemoved) {
                assertTrue(removed.contains(idx.intValue()),
                    "expected item[" + idx + "] to be removed");
            }
        }

        // Validate added_items
        if (expected.containsKey("added_items")) {
            List<Map<String, Object>> expectedAdded = (List<Map<String, Object>>) expected.get("added_items");
            List<Map<String, Object>> added = output.getAddedItems();
            assertEquals(expectedAdded.size(), added.size(), "added_items count mismatch");
            for (int i = 0; i < expectedAdded.size(); i++) {
                for (Map.Entry<String, Object> entry : expectedAdded.get(i).entrySet()) {
                    Object got = added.get(i).get(entry.getKey());
                    assertValueEquals(entry.getValue(), got,
                        "added_items[" + i + "]." + entry.getKey());
                }
            }
        }

        // Validate common writes
        if (expected.containsKey("common")) {
            Map<String, Object> expectedCommon = (Map<String, Object>) expected.get("common");
            Map<String, Object> cw = output.getCommonWrites();
            for (Map.Entry<String, Object> entry : expectedCommon.entrySet()) {
                assertTrue(cw.containsKey(entry.getKey()),
                    "common_writes missing key: " + entry.getKey());
                assertValueEquals(entry.getValue(), cw.get(entry.getKey()),
                    "common_writes[" + entry.getKey() + "]");
            }
        }

        // Validate item_order
        if (expected.containsKey("item_order")) {
            List<Number> expectedOrder = (List<Number>) expected.get("item_order");
            List<Integer> order = output.getItemOrder();
            assertNotNull(order, "item_order is null");
            assertEquals(expectedOrder.size(), order.size(), "item_order length mismatch");
            for (int i = 0; i < expectedOrder.size(); i++) {
                assertEquals(expectedOrder.get(i).intValue(), order.get(i).intValue(),
                    "item_order[" + i + "]");
            }
        }

        // Validate item writes
        if (expected.containsKey("items")) {
            List<Map<String, Object>> expectedItems = (List<Map<String, Object>>) expected.get("items");
            Map<Integer, Map<String, Object>> iw = output.getItemWrites();
            for (int i = 0; i < expectedItems.size(); i++) {
                Map<String, Object> expectedItem = expectedItems.get(i);
                if (expectedItem == null || expectedItem.isEmpty()) continue;
                Map<String, Object> writes = iw.get(i);
                assertNotNull(writes, "item_writes[" + i + "] missing");
                for (Map.Entry<String, Object> entry : expectedItem.entrySet()) {
                    assertTrue(writes.containsKey(entry.getKey()),
                        "item_writes[" + i + "] missing key: " + entry.getKey());
                    assertValueEquals(entry.getValue(), writes.get(entry.getKey()),
                        "item_writes[" + i + "]." + entry.getKey());
                }
            }
        }
    }

    private void assertValueEquals(Object expected, Object actual, String path) {
        if (expected instanceof Number && actual instanceof Number) {
            assertEquals(((Number) expected).doubleValue(), ((Number) actual).doubleValue(), 1e-9, path);
        } else if (expected instanceof Boolean && actual instanceof Boolean) {
            assertEquals(expected, actual, path);
        } else {
            assertEquals(String.valueOf(expected), String.valueOf(actual), path);
        }
    }

    @SuppressWarnings("unchecked")
    private List<String> toStringList(Object raw) {
        if (raw == null) return Collections.emptyList();
        List<String> result = new ArrayList<>();
        for (Object item : (List<Object>) raw) {
            result.add((String) item);
        }
        return result;
    }
}
