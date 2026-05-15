package page.liam.pine;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;

import java.io.*;
import java.nio.file.*;
import java.util.*;
import java.util.stream.Collectors;

import page.liam.pine.operators.AllOperators;

/**
 * Codegen reads operator schema and generates equivalent Python DSL bindings.
 *
 * Usage:
 *   # From external JSON (compat mode — reads Go-exported schema):
 *   java -cp ... page.liam.pine.Codegen -schema schema.json -output apple_generated
 *
 *   # From internal Registry (independent mode):
 *   java -cp ... page.liam.pine.Codegen --schema-from-registry -output apple_generated
 *
 *   # Export Registry schema to JSON:
 *   java -cp ... page.liam.pine.Codegen --export-schema schema-java.json
 */
public class Codegen {
    private static final ObjectMapper mapper = new ObjectMapper();

    public static void main(String[] args) throws Exception {
        String schemaPath = "";
        String outputDir = "apple_generated";
        String docDir = "";
        String resourceSchemaPath = "";
        String exportSchemaPath = "";
        boolean schemaFromRegistry = false;

        for (int i = 0; i < args.length; i++) {
            if ("-schema".equals(args[i]) && i + 1 < args.length) schemaPath = args[++i];
            else if ("-output".equals(args[i]) && i + 1 < args.length) outputDir = args[++i];
            else if ("-doc-dir".equals(args[i]) && i + 1 < args.length) docDir = args[++i];
            else if ("-resource-schema".equals(args[i]) && i + 1 < args.length) resourceSchemaPath = args[++i];
            else if ("--export-schema".equals(args[i]) && i + 1 < args.length) exportSchemaPath = args[++i];
            else if ("--schema-from-registry".equals(args[i])) schemaFromRegistry = true;
        }

        if (!exportSchemaPath.isEmpty()) {
            AllOperators.ensureRegistered();
            String json = Registry.exportSchemaJSON();
            Files.writeString(Paths.get(exportSchemaPath), json);
            System.out.printf("exported %d operator schemas to %s%n", Registry.all().size(), exportSchemaPath);
            return;
        }

        List<OperatorSchema> schemas;
        if (schemaFromRegistry) {
            AllOperators.ensureRegistered();
            schemas = fromRegistry(Registry.all());
        } else {
            if (schemaPath.isEmpty()) schemaPath = "schema.json";
            schemas = mapper.readValue(
                    new File(schemaPath),
                    new TypeReference<List<OperatorSchema>>() {});
        }

        generateOperatorsPy(schemas, outputDir);
        generateInitPy(schemas, outputDir);
        System.out.printf("generated %d operators in %s%n", schemas.size(), outputDir);

        if (!resourceSchemaPath.isEmpty()) {
            List<ResourceSchema> resourceSchemas = mapper.readValue(
                    new File(resourceSchemaPath),
                    new TypeReference<List<ResourceSchema>>() {});
            generateResourcesPy(resourceSchemas, outputDir);
            System.out.printf("generated %d resources in %s%n", resourceSchemas.size(), outputDir);
        }

        if (!docDir.isEmpty()) {
            generateDocs(schemas, docDir);
            System.out.printf("generated %d operator docs in %s%n", schemas.size(), docDir);
        }
    }

    private static List<OperatorSchema> fromRegistry(List<page.liam.pine.OperatorSchema> engineSchemas) {
        List<OperatorSchema> result = new ArrayList<>();
        for (page.liam.pine.OperatorSchema es : engineSchemas) {
            OperatorSchema cs = new OperatorSchema();
            cs.name = es.name;
            cs.type = es.type.name().toLowerCase();
            cs.description = es.description;
            Map<String, ParamSpec> params = new LinkedHashMap<>();
            for (Map.Entry<String, page.liam.pine.ParamSpec> entry : es.params.entrySet()) {
                ParamSpec ps = new ParamSpec();
                ps.type = entry.getValue().type;
                ps.required = entry.getValue().required;
                ps.defaultValue = entry.getValue().defaultValue;
                ps.description = entry.getValue().description;
                params.put(entry.getKey(), ps);
            }
            cs.params = params;
            result.add(cs);
        }
        return result;
    }

