package page.liam.pine;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.sun.net.httpserver.HttpServer;
import org.junit.jupiter.api.Test;
import page.liam.pine.metrics.MetricsCollector;
import page.liam.pine.operators.AllOperators;

import java.net.InetSocketAddress;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Request-context cancellation (issue #217): the {@link RequestContext}
 * primitives, the engine's CancelledError contract (mirroring pine-go's
 * scheduler tests), and the operators that stop on a done context.
 */
class CancellationTest {
    private static final ObjectMapper MAPPER = new ObjectMapper();

    // ---- RequestContext ---------------------------------------------------

    @Test
    void backgroundIsNeverDone() {
        RequestContext bg = RequestContext.background();
        assertFalse(bg.isCancelled());
        assertNull(bg.err());
        assertTrue(bg.deadline().isEmpty());
        assertNull(bg.resources());
    }

    @Test
    void cancelPropagatesDownNotUp() {
        RequestContext.Cancelable parent = RequestContext.withCancel(RequestContext.background());
        RequestContext.Cancelable child = RequestContext.withCancel(parent);
        RequestContext grandchild = RequestContext.withValue(child, "k", "v");

        child.cancel();
        assertTrue(grandchild.isCancelled());
        assertEquals("context canceled", grandchild.err().getMessage());
        assertFalse(parent.isCancelled());

        RequestContext.Cancelable sibling = RequestContext.withCancel(parent);
        parent.cancel();
        assertTrue(sibling.isCancelled());
        // The child sees the parent's error instance, as Go's ctx.Err() does.
        assertSame(parent.err(), sibling.err());
    }

    @Test
    void deadlineCancelsWithDeadlineExceeded() throws Exception {
        RequestContext.Cancelable ctx = RequestContext.withTimeout(RequestContext.background(), Duration.ofMillis(20));
        assertTrue(ctx.awaitCancellation(Duration.ofSeconds(5)));
        assertTrue(ctx.err().isDeadlineExceeded());
        assertEquals("context deadline exceeded", ctx.err().getMessage());
    }

    @Test
    void pastDeadlineIsDoneImmediately() {
        RequestContext.Cancelable ctx = RequestContext.withDeadline(RequestContext.background(), Instant.now().minusSeconds(1));
        assertTrue(ctx.isCancelled());
        assertTrue(ctx.err().isDeadlineExceeded());
    }

    @Test
    void earlierParentDeadlineWins() {
        Instant soon = Instant.now().plusSeconds(10);
        RequestContext.Cancelable parent = RequestContext.withDeadline(RequestContext.background(), soon);
        RequestContext.Cancelable child = RequestContext.withDeadline(parent, soon.plusSeconds(60));
        assertEquals(soon, child.deadline().orElseThrow());
        parent.cancel();
        child.cancel();
    }

    @Test
    void valuesAndResourcesResolveToNearest() {
        ResourceProvider rp = name -> new ResourceProvider.GetResult(name, true);
        RequestContext ctx = RequestContext.withValue(RequestContext.background(), "a", 1);
        ctx = RequestContext.withResources(ctx, rp);
        ctx = RequestContext.withValue(ctx, "a", 2);
        assertEquals(2, ctx.value("a"));
        assertNull(ctx.value("missing"));
        assertSame(rp, ctx.resources());
    }

    @Test
    void onCancelRunsOnceAndRegistrationRemoves() {
        RequestContext.Cancelable ctx = RequestContext.withCancel(RequestContext.background());
        AtomicInteger fired = new AtomicInteger();
        AtomicInteger removed = new AtomicInteger();
        ctx.onCancel(fired::incrementAndGet);
        ctx.onCancel(removed::incrementAndGet).close();
        ctx.cancel();
        ctx.cancel();
        assertEquals(1, fired.get());
        assertEquals(0, removed.get());

        // Registering on a done context runs the callback inline.
        ctx.onCancel(fired::incrementAndGet);
        assertEquals(2, fired.get());
    }

    @Test
    void finishedChildrenDoNotAccumulateOnParent() {
        RequestContext.Cancelable root = RequestContext.withCancel(RequestContext.background());
        for (int i = 0; i < 100; i++) {
            try (RequestContext.Cancelable child = RequestContext.withCancel(root)) {
                assertFalse(child.isCancelled());
            }
        }
        assertEquals(0, root.signal.pendingCallbacks());
        root.cancel();
    }

    @Test
    void deadlineUnderCancelledParentSchedulesNoTimer() {
        RequestContext.Cancelable parent = RequestContext.withCancel(RequestContext.background());
        parent.cancel();
        int before = RequestContext.Signal.pendingDeadlines();
        for (int i = 0; i < 100; i++) {
            RequestContext.Cancelable child = RequestContext.withTimeout(parent, Duration.ofHours(1));
            assertTrue(child.isCancelled());
            assertFalse(child.err().isDeadlineExceeded());
        }
        // Each child was cancelled in childOf; none may leave an hour-long
        // task queued on the shared deadline timer.
        assertEquals(before, RequestContext.Signal.pendingDeadlines());
    }

