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
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;

public class PineServer {
    private static final ObjectMapper mapper = GoFormat.createGoCompatMapper();
    private static final long DEFAULT_MAX_REQUEST_BODY_BYTES = 10 * 1024 * 1024; // 10 MB

    private final AtomicReference<Snapshot> snapshot = new AtomicReference<>();
    // Serializes stop() against an in-flight reload publishing a new snapshot.
    // Once closed is set no reload may re-publish; see loadConfig / stop.
    private final Object stopLock = new Object();
    private boolean closed;
    private final String configPath;
    private final int port;
    private final page.liam.pine.metrics.Provider metricsProvider;
    private long maxRequestBodyBytes = DEFAULT_MAX_REQUEST_BODY_BYTES;
    private HttpServer httpServer;
    private ExecutorService httpExecutor;
    private ScheduledExecutorService watcherExecutor;

    // Config hot-reload toggle. Defaults to true for backward compatibility;
    // setWatch(false) disables the watcher so config changes require a restart.
    // Mirrors Go's Config.Watch (nil/true enables, false disables).
    private boolean watch = true;
    // Guards load() so the engine/resource baseline is built exactly once,
    // whether reached via the embedding load() or via start().
    private boolean loaded;

    private final AtomicLong reloadCount = new AtomicLong();
    private final AtomicLong reloadErrorCount = new AtomicLong();
    private volatile long lastReloadDurationNs;

    private page.liam.pine.metrics.Counter reloadTotal;
    private page.liam.pine.metrics.Counter reloadErrorTotal;
    private page.liam.pine.metrics.Histogram reloadDuration;

    private final HttpStats httpStats = new HttpStats();

    private static final class Snapshot {
        final Engine engine;
        final ResourceProvider resources;
        // Aggregates resource-level Provider metrics for /stats. It is the
        // provider handed to this snapshot's ResourceManager, so it is recreated
        // on every hot-reload alongside the manager.
        final page.liam.pine.metrics.MetricsCollector resourceMetrics;

        // Reference count so that engine/resource teardown on retirement (config
        // hot-reload or shutdown) is deferred until every in-flight request that
        // captured this snapshot has finished — no request ever uses an operator
        // resource (e.g. a Redis connection pool) that has already been closed.
        // Starts at 1 (the live baseline held by the snapshot field); retiring
        // drops that baseline, and the last reference to reach zero runs teardown.
        private final AtomicInteger refs = new AtomicInteger(1);

        Snapshot(Engine engine, ResourceProvider resources, page.liam.pine.metrics.MetricsCollector resourceMetrics) {
            this.engine = engine;
            this.resources = resources;
            this.resourceMetrics = resourceMetrics;
        }

        // Takes an in-flight reference, returning false once the baseline has
        // been dropped and teardown is committed (count <= 0). The CAS only ever
        // increments a positive count, so a retired snapshot can never be
        // revived; the caller must fall back to the current live snapshot.
        boolean acquire() {
            for (;;) {
                int n = refs.get();
                if (n <= 0) {
                    return false;
                }
                if (refs.compareAndSet(n, n + 1)) {
                    return true;
                }
            }
        }

        // Drops one reference. When the count reaches zero (baseline dropped and
        // all in-flight references released) it runs teardown exactly once.
        void release() {
            if (refs.decrementAndGet() == 0) {
                teardown();
            }
        }

        private void teardown() {
            if (resources instanceof ResourceManager) {
                ((ResourceManager) resources).stop();
            }
            if (engine != null) {
                engine.close();
            }
        }
    }

    // Returns the current live snapshot with an in-flight reference held; the
    // caller must release() it when done. Retries if the snapshot is retired
    // between the read and the acquire, since a retirement always installs a new
    // live snapshot first. Returns null only before the initial snapshot is stored.
    private Snapshot acquireSnapshot() {
        for (;;) {
            Snapshot snap = snapshot.get();
            if (snap == null) {
                return null;
            }
            if (snap.acquire()) {
                return snap;
            }
        }
    }

    public PineServer(String configPath, int port) {
        this(configPath, port, null);
    }

