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
        String opsDir = "";
        boolean schemaFromRegistry = false;

        for (int i = 0; i < args.length; i++) {
            if ("-schema".equals(args[i]) && i + 1 < args.length) schemaPath = args[++i];
            else if ("-output".equals(args[i]) && i + 1 < args.length) outputDir = args[++i];
            else if ("-doc-dir".equals(args[i]) && i + 1 < args.length) docDir = args[++i];
            else if ("-ops-dir".equals(args[i]) && i + 1 < args.length) opsDir = args[++i];
            else if ("-resource-schema".equals(args[i]) && i + 1 < args.length) resourceSchemaPath = args[++i];
            else if ("--export-schema".equals(args[i]) && i + 1 < args.length) exportSchemaPath = args[++i];
            else if ("--schema-from-registry".equals(args[i])) schemaFromRegistry = true;
        }

        if (!exportSchemaPath.isEmpty()) {
            AllOperators.ensureRegistered();
            String json = Registry.global().exportSchemaJSON();
            Files.writeString(Paths.get(exportSchemaPath), json);
            System.out.printf("exported %d operator schemas to %s%n", Registry.global().all().size(), exportSchemaPath);
            return;
        }

        List<OperatorSchema> schemas;
        if (schemaFromRegistry) {
            AllOperators.ensureRegistered();
            schemas = fromRegistry(Registry.global().all());
        } else {
            if (schemaPath.isEmpty()) schemaPath = "schema.json";
            schemas = mapper.readValue(
                    new File(schemaPath),
                    new TypeReference<List<OperatorSchema>>() {});
        }

        generateOperatorsPy(schemas, outputDir);
        generateInitPy(schemas, outputDir);
        if (schemaFromRegistry) {
            generateMarkersPy(schemas, outputDir);
        }
        System.out.printf("generated %d operators in %s%n", schemas.size(), outputDir);

        // Generate resources: from explicit path, or from ResourceRegistry in registry mode
        List<ResourceSchema> resourceSchemas = null;
        if (!resourceSchemaPath.isEmpty()) {
            resourceSchemas = mapper.readValue(
                    new File(resourceSchemaPath),
                    new TypeReference<List<ResourceSchema>>() {});
        } else if (schemaFromRegistry) {
            List<ResourceSchema> fromReg = ResourceRegistry.all();
            if (!fromReg.isEmpty()) {
                resourceSchemas = fromReg;
            }
        }
        if (resourceSchemas != null && !resourceSchemas.isEmpty()) {
            generateResourcesPy(resourceSchemas, outputDir);
            System.out.printf("generated %d resources in %s%n", resourceSchemas.size(), outputDir);
        }

        if (!docDir.isEmpty()) {
            generateDocs(schemas, docDir, opsDir);
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
                ps.templatable = entry.getValue().templatable;
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
            w.println();
            w.println("from typing import Any");
            w.println();
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
                    if (spec.templatable) {
                        w.print(", \"templatable\": True");
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
                w.println("        consumes_row_set: bool = False,");
                w.println("        debug: bool = False,");
                w.println("        name: str | None = None,");
                w.printf("    ) -> \"%s\":%n", className);
                w.println("        _params = {");
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
                    w.printf("            _params[\"%s\"] = %s%n", pName, pName);
                }
                w.println("        return self._apply(");
                w.println("            params=_params,");
                w.println("            common_input=common_input,");
                w.println("            common_output=common_output,");
                w.println("            item_input=item_input,");
                w.println("            item_output=item_output,");
                w.println("            item_defaults=item_defaults,");
                w.println("            common_defaults=common_defaults,");
                if (isRecall) {
                    w.println("            recall=True,");
                }
                w.println("            consumes_row_set=consumes_row_set,");
                w.println("            debug=debug,");
                w.println("            name=name or \"\",");
                w.println("        )");
            }
        }
    }

    /**
     * Generate apple_generated/markers.py — operator → row-set marker bools,
     * probed from registered factory instances via instanceof checks against
     * the three marker interfaces (AdditiveWritesRowSet / ConsumesRowSet /
     * MutatesRowSet). Output must match the Go codegen byte-for-byte; the
     * cross-validation harness diffs the two trees.
     *
     * Only valid in --schema-from-registry mode; the JSON-schema path has no
     * way to recover marker information.
     */
    private static void generateMarkersPy(List<OperatorSchema> schemas, String outputDir) throws IOException {
        Path path = Paths.get(outputDir, "markers.py");
        try (PrintWriter w = new PrintWriter(Files.newBufferedWriter(path))) {
            w.println("# auto-generated from pine operator schema — DO NOT EDIT");
            w.println("\"\"\"Row-set marker bools per operator, probed from Go factories at codegen time.");
            w.println();
            w.println("The Go side declares row-set semantics via marker interfaces");
            w.println("(AdditiveWritesRowSet, ConsumesRowSet, MutatesRowSet). This file mirrors");
            w.println("those flags so Apple OpCall and the validator can judge row-set behavior");
            w.println("directly instead of inferring from operator name prefix.");
            w.println("\"\"\"");
            w.println("from __future__ import annotations");
            w.println();
            w.println("OPERATOR_MARKERS: dict[str, dict[str, bool]] = {");
            for (OperatorSchema schema : schemas) {
                Optional<Operator> opt = Registry.global().instantiate(schema.name);
                boolean additive = false, consumes = false, mutates = false;
                if (opt.isPresent()) {
                    Operator op = opt.get();
                    additive = op instanceof AdditiveWritesRowSet;
                    consumes = op instanceof ConsumesRowSet;
                    mutates = op instanceof MutatesRowSet;
                }
                w.printf("    \"%s\": {%n", schema.name);
                w.printf("        \"additive_writes_row_set\": %s,%n", additive ? "True" : "False");
                w.printf("        \"consumes_row_set\": %s,%n", consumes ? "True" : "False");
                w.printf("        \"mutates_row_set\": %s,%n", mutates ? "True" : "False");
                w.println("    },");
            }
            w.println("}");
            w.println();
            w.println();
            w.println("def get_markers(type_name: str) -> dict[str, bool]:");
            w.println("    \"\"\"Return the marker dict for type_name, or all-False defaults if unknown.");
            w.println();
            w.println("    Unknown operators (e.g., custom ops registered after codegen) are");
            w.println("    treated as having no row-set semantics; the Go side remains authoritative.");
            w.println("    \"\"\"");
            w.println("    return OPERATOR_MARKERS.get(type_name, {");
            w.println("        \"additive_writes_row_set\": False,");
            w.println("        \"consumes_row_set\": False,");
            w.println("        \"mutates_row_set\": False,");
            w.println("    })");
        }
    }

    private static void generateInitPy(List<OperatorSchema> schemas, String outputDir) throws IOException {
        Path path = Paths.get(outputDir, "__init__.py");
        try (PrintWriter w = new PrintWriter(Files.newBufferedWriter(path))) {
            w.println("# auto-generated from pine operator schema — DO NOT EDIT");
            for (OperatorSchema schema : schemas) {
                w.printf("from .operators import %sOp%n", toCamelCase(schema.name));
            }
            w.println();
            w.print("__all__ = [");
            for (int i = 0; i < schemas.size(); i++) {
                w.printf("\"%sOp\", ", toCamelCase(schemas.get(i).name));
            }
            w.println("]");
        }
    }

    private static String toCamelCase(String s) {
        StringBuilder sb = new StringBuilder();
        boolean upper = true;
        for (char c : s.toCharArray()) {
            if (c == '_') {
                upper = true;
                continue;
            }
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
        if (v instanceof String) return "\"" + escapeString((String) v) + "\"";
        if (v instanceof Boolean) return (Boolean) v ? "True" : "False";
        if (v instanceof Number) {
            double d = ((Number) v).doubleValue();
            if (d == (long) d && !Double.isInfinite(d)) return Long.toString((long) d);
            return GoFormat.formatG(d);
        }
        return "\"" + escapeString(v.toString()) + "\"";
    }

    private static String escapeString(String s) {
        StringBuilder sb = new StringBuilder(s.length());
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            switch (c) {
                case '\\':
                    sb.append("\\\\");
                    break;
                case '"':
                    sb.append("\\\"");
                    break;
                case '\n':
                    sb.append("\\n");
                    break;
                case '\r':
                    sb.append("\\r");
                    break;
                case '\t':
                    sb.append("\\t");
                    break;
                case '\0':
                    sb.append("\\0");
                    break;
                case '\b':
                    sb.append("\\b");
                    break;
                case '\f':
                    sb.append("\\f");
                    break;
                default:
                    if (c < 0x20 || (c >= 0x7f && c <= 0x9f)) {
                        sb.append(String.format("\\u%04x", (int) c));
                    } else {
                        sb.append(c);
                    }
                    break;
            }
        }
        return sb.toString();
    }

    private static void generateDocs(List<OperatorSchema> schemas, String docDir, String opsDir) throws IOException {
        Files.createDirectories(Paths.get(docDir));

        // Parse metadata contracts from operator source files
        Map<String, MetadataDoc> metadataDocs = Collections.emptyMap();
        if (opsDir != null && !opsDir.isEmpty()) {
            metadataDocs = parseOperatorDocs(opsDir);
        }

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

                MetadataDoc md = metadataDocs.get(schema.name);
                if (md != null && (md.commonInput != null || md.commonOutput != null || md.itemInput != null || md.itemOutput != null)) {
                    w.println();
                    w.println("## Metadata Contract");
                    w.println();
                    w.println("| Field | Typical Usage |");
                    w.println("|-------|---------------|");
                    if (md.commonInput != null)  w.println("| CommonInput | `" + md.commonInput + "` |");
                    if (md.commonOutput != null) w.println("| CommonOutput | `" + md.commonOutput + "` |");
                    if (md.itemInput != null)    w.println("| ItemInput | `" + md.itemInput + "` |");
                    if (md.itemOutput != null)   w.println("| ItemOutput | `" + md.itemOutput + "` |");
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
            w.println("> Auto-generated from Go operator source code. Do not edit manually.");
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

    // --- Metadata doc parsing ---

    private static class MetadataDoc {
        String commonInput, commonOutput, itemInput, itemOutput;
    }

    private static Map<String, MetadataDoc> parseOperatorDocs(String sourceDir) throws IOException {
        Map<String, MetadataDoc> result = new HashMap<>();
        Path dir = Paths.get(sourceDir);
        if (!Files.isDirectory(dir)) return result;

        Files.walk(dir)
                .filter(p -> p.toString().endsWith(".java"))
                .filter(p -> !p.getFileName().toString().equals("AllOperators.java"))
                .forEach(p -> {
                    try {
                        parseJavadocMetadata(p, result);
                    } catch (IOException e) {
                        // skip files that can't be read
                    }
                });

        return result;
    }

    private static void parseJavadocMetadata(Path file, Map<String, MetadataDoc> result) throws IOException {
        List<String> lines = Files.readAllLines(file);
        String operatorName = null;
        MetadataDoc doc = new MetadataDoc();
        boolean inJavadoc = false;
        boolean inMetadata = false;

        for (String line : lines) {
            String trimmed = line.trim();

            // Track Javadoc comment boundaries
            if (trimmed.startsWith("/**")) {
                inJavadoc = true;
                inMetadata = false;
                operatorName = null;
                doc = new MetadataDoc();
                // Check if the Operator: is on the same line
                String content = trimmed.substring(3);
                if (content.contains("Operator:")) {
                    int idx = content.indexOf("Operator:");
                    operatorName = content.substring(idx + 9).replace("*/", "").trim();
                }
                continue;
            }
            if (inJavadoc && trimmed.contains("*/")) {
                inJavadoc = false;
                if (operatorName != null && !operatorName.isEmpty()) {
                    if (doc.commonInput != null || doc.commonOutput != null ||
                        doc.itemInput != null || doc.itemOutput != null) {
                        result.put(operatorName, doc);
                    }
                }
                continue;
            }
            if (!inJavadoc) continue;

            // Strip leading " * " from Javadoc lines
            String content = trimmed;
            if (content.startsWith("*")) {
                content = content.substring(1).trim();
            }

            if (content.startsWith("Operator:")) {
                operatorName = content.substring(9).trim();
                continue;
            }
            if (content.startsWith("Metadata contract")) {
                inMetadata = true;
                continue;
            }
            // End metadata on other section headers
            if (inMetadata && !content.isEmpty() &&
                !content.startsWith("CommonInput:") && !content.startsWith("CommonOutput:") &&
                !content.startsWith("ItemInput:") && !content.startsWith("ItemOutput:")) {
                // Known section headers that end metadata
                if (content.startsWith("Type:") || content.startsWith("Description:") ||
                    content.startsWith("Params:") || content.startsWith("Operator:")) {
                    inMetadata = false;
                }
            }

            if (inMetadata) {
                if (content.startsWith("CommonInput:")) {
                    doc.commonInput = content.substring(12).trim();
                } else if (content.startsWith("CommonOutput:")) {
                    doc.commonOutput = content.substring(13).trim();
                } else if (content.startsWith("ItemInput:")) {
                    doc.itemInput = content.substring(10).trim();
                } else if (content.startsWith("ItemOutput:")) {
                    doc.itemOutput = content.substring(11).trim();
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
        public boolean templatable;

        @com.fasterxml.jackson.annotation.JsonProperty("Type")
        public void setType(String type) { this.type = type; }
        @com.fasterxml.jackson.annotation.JsonProperty("Required")
        public void setRequired(boolean required) { this.required = required; }
        @com.fasterxml.jackson.annotation.JsonProperty("Default")
        public void setDefault(Object defaultValue) { this.defaultValue = defaultValue; }
        @com.fasterxml.jackson.annotation.JsonProperty("Description")
        public void setDescription(String description) { this.description = description; }
        @com.fasterxml.jackson.annotation.JsonProperty("Templatable")
        public void setTemplatable(boolean templatable) { this.templatable = templatable; }
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
            w.println("# ruff: noqa: E501");
            w.println("from __future__ import annotations");
            w.println();
            w.println("from apple.resource import BaseResource");
            w.println();

            for (ResourceSchema schema : schemas) {
                String className = toCamelCase(schema.name) + "Resource";
                w.println();
                w.printf("class %s(BaseResource):%n", className);
                String desc = schema.description != null && !schema.description.isEmpty()
                        ? schema.name + " — " + schema.description : schema.name;
                w.printf("    \"\"\"Resource: %s\"\"\"%n", desc);
                w.printf("    _name = \"%s\"%n", schema.name);
                w.printf("    _default_interval = %d%n", schema.defaultInterval);

                // _params_schema
                List<String> paramNames = new ArrayList<>(schema.params.keySet());
                Collections.sort(paramNames);
                w.print("    _params_schema = {");
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

                w.println();
                w.println("    def __init__(");
                w.println("        self,");
                w.println("        *,");
                for (String pName : paramNames) {
                    ParamSpec spec = schema.params.get(pName);
                    String pyType = toPythonType(spec.type);
                    String pyDefault = spec.defaultValue != null ? toPythonLiteral(spec.defaultValue)
                            : (spec.required ? "..." : toPythonDefault(spec.type));
                    w.printf("        %s: %s = %s,%n", pName, pyType, pyDefault);
                }
                w.printf("        interval: int = %d,%n", schema.defaultInterval);
                w.println("    ):");
                w.println("        super().__init__(");
                w.println("            interval=interval,");
                for (String pName : paramNames) {
                    w.printf("            %s=%s,%n", pName, pName);
                }
                w.println("        )");
            }
        }

        // Generate resources_init.py
        Path initPath = Paths.get(outputDir, "resources_init.py");
        try (PrintWriter w = new PrintWriter(Files.newBufferedWriter(initPath))) {
            w.println("# auto-generated from pine resource schema — DO NOT EDIT");
            for (ResourceSchema schema : schemas) {
                w.printf("from .resources import %sResource%n", toCamelCase(schema.name));
            }
            w.println();
            w.print("__all__ = [");
            for (int i = 0; i < schemas.size(); i++) {
                w.printf("\"%sResource\", ", toCamelCase(schemas.get(i).name));
            }
            w.println("]");
        }
    }
}
