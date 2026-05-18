package page.liam.pine;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;

import java.io.*;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;

public class PineServer {
    private static final ObjectMapper mapper = GoFormat.createGoCompatMapper();
    private static final long DEFAULT_MAX_REQUEST_BODY_BYTES = 10 * 1024 * 1024; // 10 MB

    private final AtomicReference<Snapshot> snapshot = new AtomicReference<>();
    private final String configPath;
    private final int port;
    private final page.liam.pine.metrics.Provider metricsProvider;
    private long maxRequestBodyBytes = DEFAULT_MAX_REQUEST_BODY_BYTES;
    private HttpServer httpServer;
    private ScheduledExecutorService watcherExecutor;

    private final AtomicLong reloadCount = new AtomicLong();
    private final AtomicLong reloadErrorCount = new AtomicLong();
    private volatile long lastReloadDurationNs;

    private page.liam.pine.metrics.Counter reloadTotal;
    private page.liam.pine.metrics.Counter reloadErrorTotal;
    private page.liam.pine.metrics.Histogram reloadDuration;

    private static class Snapshot {
        final Engine engine;
        final ResourceProvider resources;
        Snapshot(Engine engine, ResourceProvider resources) {
            this.engine = engine;
            this.resources = resources;
        }
    }

    public PineServer(String configPath, int port) {
        this(configPath, port, null);
    }

    public PineServer(String configPath, int port, page.liam.pine.metrics.Provider metricsProvider) {
        this.configPath = configPath;
        this.port = port;
        this.metricsProvider = metricsProvider;
        if (metricsProvider != null) {
            reloadTotal = metricsProvider.newCounter(
                    new page.liam.pine.metrics.MetricOpts("pine_config_reload_total", "Config reload count"));
            reloadErrorTotal = metricsProvider.newCounter(
                    new page.liam.pine.metrics.MetricOpts("pine_config_reload_errors_total", "Config reload error count"));
            reloadDuration = metricsProvider.newHistogram(
                    new page.liam.pine.metrics.HistogramOpts("pine_config_reload_duration_seconds", "Config reload duration", new double[]{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0}));
        }
    }

    public void start() throws Exception {
        synchronized (this) {
            started = true;
            middlewares = Collections.unmodifiableList(middlewares);
        }

        byte[] configData = Files.readAllBytes(Paths.get(configPath));
        loadConfig(configData); // initial load — not counted as reload
        lastReloadDurationNs = 0;
        lastModified = Files.getLastModifiedTime(Paths.get(configPath)).toMillis();

        // Read max_request_body_size from config if present
        try {
            Map<String, Object> rawConfig = mapper.readValue(configData, new TypeReference<Map<String, Object>>() {});
            Object bodySize = rawConfig.get("max_request_body_size");
            if (bodySize instanceof Number) {
                long size = ((Number) bodySize).longValue();
                if (size > 0) {
                    this.maxRequestBodyBytes = size;
                }
            }
        } catch (Exception ignored) {
            // If parsing fails for body size, keep default
        }

        httpServer = HttpServer.create(new InetSocketAddress(port), 0);
        httpServer.setExecutor(Executors.newFixedThreadPool(
                Runtime.getRuntime().availableProcessors() * 2));

        httpServer.createContext("/health", wrapHandler("/health", this::handleHealth));
        httpServer.createContext("/execute", wrapHandler("/execute", this::handleExecute));
        httpServer.createContext("/stats", wrapHandler("/stats", this::handleStats));
        httpServer.createContext("/dag", wrapHandler("/dag", this::handleDAG));

        httpServer.start();

        watcherExecutor = Executors.newSingleThreadScheduledExecutor();
        watcherExecutor.scheduleAtFixedRate(this::checkReload, 2, 2, TimeUnit.SECONDS);
    }

    @FunctionalInterface
    public interface Middleware {
        com.sun.net.httpserver.HttpHandler wrap(com.sun.net.httpserver.HttpHandler next);
    }