    public PineServer(String configPath, int port, page.liam.pine.metrics.Provider metricsProvider) {
        this.configPath = configPath;
        this.port = port;
        this.metricsProvider = (metricsProvider != null)
                ? metricsProvider
                : page.liam.pine.metrics.NopProvider.getInstance();
        reloadTotal = this.metricsProvider.newCounter(
                new page.liam.pine.metrics.MetricOpts("pine_config_reload_total", "Config reload count"));
        reloadErrorTotal = this.metricsProvider.newCounter(
                new page.liam.pine.metrics.MetricOpts("pine_config_reload_errors_total", "Config reload error count"));
        reloadDuration = this.metricsProvider.newHistogram(
                new page.liam.pine.metrics.HistogramOpts("pine_config_reload_duration_seconds", "Config reload duration", new double[]{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0}));
    }

    /**
     * Load the engine and resource baseline: parse the effective max request
     * body size, build the engine + ResourceManager from the config file, store
     * the live snapshot, and (unless {@link #setWatch(boolean)} disabled it)
     * start the config-reload watcher. Idempotent — a second call is a no-op.
     *
     * <p>This is the embedding entry point. Callers that only need the engine
     * (see {@link #execute} / {@link #acquire}) can call {@code load()} without
     * ever standing up the HTTP server; {@link #start()} calls it internally.
     * Mirrors Go's NewServer.
     */
    public synchronized void load() throws Exception {
        if (loaded) {
            return;
        }

        byte[] configData = Files.readAllBytes(Paths.get(configPath));

        // Read max_request_body_size from config if present.
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
            // If parsing fails for body size, keep default.
        }

        loadConfig(configData); // initial load — not counted as reload
        lastReloadDurationNs = 0;
        lastModified = Files.getLastModifiedTime(Paths.get(configPath)).toMillis();

        // Start the config-reload watcher unless it was disabled. Watch defaults
        // to enabled for backward compatibility; setWatch(false) turns it off so
        // config changes require a process restart.
        if (watch) {
            watcherExecutor = Executors.newSingleThreadScheduledExecutor();
            watcherExecutor.scheduleAtFixedRate(this::checkReload, 2, 2, TimeUnit.SECONDS);
        }