    // ---- engine contract ---------------------------------------------------

    // Blocks until RELEASE opens, ignoring ctx: the engine, not the operator,
    // must notice the cancellation. Tests in this class run sequentially, so
    // the latches are reset per test.
    private static volatile CountDownLatch started;
    private static volatile CountDownLatch release;

    public static final class BlockIgnoringCtx extends AbstractOperator {
        @Override
        public void init(OperatorParams params) {
        }

        @Override
        public void execute(RequestContext ctx, OperatorInput input, OperatorOutput output) {
            started.countDown();
            try {
                release.await();
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
            output.setCommon("x", true);
        }
    }

    public static final class CopyXToY extends AbstractOperator {
        @Override
        public void init(OperatorParams params) {
        }

        @Override
        public void execute(RequestContext ctx, OperatorInput input, OperatorOutput output) {
            output.setCommon("y", input.common("x"));
        }
    }

    private static boolean registered;

    private static synchronized void registerProbes() {
        if (registered) {
            return;
        }
        AllOperators.ensureRegistered();
        Registry.registerGlobal(new OperatorSchema("test_block_ignoring_ctx", OperatorType.TRANSFORM,
                "test-only: blocks until released, ignoring ctx", Map.of()), BlockIgnoringCtx::new);
        Registry.registerGlobal(new OperatorSchema("test_copy_x_y", OperatorType.TRANSFORM,
                "test-only: copies common x to y", Map.of()), CopyXToY::new);
        registered = true;
    }

    private static Map<String, Object> op(String type, List<String> commonIn, List<String> commonOut) {
        Map<String, Object> meta = new LinkedHashMap<>();
        meta.put("common_input", commonIn);
        meta.put("common_output", commonOut);
        meta.put("item_input", List.of());
        meta.put("item_output", List.of());
        Map<String, Object> op = new LinkedHashMap<>();
        op.put("type_name", type);
        op.put("$metadata", meta);
        return op;
    }

    private static byte[] config(Map<String, Map<String, Object>> ops, List<String> commonOut) throws Exception {
        Map<String, Object> pipelineConfig = new LinkedHashMap<>();
        pipelineConfig.put("operators", ops);
        pipelineConfig.put("pipeline_map", Map.of("s", Map.of("pipeline", new ArrayList<>(ops.keySet()))));
        Map<String, Object> contract = new LinkedHashMap<>();
        contract.put("common_input", List.of());
        contract.put("common_output", commonOut);
        contract.put("item_input", List.of());
        contract.put("item_output", List.of());
        Map<String, Object> config = new LinkedHashMap<>();
        config.put("pipeline_config", pipelineConfig);
        config.put("pipeline_group", Map.of("main", Map.of("pipeline", List.of("s"))));
        config.put("flow_contract", contract);
        return MAPPER.writeValueAsBytes(config);
    }

    private static Engine chainEngine(MetricsCollector metrics) throws Exception {
        registerProbes();
        Map<String, Map<String, Object>> ops = new LinkedHashMap<>();
        ops.put("op_a", op("test_block_ignoring_ctx", List.of(), List.of("x")));
        ops.put("op_b", op("test_copy_x_y", List.of("x"), List.of("y")));
        return Engine.create(config(ops, List.of("x", "y")), Engine.withMetrics(metrics));
    }

    // Runs op_a → op_b under ctx: once op_a started, calls trigger, then
    // releases op_a only after ctx is done, so op_b always finds it cancelled.
    private static Engine.Result runChain(Engine engine, RequestContext ctx, Runnable trigger) throws Exception {
        started = new CountDownLatch(1);
        release = new CountDownLatch(1);
        Thread helper = Thread.ofVirtual().start(() -> {
            try {
                started.await();
                trigger.run();
                ctx.awaitCancellation(Duration.ofSeconds(10));
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            } finally {
                release.countDown();
            }
        });
        Engine.Result result = engine.execute(ctx, new LinkedHashMap<>(), List.of());
        helper.join();
        return result;
    }

    @SuppressWarnings("unchecked")
    private static double dagExecCount(MetricsCollector metrics, String status) {
        Map<String, Object> cells = (Map<String, Object>) metrics.snapshot().get("pine_dag_executions_total");
        Object v = cells.get(status);
        return v == null ? 0 : (Double) v;
    }