    private static void generateOperatorsPy(List<OperatorSchema> schemas, String outputDir) throws IOException {
        Files.createDirectories(Paths.get(outputDir));
        Path path = Paths.get(outputDir, "operators.py");

        try (PrintWriter w = new PrintWriter(Files.newBufferedWriter(path))) {
            w.println("# auto-generated from pine operator schema — DO NOT EDIT");
            w.println("from __future__ import annotations");
            w.println("from typing import Any");
            w.println("from apple.base import BaseOp");
            w.println();

            for (OperatorSchema schema : schemas) {
                String className = toCamelCase(schema.name) + "Op";
                w.println();
                w.printf("class %s(BaseOp):%n", className);
                w.printf("    \"\"\"Operator: %s\"\"\"%n", schema.name);
                w.printf("    _name = \"%s\"%n", schema.name);

                // _params_schema
                w.print("    _params_schema = {");
                List<String> paramNames = new ArrayList<>(schema.params.keySet());
                Collections.sort(paramNames);
                for (String pName : paramNames) {
                    ParamSpec spec = schema.params.get(pName);
                    w.printf("%n        \"%s\": {\"type\": \"%s\", \"required\": %s",
                            pName, spec.type, spec.required ? "True" : "False");
                    if (spec.defaultValue != null) {
                        w.printf(", \"default\": %s", toPythonLiteral(spec.defaultValue));
                    }
                    w.print("},");
                }
                w.println();
                w.println("    }");

                // __call__ method
                w.println();
                w.println("    def __call__(");
                w.println("        self,");
                w.println("        *,");
                for (String pName : paramNames) {
                    ParamSpec spec = schema.params.get(pName);
                    String pyType = toPythonType(spec.type);
                    String pyDefault;
                    if (spec.required) {
                        pyDefault = "...";
                    } else if (spec.defaultValue != null) {
                        pyDefault = toPythonLiteral(spec.defaultValue);
                    } else {
                        pyDefault = toPythonDefault(spec.type);
                    }
                    w.printf("        %s: %s = %s,%n", pName, pyType, pyDefault);
                }
                w.println("        common_input: list[str] | None = None,");
                w.println("        common_output: list[str] | None = None,");
                w.println("        item_input: list[str] | None = None,");
                w.println("        item_output: list[str] | None = None,");
                w.println("        item_defaults: dict | None = None,");
                w.println("        common_defaults: dict | None = None,");
                boolean isRecall = "recall".equalsIgnoreCase(schema.type);
                if (isRecall) {
                    w.println("        recall: bool = True,");
                }
                w.println("        row_dependency: bool = False,");
                w.println("        debug: bool = False,");
                w.println("        name: str | None = None,");
                w.printf("    ) -> \"%sOp\":%n", className);
                w.println("        params = {");
                for (String pName : paramNames) {
                    ParamSpec spec = schema.params.get(pName);
                    if (spec.required || spec.defaultValue != null) {
                        w.printf("            \"%s\": %s,%n", pName, pName);
                    }
                }
                w.println("        }");
                // Conditional params
                List<String> conditional = paramNames.stream()
                        .filter(n -> !schema.params.get(n).required && schema.params.get(n).defaultValue == null)
                        .collect(Collectors.toList());
                for (String pName : conditional) {
                    w.printf("        if %s is not None:%n", pName);
                    w.printf("            params[\"%s\"] = %s%n", pName, pName);
                }
                w.println("        return self._apply(");
                w.println("            params=params,");
                w.println("            common_input=common_input,");
                w.println("            common_output=common_output,");
                w.println("            item_input=item_input,");
                w.println("            item_output=item_output,");
                w.println("            item_defaults=item_defaults,");
                w.println("            common_defaults=common_defaults,");
                if (isRecall) {
                    w.println("            recall=True,");
                }
                w.println("            row_dependency=row_dependency,");
                w.println("            debug=debug,");
                w.println("            name=name or \"\",");
                w.println("        )");
                w.println();
            }
        }
    }

    private static void generateInitPy(List<OperatorSchema> schemas, String outputDir) throws IOException {
        Path path = Paths.get(outputDir, "__init__.py");
        try (PrintWriter w = new PrintWriter(Files.newBufferedWriter(path))) {
            w.println("# auto-generated — DO NOT EDIT");
            w.println("from .operators import (");
            for (OperatorSchema schema : schemas) {
                w.printf("    %sOp,%n", toCamelCase(schema.name));
            }
            w.println(")");
            w.println();
            w.println("__all__ = [");
            for (OperatorSchema schema : schemas) {
                w.printf("    \"%sOp\",%n", toCamelCase(schema.name));
            }
            w.println("]");
        }
    }