    private List<Middleware> middlewares = new ArrayList<>();
    private volatile boolean started;

    /**
     * Register a middleware. Must be called before {@link #start()};
     * calling after start throws IllegalStateException.
     * Not thread-safe — all registrations must happen on a single thread.
     */
    public synchronized void addMiddleware(Middleware mw) {
        if (started) {
            throw new IllegalStateException("cannot add middleware after server has started");
        }
        this.middlewares.add(mw);
    }

    private com.sun.net.httpserver.HttpHandler wrapHandler(String path, com.sun.net.httpserver.HttpHandler handler) {
        com.sun.net.httpserver.HttpHandler wrapped = handler;
        // HTTP metrics (innermost)
        if (metricsProvider != null) {
            wrapped = httpMetricsMiddleware(path, wrapped);
        }
        // User middlewares (outer to inner)
        for (int i = middlewares.size() - 1; i >= 0; i--) {
            wrapped = middlewares.get(i).wrap(wrapped);
        }
        return wrapped;
    }

    private page.liam.pine.metrics.Counter httpRequestsTotal;
    private page.liam.pine.metrics.Histogram httpRequestDuration;

    private com.sun.net.httpserver.HttpHandler httpMetricsMiddleware(String path, com.sun.net.httpserver.HttpHandler next) {
        if (httpRequestsTotal == null) {
            httpRequestsTotal = metricsProvider.newCounter(
                    new page.liam.pine.metrics.MetricOpts("pine_http_requests_total", "HTTP request count", "method", "path", "status"));
            httpRequestDuration = metricsProvider.newHistogram(
                    new page.liam.pine.metrics.HistogramOpts("pine_http_request_duration_seconds", "HTTP request duration", new double[]{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}, "method", "path"));
        }
        return exchange -> {
            long start = System.nanoTime();
            try {
                next.handle(exchange);
            } finally {
                double duration = (System.nanoTime() - start) / 1_000_000_000.0;
                String method = exchange.getRequestMethod();
                String status = statusBucket(exchange.getResponseCode());
                httpRequestsTotal.with(method, path, status).inc();
                httpRequestDuration.with(method, path).observe(duration);
            }
        };
    }

    private static String statusBucket(int code) {
        if (code >= 500) return "5xx";
        if (code >= 400) return "4xx";
        if (code >= 300) return "3xx";
        if (code >= 200) return "2xx";
        return "other";
    }

    public void stop() {
        if (watcherExecutor != null) {
            watcherExecutor.shutdownNow();
        }
        if (httpServer != null) {
            httpServer.stop(5); // waits up to 5s for in-flight exchanges to complete
        }
    }

    private void loadConfig(byte[] configData) throws Exception {
        long start = System.nanoTime();
        ResourceManager rm = new ResourceManager();
        rm.loadFromConfig(configData);
        try {
            rm.start();
            Config cfg = Config.load(configData);
            rm.validateDeps(cfg.pipelineConfig.operators);
            Engine engine = Engine.create(configData, rm);
            Snapshot old = snapshot.getAndSet(new Snapshot(engine, rm));
            if (old != null && old.resources instanceof ResourceManager) {
                ((ResourceManager) old.resources).stop();
            }
        } catch (Exception e) {
            rm.stop();
            throw e;
        }
        lastReloadDurationNs = System.nanoTime() - start;
    }

    private void recordReload() {
        reloadCount.incrementAndGet();
        if (reloadTotal != null) {
            reloadTotal.inc();
            reloadDuration.observe(lastReloadDurationNs / 1_000_000_000.0);
        }
    }

    private volatile long lastModified = 0;

    private void checkReload() {
        try {
            long mod = Files.getLastModifiedTime(Paths.get(configPath)).toMillis();
            if (mod > lastModified) {
                lastModified = mod;
                byte[] data = Files.readAllBytes(Paths.get(configPath));
                loadConfig(data);
                recordReload();
            }
        } catch (Exception e) {
            reloadErrorCount.incrementAndGet();
            if (reloadErrorTotal != null) reloadErrorTotal.inc();
            System.err.println("[pine-server] reload failed: " + e.getMessage());
        }
    }