    @Test
    void cancelledMidRunReportsCancelledError() throws Exception {
        MetricsCollector metrics = new MetricsCollector();
        Engine engine = chainEngine(metrics);
        RequestContext.Cancelable ctx = RequestContext.withCancel(RequestContext.background());
        Engine.Result result = runChain(engine, ctx, ctx::cancel);

        assertInstanceOf(PineErrors.CancelledError.class, result.error);
        assertEquals("pine: execution cancelled: context canceled", result.error.getMessage());
        assertEquals(true, result.common.get("x"), "op_a completed and keeps its write");
        assertFalse(result.common.containsKey("y"), "op_b never ran");
        assertEquals(1, result.trace.size());
        assertEquals("op_a", result.trace.get(0).name);
        assertEquals(1.0, dagExecCount(metrics, "error"));
        assertEquals(0.0, dagExecCount(metrics, "success"));
        engine.close();
    }

    @Test
    void deadlineReportsDeadlineExceeded() throws Exception {
        Engine engine = chainEngine(new MetricsCollector());
        RequestContext.Cancelable ctx = RequestContext.withTimeout(RequestContext.background(), Duration.ofMillis(20));
        Engine.Result result = runChain(engine, ctx, () -> { });

        assertInstanceOf(PineErrors.CancelledError.class, result.error);
        assertEquals("pine: execution cancelled: context deadline exceeded", result.error.getMessage());
        assertTrue(((PineErrors.CancelledError) result.error).getCause().isDeadlineExceeded());
        engine.close();
    }

