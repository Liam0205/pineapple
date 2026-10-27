package page.liam.pine;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;
import page.liam.pine.debug.Emitter;
import page.liam.pine.debug.NodeMeta;
import page.liam.pine.debug.Recorder;
import page.liam.pine.operators.AllOperators;

import java.time.Duration;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Request-level debug (issue #219): the {@link Recorder} primitives and the
 * engine's node binding, mirroring pine-go's {@code pkg/debug/debug_test.go}
 * and {@code internal/runtime/debug_test.go}.
 */
class RequestDebugTest {
    private static final ObjectMapper MAPPER = new ObjectMapper();
    private static final Object REQUEST_KEY = new Object();

    record Event(NodeMeta meta, String kind, Object data) {
    }

    static final class Capture implements Emitter {
        final List<Event> events = Collections.synchronizedList(new ArrayList<>());

        @Override
        public void emit(NodeMeta meta, String kind, Object data) {
            events.add(new Event(meta, kind, data));
        }

        List<Event> ofKind(String kind) {
            synchronized (events) {
                return events.stream().filter(e -> e.kind.equals(kind)).toList();
            }
        }
    }

    // ---- Recorder ---------------------------------------------------------

    @Test
    void disabledByDefault() {
        RequestContext bg = RequestContext.background();
        assertNull(Recorder.fromContext(bg));
        assertSame(bg, Recorder.withEmitter(bg, null));
    }

    @Test
    void nilEmitterKeepsExistingRecorder() {
        Capture capture = new Capture();
        RequestContext ctx = Recorder.withEmitter(RequestContext.background(), capture);
        assertSame(ctx, Recorder.withEmitter(ctx, null));
        Recorder.fromContext(ctx).emit("k", 1);
        assertEquals(1, capture.events.size());
    }

    @Test
    void bindNodePreservesValuesAndCancellationWithoutMutatingParent() {
        Capture capture = new Capture();
        RequestContext.Cancelable root = RequestContext.withCancel(RequestContext.background());
        RequestContext parent = Recorder.withEmitter(RequestContext.withValue(root, REQUEST_KEY, "kept"), capture);
        NodeMeta meta = new NodeMeta("rank", "transform_x", "scoring");
        RequestContext bound = Recorder.fromContext(parent).bindNode(parent, meta);

        assertEquals("kept", bound.value(REQUEST_KEY));
        Recorder.fromContext(bound).emit("bound", 1);
        Recorder.fromContext(parent).emit("parent", 2);
        assertEquals(List.of(new Event(meta, "bound", 1), new Event(NodeMeta.NONE, "parent", 2)), capture.events);

        root.cancel();
        assertTrue(bound.isCancelled());
    }

    @Test
    void emitterFailureIsIsolated() {
        RequestContext ctx = Recorder.withEmitter(RequestContext.background(), (meta, kind, data) -> {
            throw new IllegalStateException("emitter broke");
        });
        Recorder.fromContext(ctx).emit("k", 1);
        RequestContext errCtx = Recorder.withEmitter(RequestContext.background(), (meta, kind, data) -> {
            throw new StackOverflowError();
        });
        Recorder.fromContext(errCtx).emit("k", 1);
    }

    // ---- engine binding ---------------------------------------------------

    // Hooks set per test; tests in this class run sequentially.
    private static volatile CountDownLatch started;
    private static volatile CountDownLatch release;
    private static volatile boolean blockUntilCancelled;

    public static final class DebugTestOp extends AbstractOperator implements ConcurrentSafe {
        private String field;

        @Override
        public void init(OperatorParams params) {
            field = params.getString("field", "result");
        }

        @Override
        public void execute(RequestContext ctx, OperatorInput input, OperatorOutput output) throws PineErrors.OperatorException {
            if (!"kept".equals(ctx.value(REQUEST_KEY))) {
                throw new PineErrors.OperatorException("node context lost request values");
            }
            int n = input.itemCount();
            CountDownLatch s = started;
            if (s != null) {
                s.countDown();
                try {
                    if (blockUntilCancelled) {
                        ctx.awaitCancellation(Duration.ofSeconds(10));
                        ctx.throwIfCancelled();
                    } else {
                        release.await(10, TimeUnit.SECONDS);
                    }
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                }
            }
            Recorder r = Recorder.fromContext(ctx);
            if (r != null) {
                r.emit("items", n);
            }
            for (int i = 0; i < n; i++) {
                Object value = input.item(i, "value");
                if (r != null) {
                    r.emit("item", value);
                }
                output.setItem(i, field, value);
            }
        }
    }

    private static boolean registered;

    private static synchronized void register() {
        if (registered) {
            return;
        }
        AllOperators.ensureRegistered();
        Registry.registerGlobal(new OperatorSchema("transform_debug_test", OperatorType.TRANSFORM,
                "test-only: emits request debug events", Map.of(
                        "field", new ParamSpec("string", false, "result", "item output field"))),
                DebugTestOp::new);
        registered = true;
    }

    private static Map<String, Object> op(String field, int dataParallel) {
        Map<String, Object> meta = new LinkedHashMap<>();
        meta.put("common_input", List.of());
        meta.put("common_output", List.of());
        meta.put("item_input", List.of("value"));
        meta.put("item_output", List.of(field));
        Map<String, Object> op = new LinkedHashMap<>();
        op.put("type_name", "transform_debug_test");
        op.put("$metadata", meta);
        op.put("field", field);
        if (dataParallel > 1) {
            op.put("data_parallel", dataParallel);
        }
        return op;
    }

    // ops all live in the pipeline_map entry "scoring".
    private static Engine engine(Map<String, Map<String, Object>> ops, List<String> itemOut) throws Exception {
        register();
        Map<String, Object> pipelineConfig = new LinkedHashMap<>();
        pipelineConfig.put("operators", ops);
        pipelineConfig.put("pipeline_map", Map.of("scoring", Map.of("pipeline", new ArrayList<>(ops.keySet()))));
        Map<String, Object> contract = new LinkedHashMap<>();
        contract.put("common_input", List.of());
        contract.put("common_output", List.of());
        contract.put("item_input", List.of("value"));
        contract.put("item_output", itemOut);
        Map<String, Object> config = new LinkedHashMap<>();
        config.put("pipeline_config", pipelineConfig);
        config.put("pipeline_group", Map.of("main", Map.of("pipeline", List.of("scoring"))));
        config.put("flow_contract", contract);
        return Engine.create(MAPPER.writeValueAsBytes(config));
    }

    private static List<Map<String, Object>> items(int n) {
        List<Map<String, Object>> items = new ArrayList<>();
        for (int i = 0; i < n; i++) {
            Map<String, Object> item = new LinkedHashMap<>();
            item.put("value", i + 1);
            items.add(item);
        }
        return items;
    }

    @Test
    void preservesParallelShardsAndNodeMetadata() throws Exception {
        started = null;
        Map<String, Map<String, Object>> ops = new LinkedHashMap<>();
        ops.put("rank", op("result", 3));
        Engine engine = engine(ops, List.of("result"));
        NodeMeta want = new NodeMeta("rank", "transform_debug_test", "scoring");

        for (boolean enabled : new boolean[]{false, true, false}) {
            RequestContext ctx = RequestContext.withValue(RequestContext.background(), REQUEST_KEY, "kept");
            Capture capture = new Capture();
            if (enabled) {
                ctx = Recorder.withEmitter(ctx, capture);
            }
            Engine.Result result = engine.execute(ctx, new LinkedHashMap<>(), items(6));
            assertNull(result.error);
            for (int i = 0; i < 6; i++) {
                assertEquals(i + 1, result.items.get(i).get("result"));
            }
            assertEquals(1, result.trace.size());
            assertNull(result.trace.get(0).inputSnapshot, "request recorder must not turn on static snapshots");
            assertNull(result.trace.get(0).outputSnapshot);

            if (!enabled) {
                assertTrue(capture.events.isEmpty());
                continue;
            }
            List<Event> shards = capture.ofKind("items");
            assertEquals(3, shards.size(), "one event per data_parallel shard");
            for (Event e : shards) {
                assertEquals(want, e.meta);
                assertEquals(2, e.data);
            }
            List<Event> perItem = capture.ofKind("item");
            assertEquals(6, perItem.size());
            Set<Object> seen = new HashSet<>();
            for (Event e : perItem) {
                assertEquals(want, e.meta);
                assertTrue(seen.add(e.data), "duplicated item " + e.data);
            }
            assertEquals(Set.of(1, 2, 3, 4, 5, 6), seen);
        }
    }

    @Test
    void separatesConcurrentNodesAndRequests() throws Exception {
        final int requests = 8;
        started = new CountDownLatch(2 * requests);
        release = new CountDownLatch(1);
        blockUntilCancelled = false;
        try {
            Map<String, Map<String, Object>> ops = new LinkedHashMap<>();
            ops.put("first", op("first", 1));
            ops.put("second", op("second", 1));
            Engine engine = engine(ops, List.of("first", "second"));

            List<Thread> threads = new ArrayList<>();
            List<String> failures = Collections.synchronizedList(new ArrayList<>());
            for (int count = 1; count <= requests; count++) {
                final int n = count;
                threads.add(Thread.ofVirtual().start(() -> {
                    Capture capture = new Capture();
                    RequestContext ctx = Recorder.withEmitter(
                            RequestContext.withValue(RequestContext.background(), REQUEST_KEY, "kept"), capture);
                    Engine.Result result = engine.execute(ctx, new LinkedHashMap<>(), items(n));
                    if (result.error != null) {
                        failures.add(result.error.toString());
                        return;
                    }
                    Set<String> seen = new HashSet<>();
                    for (Event e : capture.ofKind("items")) {
                        if (!e.data.equals(n) || !e.meta.type().equals("transform_debug_test")
                                || !e.meta.subFlow().equals("scoring")) {
                            failures.add("cross-request data/meta " + e + " for request with " + n + " items");
                        }
                        seen.add(e.meta.operator());
                    }
                    if (capture.ofKind("items").size() != 2 || !seen.equals(Set.of("first", "second"))) {
                        failures.add("node scopes not isolated: " + capture.events);
                    }
                }));
            }
            assertTrue(started.await(10, TimeUnit.SECONDS), "independent nodes did not overlap");
            release.countDown();
            for (Thread t : threads) {
                t.join();
            }
            assertEquals(List.of(), failures);
        } finally {
            started = null;
        }
    }

    @Test
    void parallelShardsKeepCallerCancellation() throws Exception {
        started = new CountDownLatch(3);
        blockUntilCancelled = true;
        try {
            Map<String, Map<String, Object>> ops = new LinkedHashMap<>();
            ops.put("rank", op("result", 3));
            Engine engine = engine(ops, List.of("result"));
            RequestContext.Cancelable parent = RequestContext.withTimeout(RequestContext.background(), Duration.ofSeconds(10));
            RequestContext ctx = Recorder.withEmitter(
                    RequestContext.withValue(parent, REQUEST_KEY, "kept"), new Capture());

            Engine.Result[] result = new Engine.Result[1];
            Thread runner = Thread.ofVirtual().start(() -> result[0] = engine.execute(ctx, new LinkedHashMap<>(), items(3)));
            assertTrue(started.await(10, TimeUnit.SECONDS), "parallel shards did not start");
            parent.cancel();
            runner.join(Duration.ofSeconds(10));
            assertFalse(runner.isAlive(), "parallel shards did not stop after caller cancellation");
            assertNotNull(result[0].error);
            assertTrue(result[0].error.getMessage().contains("context canceled"), result[0].error.getMessage());
        } finally {
            started = null;
            blockUntilCancelled = false;
        }
    }
}