    private void handleHealth(HttpExchange exchange) throws IOException {
        if (!"GET".equals(exchange.getRequestMethod())) {
            sendResponse(exchange, 405, Map.of("error", "method not allowed"));
            return;
        }
        sendResponse(exchange, 200, Map.of("status", "ok"));
    }

    private void handleExecute(HttpExchange exchange) throws IOException {
        if (!"POST".equals(exchange.getRequestMethod())) {
            sendResponse(exchange, 405, Map.of("error", "method not allowed"));
            return;
        }

        Snapshot snap = snapshot.get();
        if (snap == null) {
            sendResponse(exchange, 503, Map.of("error", "engine not loaded"));
            return;
        }

        try {
            byte[] body = readLimitedBody(exchange.getRequestBody(), maxRequestBodyBytes);
            if (body == null) {
                sendResponse(exchange, 413, Map.of("error", "request body too large"));
                return;
            }
            Map<String, Object> req = mapper.readValue(body, new TypeReference<Map<String, Object>>() {});

            @SuppressWarnings("unchecked")
            Map<String, Object> common = (Map<String, Object>) req.get("common");
            @SuppressWarnings("unchecked")
            List<Map<String, Object>> items = (List<Map<String, Object>>) req.getOrDefault("items", Collections.emptyList());

            Engine.Result result = snap.engine.execute(common, items);

            Map<String, Object> resp = new LinkedHashMap<>();
            resp.put("common", result.common);
            resp.put("items", result.items);

            if (result.warnings != null && !result.warnings.isEmpty()) {
                List<String> warnList = new ArrayList<>();
                for (Engine.Warning w : result.warnings) {
                    warnList.add("operator \"" + w.operator + "\": " + w.err.getMessage());
                }
                resp.put("warnings", warnList);
            }

            Object returnTrace = common.get("_return_trace");
            if (Boolean.TRUE.equals(returnTrace) && result.trace != null) {
                List<Map<String, Object>> traceList = new ArrayList<>();
                for (OpTrace t : result.trace) {
                    Map<String, Object> tm = new LinkedHashMap<>();
                    tm.put("name", t.name);
                    tm.put("duration_ms", (t.durationNs / 1000) / 1000.0);
                    if (t.skipped) {
                        tm.put("skipped", true);
                    }
                    if (t.inputSnapshot != null) {
                        tm.put("input_snapshot", t.inputSnapshot);
                    }
                    if (t.outputSnapshot != null) {
                        tm.put("output_snapshot", t.outputSnapshot);
                    }
                    traceList.add(tm);
                }
                resp.put("trace", traceList);
            }

            if (result.error != null) {
                if (result.error instanceof PineErrors.PanicError) {
                    System.err.println("[pine-server] PANIC: " + ((PineErrors.PanicError) result.error).detailedError());
                }
                resp.put("error", result.error.getMessage());
            }

            sendResponse(exchange, result.error != null ? 500 : 200, resp);

        } catch (com.fasterxml.jackson.core.JsonProcessingException e) {
            sendResponse(exchange, 400, Map.of("error", "invalid request: " + e.getMessage()));
        } catch (IllegalArgumentException e) {
            Map<String, Object> errResp = new LinkedHashMap<>();
            errResp.put("common", null);
            errResp.put("items", null);
            errResp.put("error", e.getMessage());
            sendResponse(exchange, 400, errResp);
        } catch (Exception e) {
            sendResponse(exchange, 500, Map.of("error", e.getMessage()));
        }
    }

