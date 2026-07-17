package page.liam.pine;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.net.ServerSocket;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Tests for the issue #169 upstreaming: custom Routes (Ingress/Egress), the
 * watch toggle, and the embedding API (load / execute / acquire). Mirrors
 * pine-go's routes_test.go.
 */
public class RouteTest {
    private static final ObjectMapper mapper = new ObjectMapper();

    // Minimal pipeline config mirroring ServerTest: recall_static seeds two
    // items, transform_copy maps score -> final_score. common input/output are
    // empty so execute({}, []) runs the full pipeline.
    private static final String MINIMAL_CONFIG = "{"
            + "\"pipeline_config\": {"
            + "  \"operators\": {"
            + "    \"recall\": {"
            + "      \"type_name\": \"recall_static\","
            + "      \"items\": ["
            + "        {\"item_id\": \"a\", \"score\": 1.0},"
            + "        {\"item_id\": \"b\", \"score\": 2.0}"
            + "      ],"
            + "      \"$metadata\": {"
            + "        \"common_input\": [],"
            + "        \"common_output\": [],"
            + "        \"item_input\": [],"
            + "        \"item_output\": [\"item_id\", \"score\"]"
            + "      }"
            + "    },"
            + "    \"copy\": {"
            + "      \"type_name\": \"transform_copy\","
            + "      \"direction\": \"item_to_item\","
            + "      \"$metadata\": {"
            + "        \"common_input\": [],"
            + "        \"common_output\": [],"
            + "        \"item_input\": [\"score\"],"
            + "        \"item_output\": [\"final_score\"]"
            + "      }"
            + "    }"
            + "  }"
            + "},"
            + "\"pipeline_group\": {"
            + "  \"main\": {\"pipeline\": [\"recall\", \"copy\"]}"
            + "},"
            + "\"flow_contract\": {"
            + "  \"common_input\": [],"
            + "  \"common_output\": [],"
            + "  \"item_output\": [\"item_id\", \"final_score\"]"
            + "}"
            + "}";

    private static Path writeTempConfig() throws IOException {
        Path f = Files.createTempFile("pine-route-test-", ".json");
        Files.write(f, MINIMAL_CONFIG.getBytes(StandardCharsets.UTF_8));
        f.toFile().deleteOnExit();
        return f;
    }

    private static PineServer newServer() throws IOException {
        return new PineServer(writeTempConfig().toString(), 0);
    }

    private static int findFreePort() throws IOException {
        try (ServerSocket socket = new ServerSocket(0)) {
            return socket.getLocalPort();
        }
    }

    private static final PineServer.Ingress PASSTHROUGH_INGRESS =
            exchange -> new PineServer.ExecRequest(Map.of(), List.of());
    private static final PineServer.Egress DISCARD_EGRESS =
            (exchange, result, error) -> { };

    // --- addRoute validation (mirrors Go TestValidateRoutes) --------------

    @Test
    void testAddRouteValid() throws IOException {
        PineServer s = newServer();
        assertDoesNotThrow(() -> s.addRoute(new PineServer.Route(
                "POST", "/api/v1/report", PASSTHROUGH_INGRESS, DISCARD_EGRESS)));
    }

    @Test
    void testAddRouteEmptyPath() throws IOException {
        PineServer s = newServer();
        IllegalArgumentException e = assertThrows(IllegalArgumentException.class,
                () -> s.addRoute(new PineServer.Route("", "", PASSTHROUGH_INGRESS, DISCARD_EGRESS)));
        assertTrue(e.getMessage().contains("must start with '/'"), e.getMessage());
        assertEquals("custom route path \"\" must start with '/'", e.getMessage());
    }

    @Test
    void testAddRouteRelativePath() throws IOException {
        PineServer s = newServer();
        IllegalArgumentException e = assertThrows(IllegalArgumentException.class,
                () -> s.addRoute(new PineServer.Route("", "api", PASSTHROUGH_INGRESS, DISCARD_EGRESS)));
        assertTrue(e.getMessage().contains("must start with '/'"), e.getMessage());
        assertEquals("custom route path \"api\" must start with '/'", e.getMessage());
    }

    @Test
    void testAddRouteRootPath() throws IOException {
        PineServer s = newServer();
        IllegalArgumentException e = assertThrows(IllegalArgumentException.class,
                () -> s.addRoute(new PineServer.Route("", "/", PASSTHROUGH_INGRESS, DISCARD_EGRESS)));
        assertTrue(e.getMessage().contains("not-found handler"), e.getMessage());
        assertEquals("custom route path \"/\" conflicts with the built-in not-found handler", e.getMessage());
    }

