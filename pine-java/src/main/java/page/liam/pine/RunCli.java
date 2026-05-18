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
        } catch (Exception e) {
            System.err.println("error reading config: " + e.getMessage());
            System.exit(1);
            return;
        }

        byte[] requestData;
        try {
            requestData = Files.readAllBytes(Paths.get(requestPath));
        } catch (Exception e) {
            System.err.println("error reading request: " + e.getMessage());
            System.exit(1);
            return;
        }

        ResourceProvider rp = null;
        if (!resourcesPath.isEmpty()) {
            try {
                byte[] resData = Files.readAllBytes(Paths.get(resourcesPath));
                Map<String, Object> resources = mapper.readValue(resData, new TypeReference<Map<String, Object>>() {});
                rp = new StaticResourceProvider(resources);
            } catch (Exception e) {
                System.err.println("error reading static resources: " + e.getMessage());
                System.exit(1);
                return;
            }
        }

        Engine engine;
        try {
            engine = Engine.create(configData, rp);
        } catch (Exception e) {
            System.err.println("error creating engine: " + e.getMessage());
            System.exit(1);
            return;
        }

        Map<String, Object> req;
        try {
            req = mapper.readValue(requestData, new TypeReference<Map<String, Object>>() {});
        } catch (Exception e) {
            System.err.println("error parsing request: " + e.getMessage());
            System.exit(1);
            return;
        }

        Map<String, Object> common = req.containsKey("common")
                ? mapper.convertValue(req.get("common"), new TypeReference<Map<String, Object>>() {})
                : Collections.emptyMap();
        List<Map<String, Object>> items = req.containsKey("items")
                ? mapper.convertValue(req.get("items"), new TypeReference<List<Map<String, Object>>>() {})
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
