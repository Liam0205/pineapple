package page.liam.pine;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;

import java.io.ByteArrayOutputStream;
import java.io.PrintStream;
import java.nio.charset.StandardCharsets;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Pins the issue #172 fix: log_prefix is engine-scoped, not process-global.
 * Multiple engines in one process each keep their own prefix; construction
 * order does not matter and no global state is touched. Mirrors pine-go
 * TestLogPrefixPerEngineIsolation.
 */
public class LogPrefixTest {
    private static final ObjectMapper mapper = new ObjectMapper();

    private byte[] configWithPrefix(String prefix) throws Exception {
        Map<String, Object> meta = new LinkedHashMap<>();
        meta.put("common_input", List.of());
        meta.put("common_output", List.of());
        meta.put("item_input", List.of());
        meta.put("item_output", List.of());

        Map<String, Object> op = new LinkedHashMap<>();
        op.put("type_name", "observe_log");
        op.put("$metadata", meta);

        Map<String, Object> pipelineConfig = new LinkedHashMap<>();
        pipelineConfig.put("operators", Map.of("obs", op));
        pipelineConfig.put("pipeline_map",
                Map.of("s", Map.of("pipeline", List.of("obs"))));

        Map<String, Object> config = new LinkedHashMap<>();
        config.put("log_prefix", prefix);
        config.put("pipeline_config", pipelineConfig);
        config.put("pipeline_group", Map.of("main", Map.of("pipeline", List.of("s"))));
        Map<String, Object> contract = new LinkedHashMap<>();
        contract.put("common_input", List.of());
        contract.put("common_output", List.of());
        contract.put("item_input", List.of());
        contract.put("item_output", List.of());
        config.put("flow_contract", contract);
        return mapper.writeValueAsBytes(config);
    }

    @Test
    void perEngineIsolation() throws Exception {
        Engine first = Engine.create(configWithPrefix("[engine-b] "));
        try {
            Engine second = Engine.create(configWithPrefix("[engine-a] "));
            try {
                assertEquals("[engine-b] ", first.logPrefix());
                // The second engine's prefix must not be first-engine-wins ignored.
                assertEquals("[engine-a] ", second.logPrefix(),
                        "second engine prefix ignored (first-engine-wins regression)");
            } finally {
                second.close();
            }
        } finally {
            first.close();
        }
        // No global state: the old System property channel must stay unset.
        assertNull(System.getProperty("pine.log.prefix"),
                "log_prefix must not leak into global System properties");
    }

    @Test
    void optionOverridesJson() throws Exception {
        Engine engine = Engine.create(configWithPrefix("[json] "),
                Engine.withLogPrefix("[opt] "));
        try {
            assertEquals("[opt] ", engine.logPrefix());
        } finally {
            engine.close();
        }
    }

    // An explicit empty-string option must override a non-empty JSON prefix
    // (nullable tri-state, cross-runtime parity with Go/C++).
    @Test
    void emptyOptionOverridesJson() throws Exception {
        Engine engine = Engine.create(configWithPrefix("[json] "),
                Engine.withLogPrefix(""));
        try {
            assertEquals("", engine.logPrefix(),
                    "explicit empty option must win over JSON prefix");
        } finally {
            engine.close();
        }
    }

    // Concurrent executes on two engines must never interleave prefix and
    // body across engines: logf emits prefix + body + newline in one
    // PrintStream call. Captures real stderr instead of trusting the stored
    // prefix string.
    @Test
    void concurrentEnginesDoNotInterleavePrefixAndBody() throws Exception {
        Engine a = Engine.create(configWithPrefix("[engine-a] "));
        Engine b = Engine.create(configWithPrefix("[engine-b] "));
        PrintStream origErr = System.err;
        ByteArrayOutputStream captured = new ByteArrayOutputStream();
        ExecutorService pool = Executors.newFixedThreadPool(8);
        try {
            System.setErr(new PrintStream(captured, true, StandardCharsets.UTF_8));
            CountDownLatch start = new CountDownLatch(1);
            int perEngine = 50;
            for (int i = 0; i < perEngine; i++) {
                pool.submit(() -> {
                    start.await();
                    a.execute(Map.of(), List.of());
                    return null;
                });
                pool.submit(() -> {
                    start.await();
                    b.execute(Map.of(), List.of());
                    return null;
                });
            }
            start.countDown();
            pool.shutdown();
            assertTrue(pool.awaitTermination(30, TimeUnit.SECONDS), "workers did not finish");
        } finally {
            System.setErr(origErr);
            pool.shutdownNow();
            a.close();
            b.close();
        }

        int observed = 0;
        for (String line : captured.toString(StandardCharsets.UTF_8).split("\\R")) {
            if (!line.contains("[observe_log]")) {
                continue;
            }
            observed++;
            boolean fromA = line.startsWith("[engine-a] ");
            boolean fromB = line.startsWith("[engine-b] ");
            assertTrue(fromA || fromB, "observe_log line lost its engine prefix: " + line);
            // A doubled prefix means two engines' writes interleaved.
            String rest = line.substring("[engine-x] ".length());
            assertFalse(rest.startsWith("[engine-a] ") || rest.startsWith("[engine-b] "),
                    "interleaved prefixes detected: " + line);
        }
        assertTrue(observed > 0, "expected observe_log output to be captured");
    }
}
