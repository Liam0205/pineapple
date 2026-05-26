package page.liam.pine;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.DynamicTest;
import org.junit.jupiter.api.TestFactory;

import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.*;
import java.util.stream.Stream;

import static org.junit.jupiter.api.Assertions.*;

public class PipelineFixtureTest {
    private static final ObjectMapper mapper = new ObjectMapper();

    @TestFactory
    Stream<DynamicTest> pipelineFixtures() throws Exception {
        Path fixturesDir = findFixturesDir();
        if (fixturesDir == null || !Files.isDirectory(fixturesDir)) {
            return Stream.empty();
        }

        List<DynamicTest> tests = new ArrayList<>();
        try (var files = Files.list(fixturesDir)) {
            for (Path path : (Iterable<Path>) files::iterator) {
                if (!path.toString().endsWith(".json")) continue;
                JsonNode root = mapper.readTree(path.toFile());
                String fixtureName = root.has("name") ? root.get("name").asText() : path.getFileName().toString();

                // Skip fixtures that require external services (e.g., redis)
                if (root.has("requires") && root.get("requires").isArray()) {
                    System.out.println("Skipping fixture: " + fixtureName + " (requires: " + root.get("requires") + ")");
                    continue;
                }

                byte[] configBytes = mapper.writeValueAsBytes(root.get("config"));
                JsonNode cases = root.get("cases");

                // Parse strict_order flag (default: true)
                boolean strictOrder = !root.has("strict_order") || root.get("strict_order").asBoolean(true);

                // Parse static_resources if present
                ResourceProvider resourceProvider = null;
                if (root.has("static_resources") && !root.get("static_resources").isNull()) {
                    Map<String, Object> resources = mapper.convertValue(root.get("static_resources"),
                            new TypeReference<Map<String, Object>>() {});
                    resourceProvider = new StaticResourceProvider(resources);
                }

                final ResourceProvider rp = resourceProvider;

                for (JsonNode testCase : cases) {
                    String caseName = testCase.get("name").asText();
                    String fullName = fixtureName + " / " + caseName;
                    String expectError = testCase.has("expect_error") ? testCase.get("expect_error").asText() : null;

                    tests.add(DynamicTest.dynamicTest(fullName, () -> {
                        Engine engine = Engine.create(configBytes, rp);

                        JsonNode request = testCase.get("request");
                        Map<String, Object> common = mapper.convertValue(request.get("common"),
                                new TypeReference<Map<String, Object>>() {});
                        List<Map<String, Object>> items = request.has("items")
                                ? mapper.convertValue(request.get("items"),
                                new TypeReference<List<Map<String, Object>>>() {})
                                : Collections.emptyList();

                        Engine.Result result = engine.execute(common, items);

                        if (expectError != null) {
                            assertNotNull(result.error,
                                    fullName + " — expected error containing \"" + expectError + "\", got success");
                            assertTrue(result.error.getMessage().contains(expectError),
                                    fullName + " — expected error containing \"" + expectError + "\", got: " + result.error.getMessage());
                            return;
                        }

                        assertNull(result.error, fullName + " — unexpected error: " + result.error);

                        JsonNode expected = testCase.get("expected");
                        Map<String, Object> expectedCommon = mapper.convertValue(expected.get("common"),
                                new TypeReference<Map<String, Object>>() {});
                        List<Map<String, Object>> expectedItems = expected.has("items")
                                ? mapper.convertValue(expected.get("items"),
                                new TypeReference<List<Map<String, Object>>>() {})
                                : Collections.emptyList();

                        assertMapEquals(expectedCommon, result.common, fullName + " — common mismatch");

                        List<Map<String, Object>> expItems = expectedItems;
                        List<Map<String, Object>> actItems = result.items;
                        if (!strictOrder) {
                            expItems = sortItemsByJSON(expItems);
                            actItems = sortItemsByJSON(actItems);
                        }
                        assertEquals(expItems.size(), actItems.size(),
                                fullName + " — item count mismatch");
                        for (int idx = 0; idx < expItems.size(); idx++) {
                            assertMapEquals(expItems.get(idx), actItems.get(idx),
                                    fullName + " — items[" + idx + "] mismatch");
                        }
                    }));
                }
            }
        }
        return tests.stream();
    }

    // Machine epsilon for IEEE 754 double precision: 2^-52
    private static final double FLOAT_EPSILON = Math.pow(2, -52);

    /**
     * Relative epsilon comparison for floating-point values.
     * Formula: |a - b| <= eps * max(|a|, |b|, 1.0)
     */
    private static boolean floatsEqual(double a, double b) {
        double diff = Math.abs(a - b);
        double scale = Math.max(Math.max(Math.abs(a), Math.abs(b)), 1.0);
        return diff <= FLOAT_EPSILON * scale;
    }

    private static void assertMapEquals(Map<String, Object> expected, Map<String, Object> actual, String msg) {
        assertEquals(expected.keySet(), actual.keySet(), msg + " (keys differ)");
        for (String key : expected.keySet()) {
            Object e = expected.get(key);
            Object a = actual.get(key);
            if (e instanceof Number && a instanceof Number) {
                assertTrue(floatsEqual(((Number) e).doubleValue(), ((Number) a).doubleValue()),
                        msg + " field=" + key + " expected=" + e + " actual=" + a);
            } else {
                assertEquals(e, a, msg + " field=" + key);
            }
        }
    }

    private static Path findFixturesDir() {
        Path dir = Paths.get(System.getProperty("user.dir"));
        for (int i = 0; i < 5; i++) {
            Path candidate = dir.resolve("fixtures/pipelines");
            if (Files.isDirectory(candidate)) return candidate;
            dir = dir.getParent();
            if (dir == null) break;
        }
        return null;
    }

    /**
     * Returns a copy of items sorted by their JSON serialization.
     * Used for order-insensitive comparison when strict_order is false.
     */
    private static List<Map<String, Object>> sortItemsByJSON(List<Map<String, Object>> items) {
        List<Map<String, Object>> copy = new ArrayList<>(items);
        copy.sort(Comparator.comparing(m -> {
            try {
                return mapper.writeValueAsString(new TreeMap<>(m));
            } catch (Exception e) {
                return "";
            }
        }));
        return copy;
    }
}
