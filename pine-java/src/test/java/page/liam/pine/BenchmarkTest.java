package page.liam.pine;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;

import java.nio.file.*;
import java.util.*;

import static org.junit.jupiter.api.Assertions.assertNotNull;

public class BenchmarkTest {
    private static final ObjectMapper mapper = new ObjectMapper();
    private static final int WARMUP = 50;
    private static final int ITERATIONS = 500;

    @Test
    void benchmarkPipelineFixtures() throws Exception {
        Path fixturesDir = findFixturesDir();
        if (fixturesDir == null) return;

        List<BenchCase> cases = new ArrayList<>();
        try (var files = Files.list(fixturesDir)) {
            for (Path path : (Iterable<Path>) files::iterator) {
                if (!path.toString().endsWith(".json")) continue;
                JsonNode root = mapper.readTree(path.toFile());
                String name = path.getFileName().toString().replace(".json", "");
                byte[] configBytes = mapper.writeValueAsBytes(root.get("config"));

                ResourceProvider rp = null;
                if (root.has("static_resources") && !root.get("static_resources").isNull()) {
                    Map<String, Object> resources = mapper.convertValue(root.get("static_resources"),
                            new TypeReference<Map<String, Object>>() {});
                    rp = new StaticResourceProvider(resources);
                }

                JsonNode firstCase = root.get("cases").get(0);
                JsonNode request = firstCase.get("request");
                Map<String, Object> common = mapper.convertValue(request.get("common"),
                        new TypeReference<Map<String, Object>>() {});
                List<Map<String, Object>> items = request.has("items")
                        ? mapper.convertValue(request.get("items"),
                        new TypeReference<List<Map<String, Object>>>() {})
                        : Collections.emptyList();

                cases.add(new BenchCase(name, configBytes, rp, common, items));
            }
        }

        cases.sort(Comparator.comparing(c -> c.name));

        System.out.println("\n=== Pine-Java Pipeline Benchmark ===");
        System.out.printf("%-40s %10s %10s %10s%n", "fixture", "ops/sec", "avg(µs)", "p99(µs)");
        System.out.println("-".repeat(74));

        for (BenchCase bc : cases) {
            Engine engine = Engine.create(bc.configBytes, bc.resourceProvider);

            for (int i = 0; i < WARMUP; i++) {
                engine.execute(bc.common, bc.items);
            }

            long[] durations = new long[ITERATIONS];
            for (int i = 0; i < ITERATIONS; i++) {
                long start = System.nanoTime();
                Engine.Result result = engine.execute(bc.common, bc.items);
                durations[i] = System.nanoTime() - start;
                assertNotNull(result);
            }

            Arrays.sort(durations);
            long avg = Arrays.stream(durations).sum() / ITERATIONS;
            long p99 = durations[(int) (ITERATIONS * 0.99)];
            double opsPerSec = 1_000_000_000.0 / avg;

            System.out.printf("%-40s %10.0f %10.1f %10.1f%n",
                    bc.name, opsPerSec, avg / 1000.0, p99 / 1000.0);
        }
        System.out.println();
    }

    @Test
    void benchmarkEngineCreate() throws Exception {
        Path fixturesDir = findFixturesDir();
        if (fixturesDir == null) return;

        Path path = fixturesDir.resolve("recall_merge_filter_sort.json");
        if (!Files.exists(path)) return;

        JsonNode root = mapper.readTree(path.toFile());
        byte[] configBytes = mapper.writeValueAsBytes(root.get("config"));

        for (int i = 0; i < WARMUP; i++) {
            Engine.create(configBytes);
        }

        long[] durations = new long[ITERATIONS];
        for (int i = 0; i < ITERATIONS; i++) {
            long start = System.nanoTime();
            Engine engine = Engine.create(configBytes);
            durations[i] = System.nanoTime() - start;
            assertNotNull(engine);
        }

        Arrays.sort(durations);
        long avg = Arrays.stream(durations).sum() / ITERATIONS;
        long p99 = durations[(int) (ITERATIONS * 0.99)];

        System.out.println("\n=== Engine.create() Benchmark ===");
        System.out.printf("avg: %.1f µs, p99: %.1f µs, ops/sec: %.0f%n",
                avg / 1000.0, p99 / 1000.0, 1_000_000_000.0 / avg);
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

    private static class BenchCase {
        final String name;
        final byte[] configBytes;
        final ResourceProvider resourceProvider;
        final Map<String, Object> common;
        final List<Map<String, Object>> items;

        BenchCase(String name, byte[] configBytes, ResourceProvider resourceProvider,
                  Map<String, Object> common, List<Map<String, Object>> items) {
            this.name = name;
            this.configBytes = configBytes;
            this.resourceProvider = resourceProvider;
            this.common = common;
            this.items = items;
        }
    }
}
