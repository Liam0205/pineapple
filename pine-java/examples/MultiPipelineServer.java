/*
 * Multi-Pipeline Server — Pine-Java Embedding Example
 * ===================================================
 *
 * One process, several pipelines, each bound to its own endpoint with its
 * own log prefix:
 *
 *   POST /api/feed    -> feed.json    (log_prefix "[feed] ")
 *   POST /api/search  -> search.json  (log_prefix "[search] ")
 *   POST /execute     -> 410 Gone     (legacy endpoint, deliberately retired)
 *
 * Instead of running the bundled single-pipeline PineServer (whose start()
 * owns /execute), each pipeline is an EMBEDDED runtime: PineServer.load()
 * builds the engine + resources + hot-reload watcher without any HTTP, and
 * PineServer.execute(common, items) runs a request against the live
 * reference-counted snapshot (issue #169 embedding API). The HTTP layer below
 * is a plain com.sun.net.httpserver mux the application owns — the same
 * pattern works under Spring/Vert.x by calling execute() from your handlers.
 *
 * Since issue #172 log_prefix is engine-scoped: lines emitted while the feed
 * pipeline executes (observe_log, [pine-debug]) carry "[feed] " while search
 * lines carry "[search] ", concurrently, in one process. No global state.
 *
 * /execute is kept only as a tombstone: it answers 410 Gone and points
 * callers at the named endpoints.
 *
 * Compile & run (from repo root; the default Maven build compiles this
 * example as a test source, so plain `mvn test-compile` catches rot):
 *
 *   cd pine-java && mvn -q test-compile
 *   CP="target/classes:target/test-classes:$(mvn -q dependency:build-classpath -Dmdep.outputFile=/dev/stdout | tail -1)"
 *   java -cp "$CP" page.liam.pine.examples.MultiPipelineServer \
 *        ../pine-go/examples/multi-pipeline/feed.json \
 *        ../pine-go/examples/multi-pipeline/search.json 8080
 *
 * Try:
 *
 *   curl -s -X POST localhost:8080/api/feed   -d '{"common":{"user_id":"u1"}}'
 *   curl -s -X POST localhost:8080/api/search -d '{"common":{"query":"tech"}}'
 *   curl -si -X POST localhost:8080/execute   -d '{}'   # 410 Gone
 */
package page.liam.pine.examples;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import page.liam.pine.Engine;
import page.liam.pine.PineErrors;
import page.liam.pine.PineServer;

import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

public class MultiPipelineServer {
    private static final ObjectMapper mapper = new ObjectMapper();

