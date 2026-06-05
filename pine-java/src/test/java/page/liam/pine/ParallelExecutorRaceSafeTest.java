package page.liam.pine;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.DisplayName;

import java.util.*;
import java.util.concurrent.*;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Lazy OperatorInput proxy must be safe to dereference concurrently from
 * data_parallel shards. ParallelExecutor.execute splits a lazy input into
 * N shards that all share the same FrameReader; the operator's Execute
 * runs in N parallel ExecutorService tasks and reads through that shared
 * frame.
 *
 * <p>Without a targeted test, any future change that introduces
 * non-thread-safe state into ColumnFrame.item() (e.g. a memoization
 * cache that forgets to take rwLock) would slip past the existing
 * IntegrationTest because that test exercises full Engine pipelines
 * without the -race / -fno-omit-frame-pointer style introspection.
 * This test runs a bare-metal lazy split with N=8 shards repeatedly
 * and asserts every (i, val+1) pair lands correctly. Run with `mvn test`
 * — failures here also tend to surface as flaky values.
 */
public class ParallelExecutorRaceSafeTest {

    static class StatelessIncrementOp implements Operator, ConcurrentSafe {
        @Override
        public void init(OperatorParams params) {}

        @Override
        public void execute(CancellationToken token, OperatorInput input, OperatorOutput out) {
            for (int i = 0; i < input.itemCount(); i++) {
                Object v = input.item(i, "val");
                double d = (v instanceof Number) ? ((Number) v).doubleValue() : 0.0;
                out.setItem(i, "result", d + 1.0);
            }
        }
    }

    @Test
    @DisplayName("ParallelExecutor lazy input race-safe across shards")
    void lazyInputAcrossShardsIsRaceSafe() throws Exception {
        final int itemCount = 200;
        final int parallelism = 8;
        final int iterations = 10;

        ExecutorService pool = Executors.newFixedThreadPool(parallelism);
        try {
            for (int iter = 0; iter < iterations; iter++) {
                List<Map<String, Object>> items = new ArrayList<>(itemCount);
                for (int i = 0; i < itemCount; i++) {
                    items.add(new LinkedHashMap<>(Map.of("val", (double) i)));
                }
                ColumnFrame frame = new ColumnFrame(new LinkedHashMap<>(), items);
                InputFieldSpec spec = new InputFieldSpec(
                        List.of(), List.of(), List.of(),
                        List.of("val"), List.of(), List.of());
                OperatorInput input = frame.buildInput("incr", spec);
                assertTrue(input.isLazy(),
                        "iter " + iter + ": expected lazy input from ColumnFrame.buildInput");

                OperatorOutput out = ParallelExecutor.execute(
                        CancellationToken.create(),
                        new StatelessIncrementOp(),
                        input,
                        parallelism,
                        "incr",
                        pool);

                Map<Integer, Map<String, Object>> writes = out.getItemWrites();
                assertEquals(itemCount, writes.size(), "iter " + iter + ": item write count");
                for (int i = 0; i < itemCount; i++) {
                    Map<String, Object> row = writes.get(i);
                    assertNotNull(row, "iter " + iter + " missing row " + i);
                    assertEquals((double) i + 1.0, ((Number) row.get("result")).doubleValue(),
                            "iter " + iter + " row " + i);
                }
            }
        } finally {
            pool.shutdownNow();
            pool.awaitTermination(2, TimeUnit.SECONDS);
        }
    }
}
