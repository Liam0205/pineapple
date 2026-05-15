package page.liam.pine;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.*;

import java.io.IOException;
import java.net.ServerSocket;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

@TestMethodOrder(MethodOrderer.OrderAnnotation.class)
public class ServerTest {
    private static final ObjectMapper mapper = new ObjectMapper();
    private static PineServer server;
    private static int port;
    private static Path configFile;
    private static HttpClient client;

    @BeforeAll
    static void startServer() throws Exception {
        String config = "{"
                + "\"_PINEAPPLE_VERSION\": \"0.6.6\","
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

        configFile = Files.createTempFile("pine-test-config-", ".json");
        Files.write(configFile, config.getBytes(StandardCharsets.UTF_8));

        port = findFreePort();
        server = new PineServer(configFile.toString(), port);
        server.start();

        client = HttpClient.newHttpClient();
    }

    @AfterAll
    static void stopServer() throws IOException {
        if (server != null) {
            server.stop();
        }
        if (configFile != null) {
            Files.deleteIfExists(configFile);
        }
    }

    private static int findFreePort() throws IOException {
        try (ServerSocket socket = new ServerSocket(0)) {
            return socket.getLocalPort();
        }
    }

    private String baseUrl() {
        return "http://localhost:" + port;
    }

    @Test
    @Order(1)
    void testHealthEndpoint() throws Exception {
        HttpRequest req = HttpRequest.newBuilder()
                .uri(URI.create(baseUrl() + "/health"))
                .GET()
                .build();

        HttpResponse<String> resp = client.send(req, HttpResponse.BodyHandlers.ofString());
        assertEquals(200, resp.statusCode());

        Map<String, Object> body = mapper.readValue(resp.body(), new TypeReference<>() {});
        assertEquals("ok", body.get("status"));
    }

    @Test
    @Order(2)
    void testExecuteSuccess() throws Exception {
        String requestBody = "{\"common\": {}, \"items\": []}";

        HttpRequest req = HttpRequest.newBuilder()
                .uri(URI.create(baseUrl() + "/execute"))
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString(requestBody))
                .build();

        HttpResponse<String> resp = client.send(req, HttpResponse.BodyHandlers.ofString());
        assertEquals(200, resp.statusCode());

        Map<String, Object> body = mapper.readValue(resp.body(), new TypeReference<>() {});
        assertTrue(body.containsKey("common"));
        assertTrue(body.containsKey("items"));
    }

    @Test
    @Order(3)
    void testExecuteEmptyBody() throws Exception {
        HttpRequest req = HttpRequest.newBuilder()
                .uri(URI.create(baseUrl() + "/execute"))
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString(""))
                .build();

        HttpResponse<String> resp = client.send(req, HttpResponse.BodyHandlers.ofString());
        assertEquals(400, resp.statusCode());
    }

    @Test
    @Order(4)
    void testStatsEndpoint() throws Exception {
        HttpRequest req = HttpRequest.newBuilder()
                .uri(URI.create(baseUrl() + "/stats"))
                .GET()
                .build();

        HttpResponse<String> resp = client.send(req, HttpResponse.BodyHandlers.ofString());
        assertEquals(200, resp.statusCode());

        Map<String, Object> body = mapper.readValue(resp.body(), new TypeReference<>() {});
        assertTrue(body.containsKey("operators"));
    }

    @Test
    @Order(5)
    void testDagDotFormat() throws Exception {
        HttpRequest req = HttpRequest.newBuilder()
                .uri(URI.create(baseUrl() + "/dag?format=dot"))
                .GET()
                .build();

        HttpResponse<String> resp = client.send(req, HttpResponse.BodyHandlers.ofString());
        assertEquals(200, resp.statusCode());
        assertTrue(resp.body().contains("digraph"));
    }

    @Test
    @Order(6)
    void testDagInvalidFormat() throws Exception {
        HttpRequest req = HttpRequest.newBuilder()
                .uri(URI.create(baseUrl() + "/dag?format=invalid"))
                .GET()
                .build();

        HttpResponse<String> resp = client.send(req, HttpResponse.BodyHandlers.ofString());
        assertEquals(400, resp.statusCode());
    }
}