    public static void main(String[] args) throws Exception {
        String feedConfig = args.length > 0 ? args[0] : "feed.json";
        String searchConfig = args.length > 1 ? args[1] : "search.json";
        int port = args.length > 2 ? Integer.parseInt(args[2]) : 8080;

        // One embedded runtime per pipeline. load() builds the engine,
        // starts resources and the config hot-reload watcher — no HTTP.
        // Each config declares its own log_prefix; since issue #172 the
        // prefix scopes to its engine, so both runtimes below log under
        // different prefixes in the same process.
        PineServer feed = new PineServer(feedConfig, 0);
        feed.load();
        PineServer search = new PineServer(searchConfig, 0);
        search.load();

        HttpServer http = HttpServer.create(new InetSocketAddress(port), 0);
        // HttpServer contexts use longest-PREFIX matching: without a guard,
        // /api/feed/anything would run the feed pipeline. exact() rejects
        // sub-paths with 404, matching the bundled PineServer.wrapHandler
        // and the Go/C++ examples' exact-path dispatch.
        http.createContext("/api/feed", exact("/api/feed", exchange -> handlePipeline(exchange, feed)));
        http.createContext("/api/search", exact("/api/search", exchange -> handlePipeline(exchange, search)));
        // Legacy endpoint: kept so old callers get a clear migration signal
        // instead of a generic 404, but it no longer runs any pipeline.
        http.createContext("/execute", exact("/execute", exchange -> respond(exchange, 410,
                Map.of("error", "endpoint retired: use /api/feed or /api/search"))));
        http.start();
        System.out.println("listening on :" + port);

        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            http.stop(2);
            feed.stop();
            search.stop();
        }));
    }

    // Mirrors the bundled server's default request-body cap. An embedding
    // HTTP layer must keep this boundary itself — issue #169 treats the body
    // limit as a shared-dispatch-layer safety contract.
    private static final long MAX_BODY_BYTES = 10L * 1024 * 1024; // 10 MB

    // Adapts HTTP to one embedded pipeline runtime. execute() acquires the
    // live snapshot with an in-flight reference, so a concurrent hot-reload
    // never tears the engine down mid-request.
    private static void handlePipeline(HttpExchange exchange, PineServer rt) throws java.io.IOException {
        if (!"POST".equals(exchange.getRequestMethod())) {
            respond(exchange, 405, Map.of("error", "method not allowed"));
            return;
        }
        byte[] body = readLimitedBody(exchange, MAX_BODY_BYTES);
        if (body == null) {
            respond(exchange, 413, Map.of("error", "request body too large"));
            return;
        }
        Map<String, Object> common;
        List<Map<String, Object>> items;
        try {
            Map<String, Object> parsed = mapper.readValue(body, new TypeReference<>() {});
            @SuppressWarnings("unchecked")
            Map<String, Object> c = (Map<String, Object>) parsed.get("common");
            @SuppressWarnings("unchecked")
            List<Map<String, Object>> it = (List<Map<String, Object>>) parsed.get("items");
            common = c != null ? c : Map.of();
            items = it != null ? it : List.of();
        } catch (Exception e) {
            respond(exchange, 400, Map.of("error", "invalid request body"));
            return;
        }

        try {
            Engine.Result result = rt.execute(common, items);
            Map<String, Object> resp = new LinkedHashMap<>();
            resp.put("common", result.common);
            resp.put("items", result.items);
            // Operator failures are RETURNED in result.error, not thrown —
            // the Java throw/return split (validation throws, execution
            // returns). Mirror the bundled handler: error != null -> 500.
            if (result.error != null) {
                if (result.error instanceof PineErrors.PanicError) {
                    // Log the stack trace server-side; clients get the safe message.
                    System.err.println("[example] PANIC: "
                            + ((PineErrors.PanicError) result.error).detailedError());
                }
                resp.put("error", result.error.getMessage());
            }
            respond(exchange, result.error != null ? 500 : 200, resp);
        } catch (PineErrors.ValidationError e) {
            respond(exchange, 400, Map.of("error", e.getMessage()));
        } catch (Exception e) {
            respond(exchange, 500, Map.of("error", e.getMessage()));
        }
    }

    // Reads at most limit bytes; returns null once the body exceeds it (the
    // caller answers 413). Counting the stream keeps memory bounded without
    // trusting Content-Length.
    private static byte[] readLimitedBody(HttpExchange exchange, long limit) throws java.io.IOException {
        java.io.ByteArrayOutputStream buf = new java.io.ByteArrayOutputStream();
        byte[] chunk = new byte[8192];
        long total = 0;
        java.io.InputStream in = exchange.getRequestBody();
        int n;
        while ((n = in.read(chunk)) != -1) {
            total += n;
            if (total > limit) {
                return null;
            }
            buf.write(chunk, 0, n);
        }
        return buf.toByteArray();
    }

    // Enforces exact path matching over HttpServer's longest-prefix contexts.
    private static com.sun.net.httpserver.HttpHandler exact(
            String path, com.sun.net.httpserver.HttpHandler handler) {
        return exchange -> {
            if (!path.equals(exchange.getRequestURI().getPath())) {
                respond(exchange, 404, Map.of("error", "not found"));
                return;
            }
            handler.handle(exchange);
        };
    }

    private static void respond(HttpExchange exchange, int status, Object body) throws java.io.IOException {
        byte[] bytes = mapper.writeValueAsBytes(body);
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.sendResponseHeaders(status, bytes.length);
        try (OutputStream os = exchange.getResponseBody()) {
            os.write(bytes);
        }
    }
}
