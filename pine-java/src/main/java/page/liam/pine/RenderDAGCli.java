package page.liam.pine;

import page.liam.pine.operators.AllOperators;

import java.nio.file.Files;
import java.nio.file.Paths;
import java.util.Collections;

public class RenderDAGCli {
    public static void main(String[] args) throws Exception {
        AllOperators.ensureRegistered();

        String configPath = "";
        String format = "dot";
        int collapse = 0;

        for (int i = 0; i < args.length; i++) {
            if ("-config".equals(args[i]) && i + 1 < args.length) configPath = args[++i];
            else if ("-format".equals(args[i]) && i + 1 < args.length) format = args[++i];
            else if ("-collapse".equals(args[i]) && i + 1 < args.length) collapse = Integer.parseInt(args[++i]);
        }

        if (configPath.isEmpty()) {
            System.err.println("Usage: RenderDAGCli -config <path> [-format dot|mermaid] [-collapse N]");
            System.exit(1);
        }

        byte[] data;
        try {
            data = Files.readAllBytes(Paths.get(configPath));
        } catch (java.io.IOException e) {
            System.err.println("error reading config: " + e.getMessage());
            System.exit(1);
            return;
        }

        Engine engine;
        try {
            ResourceProvider rp = new StaticResourceProvider(Collections.emptyMap());
            engine = Engine.create(data, rp);
        } catch (PineErrors.ConfigError | PineErrors.RegistryError e) {
            System.err.println("error creating engine: " + e.getMessage());
            System.exit(1);
            return;
        }

        String output;
        try {
            output = engine.renderDAG(format, collapse);
        } catch (IllegalArgumentException e) {
            System.err.println("error rendering DAG: " + e.getMessage());
            System.exit(1);
            return;
        }

        System.out.print(output);
    }
}
