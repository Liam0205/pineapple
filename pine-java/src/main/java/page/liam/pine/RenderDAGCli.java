package page.liam.pine;

import page.liam.pine.operators.AllOperators;

import java.nio.file.Files;
import java.nio.file.Paths;

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

        byte[] data = Files.readAllBytes(Paths.get(configPath));
        Engine engine = Engine.create(data, (ResourceProvider) null);
        String output = engine.renderDAG(format, collapse);
        System.out.print(output);
    }
}