    private static String toCamelCase(String s) {
        StringBuilder sb = new StringBuilder();
        boolean upper = true;
        for (char c : s.toCharArray()) {
            if (c == '_') { upper = true; continue; }
            if (upper && c >= 'a' && c <= 'z') {
                sb.append((char)(c - 'a' + 'A'));
                upper = false;
            } else {
                sb.append(c);
                upper = false;
            }
        }
        return sb.toString();
    }

    private static String toPythonType(String type) {
        if ("string".equals(type)) return "str";
        if ("int".equals(type) || "int64".equals(type)) return "int";
        if ("float64".equals(type)) return "float";
        if ("bool".equals(type)) return "bool";
        return "Any";
    }

    private static String toPythonDefault(String type) {
        if ("string".equals(type)) return "\"\"";
        if ("int".equals(type) || "int64".equals(type)) return "0";
        if ("float64".equals(type)) return "0.0";
        if ("bool".equals(type)) return "False";
        return "None";
    }

    private static String toPythonLiteral(Object v) {
        if (v == null) return "None";
        if (v instanceof String) return "\"" + v + "\"";
        if (v instanceof Boolean) return (Boolean) v ? "True" : "False";
        if (v instanceof Number) {
            double d = ((Number) v).doubleValue();
            if (d == (long) d) return Long.toString((long) d);
            return Double.toString(d);
        }
        return "\"" + v + "\"";
    }

    private static void generateDocs(List<OperatorSchema> schemas, String docDir) throws IOException {
        Files.createDirectories(Paths.get(docDir));

        Map<String, List<String[]>> typeOps = new TreeMap<>();

        for (OperatorSchema schema : schemas) {
            Path path = Paths.get(docDir, schema.name + ".md");
            try (PrintWriter w = new PrintWriter(Files.newBufferedWriter(path))) {
                String type = schema.type != null ? schema.type : "Other";
                String desc = schema.description != null ? schema.description : "";

                w.printf("# %s%n%n", schema.name);
                w.printf("**Type**: %s%n%n", type);
                w.printf("%s%n%n", desc);
                w.println("## Parameters");
                w.println();
                w.println("| Name | Type | Required | Default | Description |");
                w.println("|------|------|----------|---------|-------------|");

                List<String> paramNames = new ArrayList<>(schema.params.keySet());
                Collections.sort(paramNames);
                for (String pName : paramNames) {
                    ParamSpec spec = schema.params.get(pName);
                    String defVal = spec.defaultValue != null ? "`" + spec.defaultValue + "`" : "-";
                    String pdesc = spec.description != null ? spec.description : "";
                    w.printf("| %s | %s | %s | %s | %s |%n",
                            pName, spec.type, spec.required ? "Yes" : "No", defVal, pdesc);
                }

                w.println();
                w.println("## DSL Usage");
                w.println();
                w.println("```python");
                w.printf("flow.%s(%n", schema.name);
                for (String pName : paramNames) {
                    w.printf("    %s=...,%n", pName);
                }
                w.println("    common_input=[...],");
                w.println("    item_input=[...],");
                w.println("    item_output=[...],");
                w.println(")");
                w.println("```");

                typeOps.computeIfAbsent(type, k -> new ArrayList<>())
                        .add(new String[]{schema.name, desc});
            }
        }

        // Generate index README.md
        Path idxPath = Paths.get(docDir, "README.md");
        try (PrintWriter w = new PrintWriter(Files.newBufferedWriter(idxPath))) {
            w.println("# Operator Reference");
            w.println();
            w.println("> Auto-generated from pine operator schema. Do not edit manually.");
            for (Map.Entry<String, List<String[]>> entry : typeOps.entrySet()) {
                w.println();
                w.printf("## %s%n%n", entry.getKey());
                w.println("| Operator | Description |");
                w.println("|----------|-------------|");
                for (String[] op : entry.getValue()) {
                    w.printf("| [%s](%s.md) | %s |%n", op[0], op[0], op[1]);
                }
            }
        }
    }

    // Schema model classes
    public static class OperatorSchema {
        public String name;
        public String type;
        public String description;
        public Map<String, ParamSpec> params = Collections.emptyMap();