    @Test
    void testAddRouteConflictsWithBuiltins() throws IOException {
        for (String builtin : new String[]{"/execute", "/health", "/stats", "/dag"}) {
            PineServer s = newServer();
            IllegalArgumentException e = assertThrows(IllegalArgumentException.class,
                    () -> s.addRoute(new PineServer.Route("", builtin, PASSTHROUGH_INGRESS, DISCARD_EGRESS)));
            assertTrue(e.getMessage().contains("conflicts with built-in"), e.getMessage());
            assertEquals("custom route \"" + builtin + "\" conflicts with built-in endpoint", e.getMessage());
        }
    }

    @Test
    void testAddRouteDuplicate() throws IOException {
        PineServer s = newServer();
        s.addRoute(new PineServer.Route("POST", "/api/v1/report", PASSTHROUGH_INGRESS, DISCARD_EGRESS));
        IllegalArgumentException e = assertThrows(IllegalArgumentException.class,
                () -> s.addRoute(new PineServer.Route("POST", "/api/v1/report", PASSTHROUGH_INGRESS, DISCARD_EGRESS)));
        assertTrue(e.getMessage().contains("duplicate custom route"), e.getMessage());
        assertEquals("duplicate custom route \"/api/v1/report\"", e.getMessage());
    }

    @Test
    void testAddRouteNilIngress() throws IOException {
        PineServer s = newServer();
        IllegalArgumentException e = assertThrows(IllegalArgumentException.class,
                () -> s.addRoute(new PineServer.Route("", "/a", null, DISCARD_EGRESS)));
        assertTrue(e.getMessage().contains("nil Ingress"), e.getMessage());
        assertEquals("custom route \"/a\" has nil Ingress", e.getMessage());
    }

    @Test
    void testAddRouteNilEgress() throws IOException {
        PineServer s = newServer();
        IllegalArgumentException e = assertThrows(IllegalArgumentException.class,
                () -> s.addRoute(new PineServer.Route("", "/a", PASSTHROUGH_INGRESS, null)));
        assertTrue(e.getMessage().contains("nil Egress"), e.getMessage());
        assertEquals("custom route \"/a\" has nil Egress", e.getMessage());
    }

    @Test
    void testAddRouteAfterStartThrows() throws Exception {
        PineServer s = newServer();
        s.setWatch(false);
        s.start();
        try {
            IllegalStateException e = assertThrows(IllegalStateException.class,
                    () -> s.addRoute(new PineServer.Route("", "/a", PASSTHROUGH_INGRESS, DISCARD_EGRESS)));
            assertTrue(e.getMessage().contains("after server has started"), e.getMessage());
        } finally {
            s.stop();
        }
    }

    // start() failing after load() (e.g. port already bound) must roll back the
    // watcher thread and snapshot baseline so the caller is not left with a
    // half-started server. Mirrors Go Run()'s defer Close.
    @Test
    void testStartFailureRollsBackLoad() throws Exception {
        try (ServerSocket occupier = new ServerSocket(0)) {
            PineServer s = new PineServer(writeTempConfig().toString(), occupier.getLocalPort());
            assertThrows(Exception.class, s::start);
            // load() side effects were rolled back: no live snapshot remains,
            // so the embedding API reports engine-not-loaded instead of
            // executing against a leaked baseline.
            IllegalStateException e = assertThrows(IllegalStateException.class,
                    () -> s.execute(Map.of(), List.of()));
            assertTrue(e.getMessage().contains("engine not loaded"), e.getMessage());
            assertNull(s.acquire(), "acquire() after failed start must return null");
        }
    }

    // A reload that publishes after stop() must not resurrect the server: the
    // closed flag (under the stop lock) makes the late publication release its
    // snapshot and fail, so execute()/acquire() stay engine-not-loaded.
    @Test
    void testReloadAfterStopDoesNotResurrectSnapshot() throws Exception {
        PineServer s = newServer();
        s.setWatch(false);
        s.load();
        byte[] config = Files.readAllBytes(writeTempConfig());
        s.stop();

        assertThrows(IllegalStateException.class, () -> s.loadConfig(config),
                "loadConfig after stop must refuse to publish");
        assertThrows(IllegalStateException.class, () -> s.execute(Map.of(), List.of()),
                "execute must stay engine-not-loaded after a late reload");
        assertNull(s.acquire(), "acquire must stay null after a late reload");
    }

    // --- Watch toggle -----------------------------------------------------

    @Test
    void testWatchDisabledDoesNotScheduleWatcher() throws Exception {
        PineServer s = newServer();
        s.setWatch(false);
        s.load();
        try {
            assertNull(s.watcherExecutorForTest(),
                    "setWatch(false) must not schedule the config watcher");
        } finally {
            s.stop();
        }
    }

