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
                byte[] configBytes = mapper.writeValueAsBytes(root.get("config"));
                JsonNode cases = root.get("cases");

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

                        JsonNode expected = testCase.get("expected");
                        Map<String, Object> expectedCommon = mapper.convertValue(expected.get("common"),
                                new TypeReference<Map<String, Object>>() {});
                        List<Map<String, Object>> expectedItems = expected.has("items")
                                ? mapper.convertValue(expected.get("items"),
                                new TypeReference<List<Map<String, Object>>>() {})
                                : Collections.emptyList();

                        assertEquals(expectedCommon, result.common, fullName + " — common mismatch");
                        assertEquals(expectedItems.size(), result.items.size(),
                                fullName + " — item count mismatch");
                        for (int idx = 0; idx < expectedItems.size(); idx++) {
                            assertEquals(expectedItems.get(idx), result.items.get(idx),
                                    fullName + " — items[" + idx + "] mismatch");
                        }
                    }));
                }
            }
        }
        return tests.stream();
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
}