    @Test
    void cancelledDuringLastNodeIsSuccess() throws Exception {
        registerProbes();
        Map<String, Map<String, Object>> ops = new LinkedHashMap<>();
        ops.put("op_a", op("test_block_ignoring_ctx", List.of(), List.of("x")));
        Engine engine = Engine.create(config(ops, List.of("x")));
        RequestContext.Cancelable ctx = RequestContext.withCancel(RequestContext.background());
        started = new CountDownLatch(1);
        release = new CountDownLatch(1);
        Thread helper = Thread.ofVirtual().start(() -> {
            try {
                started.await();
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
            ctx.cancel();
            release.countDown();
        });
        Engine.Result result = engine.execute(ctx, new LinkedHashMap<>(), List.of());
        helper.join();
        assertNull(result.error);
        assertEquals(true, result.common.get("x"));
        engine.close();
    }

    @Test
    void benchSleepStopsOnDeadline() throws Exception {
        AllOperators.ensureRegistered();
        Map<String, Object> sleep = op("transform_bench_sleep", List.of(), List.of());
        sleep.put("delay_ms", 10_000);
        Map<String, Map<String, Object>> ops = new LinkedHashMap<>();
        ops.put("sleep", sleep);
        Engine engine = Engine.create(config(ops, List.of()));
        long start = System.nanoTime();
        Engine.Result result;
        try (RequestContext.Cancelable ctx = RequestContext.withTimeout(RequestContext.background(), Duration.ofMillis(50))) {
            result = engine.execute(ctx, new LinkedHashMap<>(), List.of());
        }
        long elapsedMs = (System.nanoTime() - start) / 1_000_000;
        // The operator returned the context error itself, so it is an
        // ExecutionError naming it, as in pine-go.
        assertInstanceOf(PineErrors.ExecutionError.class, result.error);
        assertEquals("pine: execution error in operator \"sleep\": context deadline exceeded", result.error.getMessage());
        assertTrue(elapsedMs < 5_000, "bench_sleep should stop at the deadline, took " + elapsedMs + " ms");
        engine.close();
    }

    @Test
    void serverStopCancelsInFlightRequests() throws Exception {
        AllOperators.ensureRegistered();
        Map<String, Object> sleep = op("transform_bench_sleep", List.of(), List.of());
        sleep.put("delay_ms", 30_000);
        Map<String, Map<String, Object>> ops = new LinkedHashMap<>();
        ops.put("sleep", sleep);
        java.nio.file.Path cfg = java.nio.file.Files.createTempFile("pine-cancel-stop-", ".json");
        java.nio.file.Files.write(cfg, config(ops, List.of()));
        int port;
        try (java.net.ServerSocket s = new java.net.ServerSocket(0)) {
            port = s.getLocalPort();
        }
        PineServer server = new PineServer(cfg.toString(), port);
        server.setWatch(false);
        server.start();
        try {
            java.net.http.HttpClient client = java.net.http.HttpClient.newHttpClient();
            java.net.http.HttpRequest req = java.net.http.HttpRequest.newBuilder(
                            java.net.URI.create("http://127.0.0.1:" + port + "/execute"))
                    .POST(java.net.http.HttpRequest.BodyPublishers.ofString("{\"common\":{},\"items\":[]}"))
                    .build();
            var pending = client.sendAsync(req, java.net.http.HttpResponse.BodyHandlers.ofString());
            // Let the request reach the operator before stopping.
            Thread.sleep(300);
            long start = System.nanoTime();
            server.stop();
            long stopMs = (System.nanoTime() - start) / 1_000_000;
            assertTrue(stopMs < 3_000, "stop() should not wait out the 30 s sleep, took " + stopMs + " ms");
            var resp = pending.get(5, TimeUnit.SECONDS);
            assertEquals(500, resp.statusCode());
            assertTrue(resp.body().contains("pine: execution error in operator \\\"sleep\\\": context canceled"),
                    resp.body());
        } finally {
            server.stop();
            java.nio.file.Files.deleteIfExists(cfg);
        }
    }

    // ---- operators ---------------------------------------------------------

    @Test
    void parallelExecutorSkippedShardsFailWithContextError() {
        RequestContext.Cancelable ctx = RequestContext.withCancel(RequestContext.background());
        ctx.cancel();
        List<Map<String, Object>> items = new ArrayList<>();
        for (int i = 0; i < 10; i++) {
            items.add(new LinkedHashMap<>(Map.of("v", (double) i)));
        }
        Operator writer = new Operator() {
            @Override
            public void init(OperatorParams params) {
            }

            @Override
            public void execute(RequestContext c, OperatorInput input, OperatorOutput output) {
                for (int i = 0; i < input.itemCount(); i++) {
                    output.setItem(i, "w", true);
                }
            }
        };
        ExecutorService pool = Executors.newVirtualThreadPerTaskExecutor();
        try {
            PineErrors.OperatorException e = assertThrows(PineErrors.OperatorException.class, () ->
                    ParallelExecutor.execute(ctx, writer, new OperatorInput(new LinkedHashMap<>(), items), 3, "w", pool));
            assertInstanceOf(PineErrors.ContextException.class, e);
            assertEquals("context canceled", e.getMessage());
        } finally {
            pool.shutdownNow();
        }
    }

    @Test
    void luaStopsBetweenItems() throws Exception {
        AllOperators.ensureRegistered();
        Map<String, Object> params = new LinkedHashMap<>();
        params.put("lua_script", "function f() return 1 end");
        params.put("function_for_item", "f");
        params.put("function_for_common", "");
        Operator lua = Registry.global().buildOperator("transform_by_lua", params);
        ((AbstractOperator) lua).setMetadata(List.of(), List.of(), List.of(), List.of("y"));
        List<Map<String, Object>> items = new ArrayList<>(List.of(new LinkedHashMap<>(), new LinkedHashMap<>()));
        RequestContext.Cancelable ctx = RequestContext.withCancel(RequestContext.background());
        ctx.cancel();
        PineErrors.OperatorException e = assertThrows(PineErrors.OperatorException.class, () ->
                lua.execute(ctx, new OperatorInput(new LinkedHashMap<>(), items), new OperatorOutput()));
        assertEquals("lua: item[0]: context canceled", e.getMessage());
        ((Closer) lua).close();
    }

    @Test
    void remoteCallStopsOnCancel() throws Exception {
        CountDownLatch requestArrived = new CountDownLatch(1);
        HttpServer slow = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        slow.createContext("/execute", exchange -> {
            requestArrived.countDown();
            try {
                Thread.sleep(10_000);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
            exchange.close();
        });
        slow.setExecutor(Executors.newVirtualThreadPerTaskExecutor());
        slow.start();
        try {
            AllOperators.ensureRegistered();
            int port = slow.getAddress().getPort();
            Map<String, Object> params = new LinkedHashMap<>();
            params.put("host", "127.0.0.1");
            params.put("port", port);
            params.put("allow_private", true);
            params.put("timeout", 30.0);
            Operator remote = Registry.global().buildOperator("transform_by_remote_pineapple", params);
            ((AbstractOperator) remote).setMetadata(List.of(), List.of(), List.of(), List.of());
            RequestContext.Cancelable ctx = RequestContext.withCancel(RequestContext.background());
            Thread.ofVirtual().start(() -> {
                try {
                    if (requestArrived.await(10, TimeUnit.SECONDS)) {
                        ctx.cancel();
                    }
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                }
            });
            long start = System.nanoTime();
            PineErrors.OperatorException e = assertThrows(PineErrors.OperatorException.class, () ->
                    remote.execute(ctx, new OperatorInput(new LinkedHashMap<>(), new ArrayList<>()), new OperatorOutput()));
            long elapsedMs = (System.nanoTime() - start) / 1_000_000;
            assertEquals("transform_by_remote_pineapple: request failed: Post \"http://127.0.0.1:" + port
                    + "/execute\": context canceled", e.getMessage());
            assertTrue(elapsedMs < 5_000, "remote call should stop on cancel, took " + elapsedMs + " ms");
        } finally {
            slow.stop(0);
        }
    }
}
