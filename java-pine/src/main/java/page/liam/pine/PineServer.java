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
import java.util.concurrent.atomic.AtomicReference;

public class PineServer {
    private static final ObjectMapper mapper = new ObjectMapper();

    private final AtomicReference<Snapshot> snapshot = new AtomicReference<>();
    private final String configPath;
    private final int port;
    private HttpServer httpServer;
    private ScheduledExecutorService watcherExecutor;

    private static class Snapshot {
        final Engine engine;
        final ResourceProvider resources;
        Snapshot(Engine engine, ResourceProvider resources) {
            this.engine = engine;
            this.resources = resources;
        }
    }

    public PineServer(String configPath, int port) {
        this.configPath = configPath;
        this.port = port;
    }

    public void start() throws Exception {
        byte[] configData = Files.readAllBytes(Paths.get(configPath));
        loadConfig(configData);

        httpServer = HttpServer.create(new InetSocketAddress(port), 0);
        httpServer.setExecutor(Executors.newFixedThreadPool(
                Runtime.getRuntime().availableProcessors() * 2));

        httpServer.createContext("/health", this::handleHealth);
        httpServer.createContext("/execute", this::handleExecute);
        httpServer.createContext("/stats", this::handleStats);

        httpServer.start();

        watcherExecutor = Executors.newSingleThreadScheduledExecutor();
        watcherExecutor.scheduleAtFixedRate(this::checkReload, 2, 2, TimeUnit.SECONDS);
    }

    public void stop() {
        if (watcherExecutor != null) {
            watcherExecutor.shutdownNow();
        }
        if (httpServer != null) {
            httpServer.stop(5);
        }
    }

    private void loadConfig(byte[] configData) throws Exception {
        ResourceManager rm = new ResourceManager();
        rm.loadFromConfig(configData);
        rm.start();

        Engine engine = Engine.create(configData, rm);
        snapshot.set(new Snapshot(engine, rm));
    }

    private volatile long lastModified = 0;

    private void checkReload() {
        try {
            long mod = Files.getLastModifiedTime(Paths.get(configPath)).toMillis();
            if (mod > lastModified) {
                lastModified = mod;
                byte[] data = Files.readAllBytes(Paths.get(configPath));
                loadConfig(data);
            }
        } catch (Exception e) {
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
            byte[] body = exchange.getRequestBody().readAllBytes();
            Map<String, Object> req = mapper.readValue(body, new TypeReference<>() {});

            @SuppressWarnings("unchecked")
            Map<String, Object> common = (Map<String, Object>) req.getOrDefault("common", Collections.emptyMap());
            @SuppressWarnings("unchecked")
            List<Map<String, Object>> items = (List<Map<String, Object>>) req.getOrDefault("items", Collections.emptyList());

            Engine.Result result = snap.engine.execute(common, items);

            Map<String, Object> resp = new LinkedHashMap<>();
            resp.put("common", result.common);
            resp.put("items", result.items);
            sendResponse(exchange, 200, resp);

        } catch (IllegalArgumentException e) {
            sendResponse(exchange, 400, Map.of("error", e.getMessage()));
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
        Map<String, Object> stats = new LinkedHashMap<>();
        stats.put("engine_loaded", snap != null);
        sendResponse(exchange, 200, stats);
    }

    private void sendResponse(HttpExchange exchange, int status, Object body) throws IOException {
        byte[] responseBytes = mapper.writeValueAsBytes(body);
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.sendResponseHeaders(status, responseBytes.length);
        try (OutputStream os = exchange.getResponseBody()) {
            os.write(responseBytes);
        }
    }

    public static void main(String[] args) throws Exception {
        String configPath = System.getProperty("pine.config", "config.json");
        int port = Integer.parseInt(System.getProperty("pine.port", "8080"));

        PineServer server = new PineServer(configPath, port);
        server.start();
        System.out.println("Pine server listening on :" + port);

        Runtime.getRuntime().addShutdownHook(new Thread(server::stop));
        Thread.currentThread().join();
    }
}
