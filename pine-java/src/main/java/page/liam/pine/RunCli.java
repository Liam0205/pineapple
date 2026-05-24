package page.liam.pine;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.core.util.DefaultIndenter;
import com.fasterxml.jackson.core.util.DefaultPrettyPrinter;
import com.fasterxml.jackson.core.util.Separators;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.ObjectWriter;
import page.liam.pine.operators.AllOperators;

import java.nio.file.Files;
import java.nio.file.Paths;
import java.util.*;

public class RunCli {
    private static final ObjectMapper mapper = GoFormat.createGoCompatMapper();
    private static final ObjectWriter prettyWriter = mapper.writer(
            new DefaultPrettyPrinter(
                    Separators.createDefaultInstance()
                            .withObjectFieldValueSpacing(Separators.Spacing.AFTER)
                            .withObjectEmptySeparator("")
                            .withArrayEmptySeparator("")
            ).withArrayIndenter(DefaultIndenter.SYSTEM_LINEFEED_INSTANCE)
    );

    public static void main(String[] args) throws Exception {
        AllOperators.ensureRegistered();

        String configPath = "";
        String requestPath = "";
        String resourcesPath = "";

        for (int i = 0; i < args.length; i++) {
            if ("-config".equals(args[i]) && i + 1 < args.length) configPath = args[++i];
            else if ("-request".equals(args[i]) && i + 1 < args.length) requestPath = args[++i];
            else if ("-static-resources".equals(args[i]) && i + 1 < args.length) resourcesPath = args[++i];
        }

        if (configPath.isEmpty() || requestPath.isEmpty()) {
            System.err.println("Usage: RunCli -config <pipeline.json> -request <request.json> [-static-resources <resources.json>]");
            System.exit(1);
        }

        byte[] configData;
        try {
            configData = Files.readAllBytes(Paths.get(configPath));
        } catch (java.io.IOException e) {
            System.err.println("error reading config: " + e.getMessage());
            System.exit(1);
            return;
        }

        byte[] requestData;
        try {
            requestData = Files.readAllBytes(Paths.get(requestPath));
        } catch (java.io.IOException e) {
            System.err.println("error reading request: " + e.getMessage());
            System.exit(1);
            return;
        }

        ResourceProvider rp = null;
        ResourceManager rm = null;

        // Register a built-in "static" resource fetcher factory mirroring
        // pine-{go,cpp}, so unified configs with resource_config blocks
        // load out-of-the-box.
        try {
            ResourceManager.registerFactory("static", params -> {
                Object value = params.get("value");
                return () -> value;
            });
        } catch (IllegalStateException ignored) {
            // already registered (re-run in tests etc.)
        }

        try {
            rm = new ResourceManager();
            rm.loadFromConfig(configData);
            rm.start();
            rp = rm;
        } catch (Exception e) {
            System.err.println("error loading resource_config: " + e.getMessage());
            System.exit(1);
            return;
        }

        if (!resourcesPath.isEmpty()) {
            try {
                byte[] resData = Files.readAllBytes(Paths.get(resourcesPath));
                Map<String, Object> resources = mapper.readValue(resData, new TypeReference<Map<String, Object>>() {});
                rp = new StaticResourceProvider(resources);
            } catch (java.io.IOException e) {
                System.err.println("error reading static resources: " + e.getMessage());
                System.exit(1);
                return;
            }
        }

        Engine engine;
        try {
            engine = Engine.create(configData, rp);
        } catch (PineErrors.ConfigError | PineErrors.RegistryError e) {
            System.err.println("error creating engine: " + e.getMessage());
            System.exit(1);
            return;
        }

        Map<String, Object> req;
        try {
            req = mapper.readValue(requestData, new TypeReference<Map<String, Object>>() {});
        } catch (com.fasterxml.jackson.core.JsonProcessingException e) {
            System.err.println("error parsing request: " + e.getMessage());
            System.exit(1);
            return;
        }

        // R3-X4 follow-up: avoid mapper.convertValue here. The GoFormat
        // ObjectMapper installs a Double serializer that writes -0.0 as
        // `gen.writeRawValue("-0")` for byte-exact parity with Go's
        // json.Marshal. convertValue routes the value through a Jackson
        // TokenBuffer (serialize → deserialize) and the RawValue token
        // survives the round-trip, surfacing later as a
        // `com.fasterxml.jackson.databind.util.RawValue` instance in the
        // frame — which ColumnFrame.checkValue then rejects as
        // "unsupported value type". A plain `Map<String,Object>` cast
        // skips the round-trip; the value was already deserialized into
        // proper Java types by the outer mapper.readValue.
        @SuppressWarnings("unchecked")
        Map<String, Object> common = req.containsKey("common")
                ? (Map<String, Object>) req.get("common")
                : Collections.emptyMap();
        @SuppressWarnings("unchecked")
        List<Map<String, Object>> items = req.containsKey("items")
                ? (List<Map<String, Object>>) req.get("items")
                : Collections.emptyList();

        Engine.Result result = engine.execute(common, items);

        if (result.error != null) {
            System.err.println("execution error: " + result.error.getMessage());
            System.exit(1);
        }

        Map<String, Object> output = new LinkedHashMap<>();
        output.put("common", result.common);
        output.put("items", result.items);

        String json = prettyWriter.writeValueAsString(output);
        System.out.println(json);
    }
}