    private void handleStats(HttpExchange exchange) throws IOException {
        if (!"GET".equals(exchange.getRequestMethod())) {
            sendResponse(exchange, 405, Map.of("error", "method not allowed"));
            return;
        }
        Snapshot snap = snapshot.get();
        if (snap == null) {
            sendResponse(exchange, 503, Map.of("error", "engine not loaded"));
            return;
        }
        Map<String, Object> resp = new LinkedHashMap<>();
        resp.put("operators", snap.engine.stats());
        resp.put("scheduler", snap.engine.schedulerStats());
        Map<String, Map<String, Long>> custom = snap.engine.operatorCustomStats();
        if (custom != null) {
            resp.put("operator_detail", custom);
        }
        resp.put("server", serverStats());
        sendResponse(exchange, 200, resp);
    }

    private void handleDAG(HttpExchange exchange) throws IOException {
        if (!"GET".equals(exchange.getRequestMethod())) {
            sendResponse(exchange, 405, Map.of("error", "method not allowed"));
            return;
        }

        Snapshot snap = snapshot.get();
        if (snap == null) {
            sendResponse(exchange, 503, Map.of("error", "engine not loaded"));
            return;
        }

        String query = exchange.getRequestURI().getQuery();
        String format = "dot";
        int collapse = 0;
        if (query != null) {
            for (String param : query.split("&")) {
                String[] kv = param.split("=", 2);
                if (kv.length == 2) {
                    if ("format".equals(kv[0])) format = kv[1];
                    else if ("collapse".equals(kv[0])) {
                        try { collapse = Integer.parseInt(kv[1]); }
                        catch (NumberFormatException e) {
                            sendResponse(exchange, 400, Map.of("error", "collapse must be a non-negative integer"));
                            return;
                        }
                        if (collapse < 0) {
                            sendResponse(exchange, 400, Map.of("error", "collapse must be a non-negative integer"));
                            return;
                        }
                    }
                }
            }
        }

        try {
            String output = snap.engine.renderDAG(format, collapse);

            String contentType = "dot".equals(format) ? "text/vnd.graphviz" : "text/plain";
            byte[] responseBytes = output.getBytes(StandardCharsets.UTF_8);
            exchange.getResponseHeaders().set("Content-Type", contentType + "; charset=utf-8");
            exchange.sendResponseHeaders(200, responseBytes.length);
            try (OutputStream os = exchange.getResponseBody()) {
                os.write(responseBytes);
            }
        } catch (Exception e) {
            sendResponse(exchange, 400, Map.of("error", e.getMessage()));
        }
    }

    private Map<String, Object> serverStats() {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("reload_count", reloadCount.get());
        m.put("reload_error_count", reloadErrorCount.get());
        m.put("last_reload_duration_ns", lastReloadDurationNs);
        return m;
    }

    private void sendResponse(HttpExchange exchange, int status, Object body) throws IOException {
        byte[] responseBytes = mapper.writeValueAsBytes(body);
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.sendResponseHeaders(status, responseBytes.length + 1);
        try (OutputStream os = exchange.getResponseBody()) {
            os.write(responseBytes);
            os.write('\n');
        }
    }

    private static byte[] readLimitedBody(java.io.InputStream in, long limit) throws IOException {
        java.io.ByteArrayOutputStream buf = new java.io.ByteArrayOutputStream();
        byte[] tmp = new byte[8192];
        long total = 0;
        int n;
        while ((n = in.read(tmp)) != -1) {
            total += n;
            if (total > limit) {
                return null;
            }
            buf.write(tmp, 0, n);
        }
        return buf.toByteArray();
    }

    public static void main(String[] args) {
        String configPath = System.getProperty("pine.config", "config.json");
        int port = Integer.parseInt(System.getProperty("pine.port", "8080"));

        PineServer server;
        try {
            server = new PineServer(configPath, port);
            server.start();
        } catch (Exception e) {
            System.err.println("fatal: " + e.getMessage());
            System.exit(1);
            return;
        }
        System.out.println("Pine server listening on :" + port);

        Runtime.getRuntime().addShutdownHook(new Thread(server::stop));
        try {
            Thread.currentThread().join();
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }
}