        loaded = true;
    }

    // Package-private accessor for tests: the scheduled watcher executor, or
    // null when the config-reload watcher was never started (setWatch(false)).
    ScheduledExecutorService watcherExecutorForTest() {
        return watcherExecutor;
    }

    public void start() throws Exception {
        List<Route> routesSnapshot;
        synchronized (this) {
            started = true;
            middlewares = Collections.unmodifiableList(middlewares);
            routes = Collections.unmodifiableList(routes);
            routesSnapshot = routes;
        }

        // Build the engine/resource baseline and start the watcher (idempotent).
        load();

        // If standing up the HTTP layer fails (e.g. port already bound), roll
        // back what load() started so the caller is not left with a live
        // watcher thread and snapshot baseline. Mirrors Go Run()'s defer Close.
        try {
            httpServer = HttpServer.create(new InetSocketAddress(port), 0);
            httpExecutor = Executors.newFixedThreadPool(
                    Runtime.getRuntime().availableProcessors() * 2);
            httpServer.setExecutor(httpExecutor);

            httpServer.createContext("/health", wrapHandler("/health", this::handleHealth));
            httpServer.createContext("/execute", wrapHandler("/execute", this::handleExecute));
            httpServer.createContext("/stats", wrapHandler("/stats", this::handleStats));
            httpServer.createContext("/dag", wrapHandler("/dag", this::handleDAG));
            httpServer.createContext("/", wrapHandler("_other", this::handleNotFound));

            // Register custom routes. The path label handed to the metrics wrapper is
            // the route's own exact path, keeping HTTP metrics cardinality bounded.
            for (Route route : routesSnapshot) {
                httpServer.createContext(route.path, wrapHandler(route.path, routeHandler(route)));
            }

            httpServer.start();
        } catch (Exception e) {
            stop();
            throw e;
        }
    }

    @FunctionalInterface
    public interface Middleware {
        com.sun.net.httpserver.HttpHandler wrap(com.sun.net.httpserver.HttpHandler next);
    }

    private List<Middleware> middlewares = new ArrayList<>();
    private volatile boolean started;

    /**
     * Register a middleware. Must be called before {@link #start()}.
     * Concurrent registration is not a supported use pattern.
     */
    public synchronized void addMiddleware(Middleware mw) {
        if (started) {
            throw new IllegalStateException("cannot add middleware after server has started");
        }
        this.middlewares.add(mw);
    }

    // --- Custom routes (issue #169) --------------------------------------
    //
    // Mirror of pine-go's Config.Routes: an Ingress converts the raw HTTP
    // exchange into an ExecRequest (common + items), the server runs it against
    // the live snapshot, and an Egress owns the entire HTTP response.

    /**
     * Ingress converts an incoming HTTP request into an {@link ExecRequest}.
     * It reads the request body itself (parallel to Go's Ingress receiving the
     * raw {@code *http.Request}). Throwing aborts execution; the route's Egress
     * is then invoked with a null result and the thrown exception.
     */
    @FunctionalInterface
    public interface Ingress {
        ExecRequest apply(HttpExchange exchange) throws Exception;
    }

    /**
     * Egress writes the pipeline outcome to the response. It receives the engine
     * result (null on ingress error) and any error, and owns the whole HTTP
     * response — status, body, and closing the response body.
     */
    @FunctionalInterface
    public interface Egress {
        void apply(HttpExchange exchange, Engine.Result result, Exception error) throws IOException;
    }

    /**
     * ExecRequest is the request an {@link Ingress} builds for the engine.
     * Either field may be null, which is treated as empty.
     */
    public static final class ExecRequest {
        public final Map<String, Object> common;
        public final List<Map<String, Object>> items;

        public ExecRequest(Map<String, Object> common, List<Map<String, Object>> items) {
            this.common = common;
            this.items = items;
        }
    }

    /** Route declares a custom endpoint layered on top of the Pine engine. */
    public static final class Route {
        public final String method; // empty/null means any method
        public final String path;   // exact path, e.g. "/api/v1/report"
        public final Ingress ingress;
        public final Egress egress;

        public Route(String method, String path, Ingress ingress, Egress egress) {
            this.method = method;
            this.path = path;
            this.ingress = ingress;
            this.egress = egress;
        }
    }

    private List<Route> routes = new ArrayList<>();

    /**
     * Enable or disable config hot-reload. Must be called before {@link #load()}
     * or {@link #start()}. Defaults to true; passing false means config changes
     * require a process restart. Mirrors Go's Config.Watch.
     */
    public synchronized void setWatch(boolean enabled) {
        if (started || loaded) {
            throw new IllegalStateException("cannot set watch after server has loaded");
        }
        this.watch = enabled;
    }

    /**
     * Register a custom route. Must be called before {@link #start()}.
     * Validation and error wording mirror pine-go's validateRoutes exactly.
     */
    public synchronized void addRoute(Route route) {
        if (started) {
            throw new IllegalStateException("cannot add route after server has started");
        }
        String path = route.path;
        if (path == null || path.isEmpty() || path.charAt(0) != '/') {
            throw new IllegalArgumentException(
                    "custom route path " + goQuote(path) + " must start with '/'");
        }
        if (path.equals("/")) {
            throw new IllegalArgumentException(
                    "custom route path \"/\" conflicts with the built-in not-found handler");
        }
        if (isBuiltinPath(path)) {
            throw new IllegalArgumentException(
                    "custom route " + goQuote(path) + " conflicts with built-in endpoint");
        }
        for (Route existing : routes) {
            if (existing.path.equals(path)) {
                throw new IllegalArgumentException("duplicate custom route " + goQuote(path));
            }
        }
        if (route.ingress == null) {
            throw new IllegalArgumentException("custom route " + goQuote(path) + " has nil Ingress");
        }
        if (route.egress == null) {
            throw new IllegalArgumentException("custom route " + goQuote(path) + " has nil Egress");
        }
        this.routes.add(route);
    }

    // The set of built-in endpoints custom routes may not shadow. "/" is
    // handled separately (it maps to the not-found handler).
    private static boolean isBuiltinPath(String path) {
        return "/execute".equals(path)
                || "/health".equals(path)
                || "/stats".equals(path)
                || "/dag".equals(path);
    }

    // Quote a string the way Go's %q verb does for the simple paths we accept
    // (surround with double quotes). Kept minimal on purpose: paths reaching
    // this point are user-supplied route paths, not arbitrary text.
    private static String goQuote(String s) {
        return "\"" + (s == null ? "" : s) + "\"";
    }

    /**
     * Run the pipeline against the live snapshot. This is the embedding entry
     * point mirroring Go's Server.Execute: it acquires an in-flight reference so
     * a concurrent hot-reload never tears down the engine/resources mid-run, and
     * releases it when done. Throws {@link IllegalStateException} with the
     * message "engine not loaded" (matching Go's ErrEngineNotLoaded) when no
     * snapshot is live.
     *
     * <p>Resources are bound to the engine at build time (unlike Go, which
     * injects them per-call via context), so this simply drives the held
     * snapshot's engine.
     */
    public Engine.Result execute(Map<String, Object> common, List<Map<String, Object>> items) {
        Snapshot snap = acquireSnapshot();
        if (snap == null) {
            throw new IllegalStateException("engine not loaded");
        }
        try {
            return snap.engine.execute(common, items);
        } finally {
            snap.release();
        }
    }

    /**
     * Handle is an acquired reference to the live snapshot. It keeps the engine
     * and resources alive (deferring hot-reload teardown) until {@link #release}
     * is called. Mirrors Go's Handle. Always call {@link #release}.
     */
    public final class Handle {
        private final Snapshot snap;

        private Handle(Snapshot snap) {
            this.snap = snap;
        }

        /** The engine bound to this snapshot. */
        public Engine engine() {
            return snap.engine;
        }

        /** The resource provider bound to this snapshot. */
        public ResourceProvider resources() {
            return snap.resources;
        }

        /**
         * The resource-level metrics collector for this snapshot, or null when
         * the snapshot was built without a dedicated collector.
         */
        public page.liam.pine.metrics.MetricsCollector resourceMetrics() {
            return snap.resourceMetrics;
        }

        /** Drop the in-flight reference. The Handle must not be used afterward. */
        public void release() {
            snap.release();
        }
    }

    /**
     * Acquire a {@link Handle} to the live snapshot with an in-flight reference
     * held, or null if no snapshot is live. The caller must call
     * {@link Handle#release()} when done. Mirrors Go's Server.Acquire.
     */
    public Handle acquire() {
        Snapshot snap = acquireSnapshot();
        if (snap == null) {
            return null;
        }
        return new Handle(snap);
    }

    /**
     * Wrap a custom {@link Route} into an HttpHandler: enforce the route method
     * (when set), cap the request body at the server-wide limit, run the
     * Ingress to build the request, execute it against the live snapshot, and
     * hand the result (or error) to the Egress, which owns the response.
     * Mirrors Go's Server.routeHandler.
     */
    private com.sun.net.httpserver.HttpHandler routeHandler(Route route) {
        return exchange -> {
            if (route.method != null && !route.method.isEmpty()
                    && !route.method.equals(exchange.getRequestMethod())) {
                sendResponse(exchange, 405, Map.of("error", "method not allowed"));
                return;
            }
            // Apply the request-body cap before user Ingress code can read the
            // body, so custom endpoints cannot bypass max_request_body_size.
            // A tripped cap responds 413 centrally (same bytes as /execute's
            // limit), never reaching Egress. Mirrors Go's MaxBytesReader.
            exchange.setStreams(
                    new LimitedBodyStream(exchange.getRequestBody(), maxRequestBodyBytes), null);
            ExecRequest req;
            try {
                req = route.ingress.apply(exchange);
            } catch (BodyLimitExceededException e) {
                sendResponse(exchange, 413, Map.of("error", "request body too large"));
                return;
            } catch (Exception e) {
                route.egress.apply(exchange, null, e);
                return;
            }
            Map<String, Object> common = req != null ? req.common : null;
            List<Map<String, Object>> items = req != null ? req.items : null;
            try {
                Engine.Result result = execute(common, items);
                route.egress.apply(exchange, result, null);
            } catch (IOException e) {
                // Egress already owns the response; a write failure here has
                // nowhere left to go. Rethrow so the HttpServer logs it.
                throw e;
            } catch (Exception e) {
                route.egress.apply(exchange, null, e);
            }
        };
    }

    /** Thrown by {@link LimitedBodyStream} when a read exceeds the body cap. */
    static final class BodyLimitExceededException extends IOException {
        BodyLimitExceededException() {
            super("request body too large");
        }
    }

    /**
     * InputStream wrapper that fails the read crossing the configured limit.
     * A body of exactly {@code limit} bytes is allowed; byte {@code limit + 1}
     * throws {@link BodyLimitExceededException} (same boundary semantics as
     * Go's http.MaxBytesReader).
     */
    private static final class LimitedBodyStream extends java.io.FilterInputStream {
        private long remaining;

        LimitedBodyStream(java.io.InputStream in, long limit) {
            super(in);
            this.remaining = limit;
        }

        @Override
        public int read() throws IOException {
            int b = super.read();
            if (b != -1 && --remaining < 0) {
                throw new BodyLimitExceededException();
            }
            return b;
        }

        @Override
        public int read(byte[] buf, int off, int len) throws IOException {
            int n = super.read(buf, off, len);
            if (n > 0) {
                remaining -= n;
                if (remaining < 0) {
                    throw new BodyLimitExceededException();
                }
            }
            return n;
        }
    }

    private com.sun.net.httpserver.HttpHandler wrapHandler(String path, com.sun.net.httpserver.HttpHandler handler) {
        // Enforce exact path matching for named endpoints.
        // HttpServer uses longest-prefix matching; without this guard,
        // /health/sub/path would match the /health context.
        com.sun.net.httpserver.HttpHandler exactHandler;
        if ("_other".equals(path)) {
            exactHandler = handler;
        } else {
            exactHandler = exchange -> {
                if (!path.equals(exchange.getRequestURI().getPath())) {
                    handleNotFound(exchange);
                    return;
                }
                handler.handle(exchange);
            };
        }
        com.sun.net.httpserver.HttpHandler wrapped = exactHandler;
        // HTTP metrics (innermost) -- always wrap; metricsProvider is never
        // null after the constructor (NopProvider tied-off).
        wrapped = httpMetricsMiddleware(path, wrapped);
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
                long elapsedNs = System.nanoTime() - start;
                double duration = elapsedNs / 1_000_000_000.0;
                String method = exchange.getRequestMethod();
                String status = statusBucket(exchange.getResponseCode());
                httpRequestsTotal.with(method, path, status).inc();
                httpRequestDuration.with(method, path).observe(duration);
                httpStats.recordRequest(method, path, status, elapsedNs);
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
            // Wait for an in-flight checkReload to finish: shutdownNow only
            // interrupts, and loadConfig does not poll the interrupt flag, so
            // without this join a reload could still be running concurrently
            // with (and after) the teardown below.
            try {
                if (!watcherExecutor.awaitTermination(10, TimeUnit.SECONDS)) {
                    System.err.println("[pine-server] watcher did not stop within 10s");
                }
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
        }
        if (httpServer != null) {
            httpServer.stop(5);
        }
        if (httpExecutor != null) {
            httpExecutor.shutdownNow();
        }
        // Tear down the live snapshot and clear the pointer so no post-stop
        // caller (e.g. the embedding execute()/acquire()) spins forever on a
        // retired snapshot. A hot-reload may have swapped it, so the current one
        // (not a captured startup value) is what must be released. Dropping the
        // baseline reference runs teardown once the last in-flight request that
        // captured it releases its reference. Mirrors Go's Close() Swap(nil).
        // closed (under stopLock) prevents any straggler reload that survived
        // the join above from re-publishing a snapshot after this point.
        synchronized (stopLock) {
            closed = true;
            Snapshot snap = snapshot.getAndSet(null);
            if (snap != null) {
                snap.release();
            }
        }
    }

    // Package-private for RouteTest, which simulates a reload racing stop().
    void loadConfig(byte[] configData) throws Exception {
        long start = System.nanoTime();
        page.liam.pine.operators.AllOperators.ensureRegistered();
        // Dedicated aggregating collector exposed under /stats.resources. The
        // ResourceManager writes through a tee into both the caller-injected
        // provider (e.g. Prometheus) and this collector, so resource metrics
        // reach Prometheus AND /stats while engine metrics (which use
        // metricsProvider directly) stay out of the resources subtree.
        page.liam.pine.metrics.MetricsCollector resourceMetrics = new page.liam.pine.metrics.MetricsCollector();
        ResourceManager rm = new ResourceManager(
                new page.liam.pine.metrics.TeeProvider(metricsProvider, resourceMetrics));
        rm.loadFromConfig(configData);
        try {
            rm.start();
            Config cfg = Config.load(configData);
            rm.validateDeps(cfg.pipelineConfig.operators);
            Engine engine = Engine.create(configData, rm, metricsProvider);
            Snapshot next = new Snapshot(engine, rm, resourceMetrics);
            // Publish under the stop lock: an in-flight reload that already
            // passed the mtime check must not re-publish a snapshot after
            // stop() cleared the reference — that snapshot would leak and
            // execute()/acquire() would spuriously come back to life.
            synchronized (stopLock) {
                if (closed) {
                    next.release();
                    throw new IllegalStateException("server stopped during reload");
                }
                Snapshot old = snapshot.getAndSet(next);
                if (old != null) {
                    // Drop the baseline reference. Teardown (resource stop +
                    // engine close) runs once the last in-flight request that
                    // captured the old snapshot releases its reference, so no
                    // request is ever served with a closed operator resource.
                    old.release();
                }
            }
        } catch (Exception e) {
            rm.stop();
            throw e;
        }
        lastReloadDurationNs = System.nanoTime() - start;
    }

    private void recordReload() {
        reloadCount.incrementAndGet();
        reloadTotal.inc();
        reloadDuration.observe(lastReloadDurationNs / 1_000_000_000.0);
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
            reloadErrorTotal.inc();
            System.err.println("[pine-server] reload failed: " + e.getMessage());
        }
    }

    private void handleNotFound(HttpExchange exchange) throws IOException {
        sendResponse(exchange, 404, Map.of("error", "not found"));
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

        Snapshot snap = acquireSnapshot();
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
            // Pre-sort data dict keys to mirror Go encoding/json behavior for
            // `map[string]any` (which sorts alphabetically), while leaving the
            // top-level response struct field order alone.
            resp.put("common", sortMapKeys(result.common));
            resp.put("items", sortItemKeys(result.items));

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
        } finally {
            snap.release();
        }
    }

    private void handleStats(HttpExchange exchange) throws IOException {
        if (!"GET".equals(exchange.getRequestMethod())) {
            sendResponse(exchange, 405, Map.of("error", "method not allowed"));
            return;
        }
        Snapshot snap = acquireSnapshot();
        if (snap == null) {
            sendResponse(exchange, 503, Map.of("error", "engine not loaded"));
            return;
        }
        try {
            Map<String, Object> resp = new LinkedHashMap<>();
            resp.put("operators", snap.engine.stats());
            resp.put("scheduler", snap.engine.schedulerStats());
            resp.put("server", serverStats());
            resp.put("http", httpStats.snapshot());
            if (snap.resourceMetrics != null) {
                resp.put("resources", snap.resourceMetrics.snapshot());
            }
            Map<String, Map<String, Long>> custom = snap.engine.operatorCustomStats();
            if (custom != null) {
                resp.put("operator_detail", custom);
            }
            sendResponse(exchange, 200, resp);
        } finally {
            snap.release();
        }
    }

    private void handleDAG(HttpExchange exchange) throws IOException {
        if (!"GET".equals(exchange.getRequestMethod())) {
            sendResponse(exchange, 405, Map.of("error", "method not allowed"));
            return;
        }

        Snapshot snap = acquireSnapshot();
        if (snap == null) {
            sendResponse(exchange, 503, Map.of("error", "engine not loaded"));
            return;
        }

        try {
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
        } finally {
            snap.release();
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

    /** Recursively sort map keys alphabetically (mirrors Go encoding/json for map[string]any). */
    @SuppressWarnings("unchecked")
    private static Map<String, Object> sortMapKeys(Map<String, Object> m) {
        if (m == null) return null;
        java.util.TreeMap<String, Object> sorted = new java.util.TreeMap<>();
        for (Map.Entry<String, Object> e : m.entrySet()) {
            Object v = e.getValue();
            if (v instanceof Map) {
                v = sortMapKeys((Map<String, Object>) v);
            } else if (v instanceof List) {
                v = sortListElements((List<Object>) v);
            }
            sorted.put(e.getKey(), v);
        }
        return sorted;
    }

    @SuppressWarnings("unchecked")
    private static List<Object> sortListElements(List<Object> list) {
        if (list == null) return null;
        List<Object> out = new java.util.ArrayList<>(list.size());
        for (Object v : list) {
            if (v instanceof Map) {
                out.add(sortMapKeys((Map<String, Object>) v));
            } else if (v instanceof List) {
                out.add(sortListElements((List<Object>) v));
            } else {
                out.add(v);
            }
        }
        return out;
    }

    @SuppressWarnings("unchecked")
    private static List<Map<String, Object>> sortItemKeys(List<Map<String, Object>> items) {
        if (items == null) return null;
        List<Map<String, Object>> out = new java.util.ArrayList<>(items.size());
        for (Map<String, Object> it : items) {
            out.add(sortMapKeys(it));
        }
        return out;
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
        boolean watch = !"false".equals(System.getProperty("pine.watch", "true"));
        boolean demoRoutes = "true".equals(System.getProperty("pine.demoRoutes", "false"));

        PineServer server;
        try {
            server = new PineServer(configPath, port);
            server.setWatch(watch);
            if (demoRoutes) {
                registerDemoRoutes(server);
            }
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

    // Register a demonstration custom route (POST /api/echo) showing the
    // Ingress/Egress contract. Enabled via -Dpine.demoRoutes=true. Kept in
    // main() land (not the library core) so it never runs in production unless
    // explicitly opted in. Mirrors the demo route in pine-go's cmd/server.
    private static void registerDemoRoutes(PineServer server) {
        server.addRoute(new Route(
                "POST",
                "/api/echo",
                exchange -> {
                    byte[] body = readLimitedBody(exchange.getRequestBody(), server.maxRequestBodyBytes);
                    if (body == null) {
                        throw new IllegalArgumentException("invalid request body");
                    }
                    Map<String, Object> parsed;
                    try {
                        parsed = mapper.readValue(body, new TypeReference<Map<String, Object>>() {});
                    } catch (Exception e) {
                        throw new IllegalArgumentException("invalid request body");
                    }
                    @SuppressWarnings("unchecked")
                    Map<String, Object> common = (Map<String, Object>) parsed.get("common");
                    @SuppressWarnings("unchecked")
                    List<Map<String, Object>> items =
                            (List<Map<String, Object>>) parsed.get("items");
                    return new ExecRequest(common, items);
                },
                (exchange, result, error) -> {
                    if (error != null) {
                        // Fixed wording — never leak exception detail to clients.
                        server.sendResponse(exchange, 400, Map.of("error", "invalid request body"));
                        return;
                    }
                    Map<String, Object> resp = new LinkedHashMap<>();
                    // Sort common keys recursively so the serialized bytes match
                    // Go's encoding/json for map[string]any.
                    resp.put("common", sortMapKeys(result.common));
                    server.sendResponse(exchange, 200, resp);
                }));
    }
}