        @com.fasterxml.jackson.annotation.JsonProperty("Name")
        public void setName(String name) { this.name = name; }
        @com.fasterxml.jackson.annotation.JsonProperty("Type")
        public void setType(String type) { this.type = type; }
        @com.fasterxml.jackson.annotation.JsonProperty("Description")
        public void setDescription(String description) { this.description = description; }
        @com.fasterxml.jackson.annotation.JsonProperty("Params")
        public void setParams(Map<String, ParamSpec> params) { this.params = params != null ? params : Collections.emptyMap(); }
    }

    public static class ParamSpec {
        public String type;
        public boolean required;
        public Object defaultValue;
        public String description;

        @com.fasterxml.jackson.annotation.JsonProperty("Type")
        public void setType(String type) { this.type = type; }
        @com.fasterxml.jackson.annotation.JsonProperty("Required")
        public void setRequired(boolean required) { this.required = required; }
        @com.fasterxml.jackson.annotation.JsonProperty("Default")
        public void setDefault(Object defaultValue) { this.defaultValue = defaultValue; }
        @com.fasterxml.jackson.annotation.JsonProperty("Description")
        public void setDescription(String description) { this.description = description; }
    }

    public static class ResourceSchema {
        public String name;
        public String description;
        public int defaultInterval;
        public Map<String, ParamSpec> params = Collections.emptyMap();

        @com.fasterxml.jackson.annotation.JsonProperty("Name")
        public void setName(String name) { this.name = name; }
        @com.fasterxml.jackson.annotation.JsonProperty("Description")
        public void setDescription(String description) { this.description = description; }
        @com.fasterxml.jackson.annotation.JsonProperty("DefaultInterval")
        public void setDefaultInterval(int defaultInterval) { this.defaultInterval = defaultInterval; }
        @com.fasterxml.jackson.annotation.JsonProperty("Params")
        public void setParams(Map<String, ParamSpec> params) { this.params = params != null ? params : Collections.emptyMap(); }
    }

    private static void generateResourcesPy(List<ResourceSchema> schemas, String outputDir) throws IOException {
        Path path = Paths.get(outputDir, "resources.py");
        try (PrintWriter w = new PrintWriter(Files.newBufferedWriter(path))) {
            w.println("# auto-generated from pine resource schema — DO NOT EDIT");
            w.println("from __future__ import annotations");
            w.println("from typing import Any");
            w.println("from apple.base import BaseResource");
            w.println();

            for (ResourceSchema schema : schemas) {
                String className = toCamelCase(schema.name) + "Resource";
                w.println();
                w.printf("class %s(BaseResource):%n", className);
                w.printf("    \"\"\"Resource: %s\"\"\"%n", schema.name);
                w.printf("    _name = \"%s\"%n", schema.name);
                w.printf("    _default_interval = %d%n", schema.defaultInterval);

                List<String> paramNames = new ArrayList<>(schema.params.keySet());
                Collections.sort(paramNames);

                w.println();
                w.println("    def __call__(");
                w.println("        self,");
                w.println("        *,");
                for (String pName : paramNames) {
                    ParamSpec spec = schema.params.get(pName);
                    String pyType = toPythonType(spec.type);
                    String pyDefault = spec.defaultValue != null ? toPythonLiteral(spec.defaultValue)
                            : (spec.required ? "..." : toPythonDefault(spec.type));
                    w.printf("        %s: %s = %s,%n", pName, pyType, pyDefault);
                }
                w.println("        interval: int = 0,");
                w.println("    ) -> BaseResource:");
                w.println("        params = {");
                for (String pName : paramNames) {
                    w.printf("            \"%s\": %s,%n", pName, pName);
                }
                w.println("        }");
                w.println("        return self._build(params, interval)");
                w.println();
            }
        }

        // Generate resources_init.py
        Path initPath = Paths.get(outputDir, "resources_init.py");
        try (PrintWriter w = new PrintWriter(Files.newBufferedWriter(initPath))) {
            w.println("# auto-generated — DO NOT EDIT");
            w.println("from .resources import (");
            for (ResourceSchema schema : schemas) {
                w.printf("    %sResource,%n", toCamelCase(schema.name));
            }
            w.println(")");
            w.println();
            w.println("__all__ = [");
            for (ResourceSchema schema : schemas) {
                w.printf("    \"%sResource\",%n", toCamelCase(schema.name));
            }
            w.println("]");
        }
    }
}