    @Test
    void testWatchDefaultSchedulesWatcher() throws Exception {
        PineServer s = newServer();
        s.load();
        try {
            assertNotNull(s.watcherExecutorForTest(),
                    "default watch must schedule the config watcher");
        } finally {
            s.stop();
        }
    }

    @Test
    void testSetWatchAfterLoadThrows() throws Exception {
        PineServer s = newServer();
        s.setWatch(false);
        s.load();
        try {
            assertThrows(IllegalStateException.class, () -> s.setWatch(true));
        } finally {
            s.stop();
        }
    }

    // --- Embedding API: load / execute / acquire --------------------------

    @Test
    void testLoadAndExecute() throws Exception {
        PineServer s = newServer();
        s.setWatch(false);
        s.load();
        try {
            Engine.Result result = s.execute(Map.of(), List.of());
            assertNull(result.error, "pipeline should not error");
            assertEquals(2, result.items.size());
            // transform_copy maps score -> final_score for each recalled item.
            assertEquals(1.0, result.items.get(0).get("final_score"));
            assertEquals(2.0, result.items.get(1).get("final_score"));
        } finally {
            s.stop();
        }
    }

    @Test
    void testLoadIdempotent() throws Exception {
        PineServer s = newServer();
        s.setWatch(false);
        s.load();
        try {
            assertDoesNotThrow(s::load); // second load is a no-op
            Engine.Result result = s.execute(Map.of(), List.of());
            assertEquals(2, result.items.size());
        } finally {
            s.stop();
        }
    }

    @Test
    void testExecuteAfterStopThrows() throws Exception {
        PineServer s = newServer();
        s.setWatch(false);
        s.load();
        s.stop();
        IllegalStateException e = assertThrows(IllegalStateException.class,
                () -> s.execute(Map.of(), List.of()));
        assertEquals("engine not loaded", e.getMessage());
    }

    @Test
    void testExecuteBeforeLoadThrows() throws IOException {
        PineServer s = newServer();
        IllegalStateException e = assertThrows(IllegalStateException.class,
                () -> s.execute(Map.of(), List.of()));
        assertEquals("engine not loaded", e.getMessage());
    }

    @Test
    void testAcquireHandle() throws Exception {
        PineServer s = newServer();
        s.setWatch(false);
        s.load();
        try {
            PineServer.Handle h = s.acquire();
            assertNotNull(h, "expected a live Handle");
            assertNotNull(h.engine(), "Handle.engine() should not be null");
            assertNotNull(h.resources(), "Handle.resources() should not be null");

            // The Handle keeps the snapshot alive; the held engine executes.
            Engine.Result out = h.engine().execute(Map.of(), List.of());
            assertEquals(2, out.items.size());
            h.release();
        } finally {
            s.stop();
        }
    }

    @Test
    void testAcquireAfterStopReturnsNull() throws Exception {
        PineServer s = newServer();
        s.setWatch(false);
        s.load();
        s.stop();
        assertNull(s.acquire(), "acquire after stop should return null");
    }

    // --- routeHandler chain over a real HttpServer ------------------------

    @Test
    void testRouteHandlerSuccess() throws Exception {
        int port = findFreePort();
        PineServer s = new PineServer(writeTempConfig().toString(), port);
        s.setWatch(false);
        s.addRoute(new PineServer.Route(
                "POST",
                "/api/echo",
                exchange -> {
                    // Body is not required for this pipeline; run with empty input.
                    drain(exchange.getRequestBody());
                    return new PineServer.ExecRequest(Map.of(), List.of());
                },
                (exchange, result, error) -> {
                    if (error != null) {
                        writeJson(exchange, 500, "{\"error\":\"" + error.getMessage() + "\"}");
                        return;
                    }
                    writeJson(exchange, 200,
                            "{\"count\":" + result.items.size() + "}");
                }));
        s.start();
        HttpClient client = HttpClient.newHttpClient();
        try {
            HttpResponse<String> resp = client.send(
                    HttpRequest.newBuilder()
                            .uri(URI.create("http://localhost:" + port + "/api/echo"))
                            .header("Content-Type", "application/json")
                            .POST(HttpRequest.BodyPublishers.ofString("{}"))
                            .build(),
                    HttpResponse.BodyHandlers.ofString());
            assertEquals(200, resp.statusCode(), resp.body());
            Map<String, Object> body = mapper.readValue(resp.body(), new TypeReference<>() {});
            assertEquals(2, ((Number) body.get("count")).intValue());
        } finally {
            s.stop();
        }
    }

    @Test
    void testRouteHandlerMethodNotAllowed() throws Exception {
        int port = findFreePort();
        PineServer s = new PineServer(writeTempConfig().toString(), port);
        s.setWatch(false);
        s.addRoute(new PineServer.Route(
                "POST", "/api/echo",
                exchange -> new PineServer.ExecRequest(Map.of(), List.of()),
                (exchange, result, error) -> writeJson(exchange, 200, "{}")));
        s.start();
        HttpClient client = HttpClient.newHttpClient();
        try {
            HttpResponse<String> resp = client.send(
                    HttpRequest.newBuilder()
                            .uri(URI.create("http://localhost:" + port + "/api/echo"))
                            .GET()
                            .build(),
                    HttpResponse.BodyHandlers.ofString());
            assertEquals(405, resp.statusCode());
            assertTrue(resp.body().contains("method not allowed"), resp.body());
        } finally {
            s.stop();
        }
    }

    @Test
    void testRouteHandlerIngressErrorReachesEgress() throws Exception {
        int port = findFreePort();
        PineServer s = new PineServer(writeTempConfig().toString(), port);
        s.setWatch(false);
        s.addRoute(new PineServer.Route(
                "POST", "/api/fail",
                exchange -> { throw new IllegalArgumentException("bad payload"); },
                (exchange, result, error) -> {
                    // Egress observes the ingress error and a null result.
                    assertNotNull(error, "Egress should receive the ingress error");
                    assertNull(result, "Egress result should be null on ingress error");
                    writeJson(exchange, 400, "{\"error\":\"" + error.getMessage() + "\"}");
                }));
        s.start();
        HttpClient client = HttpClient.newHttpClient();
        try {
            HttpResponse<String> resp = client.send(
                    HttpRequest.newBuilder()
                            .uri(URI.create("http://localhost:" + port + "/api/fail"))
                            .POST(HttpRequest.BodyPublishers.ofString("{}"))
                            .build(),
                    HttpResponse.BodyHandlers.ofString());
            assertEquals(400, resp.statusCode());
            assertTrue(resp.body().contains("bad payload"), resp.body());
        } finally {
            s.stop();
        }
    }

    // skip() advances the underlying stream just like read(): an Ingress that
    // skips past an oversized body must still trip the central 413 and never
    // reach Egress — otherwise skipNBytes would bypass max_request_body_size.
    @Test
    void testRouteHandlerBodyLimitNotBypassedBySkip() throws Exception {
        int port = findFreePort();
        // Config with a tiny body cap (16 bytes) so a small request trips it.
        Map<String, Object> cfg = mapper.readValue(MINIMAL_CONFIG, new TypeReference<>() {});
        cfg.put("max_request_body_size", 16);
        Path cfgFile = Files.createTempFile("pine-route-limit-", ".json");
        Files.write(cfgFile, mapper.writeValueAsBytes(cfg));
        cfgFile.toFile().deleteOnExit();

        boolean[] egressRan = {false};
        PineServer s = new PineServer(cfgFile.toString(), port);
        s.setWatch(false);
        s.addRoute(new PineServer.Route(
                "POST", "/api/skip",
                exchange -> {
                    // Skip the whole body instead of reading it — must still
                    // count against the limit.
                    exchange.getRequestBody().skipNBytes(1000);
                    return new PineServer.ExecRequest(Map.of(), List.of());
                },
                (exchange, result, error) -> {
                    egressRan[0] = true;
                    writeJson(exchange, 200, "{}");
                }));
        s.start();
        HttpClient client = HttpClient.newHttpClient();
        try {
            HttpResponse<String> resp = client.send(
                    HttpRequest.newBuilder()
                            .uri(URI.create("http://localhost:" + port + "/api/skip"))
                            .POST(HttpRequest.BodyPublishers.ofString("a".repeat(1000)))
                            .build(),
                    HttpResponse.BodyHandlers.ofString());
            assertEquals(413, resp.statusCode(), resp.body());
            assertTrue(resp.body().contains("request body too large"), resp.body());
            assertFalse(egressRan[0], "Egress must not run when the body cap trips via skip()");
        } finally {
            s.stop();
        }
    }

    // --- helpers ----------------------------------------------------------

    private static void drain(java.io.InputStream in) throws IOException {
        byte[] tmp = new byte[4096];
        while (in.read(tmp) != -1) {
            // discard
        }
    }

    private static void writeJson(com.sun.net.httpserver.HttpExchange exchange, int status, String body)
            throws IOException {
        byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.sendResponseHeaders(status, bytes.length);
        try (java.io.OutputStream os = exchange.getResponseBody()) {
            os.write(bytes);
        }
    }
}
