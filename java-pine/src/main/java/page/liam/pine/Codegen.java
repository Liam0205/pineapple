package page.liam.pine;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;

import java.io.*;
import java.nio.file.*;
import java.util.*;
import java.util.stream.Collectors;

/**
 * Codegen reads a Pine operator schema JSON (exported by Go pineapple-codegen -schema-json)
 * and generates equivalent Python DSL bindings.
 *
 * Usage:
 *   java -cp ... page.liam.pine.Codegen -schema schema.json -output apple_generated
 */
public class Codegen {
    private static final ObjectMapper mapper = new ObjectMapper();

    public static void main(String[] args) throws Exception {
        String schemaPath = "schema.json";
        String outputDir = "apple_generated";

        for (int i = 0; i < args.length - 1; i++) {
            if ("-schema".equals(args[i])) schemaPath = args[++i];
            else if ("-output".equals(args[i])) outputDir = args[++i];
        }

        List<OperatorSchema> schemas = mapper.readValue(
                new File(schemaPath),
                new TypeReference<List<OperatorSchema>>() {});

        generateOperatorsPy(schemas, outputDir);
        generateInitPy(schemas, outputDir);
        System.out.printf("generated %d operators in %s%n", schemas.size(), outputDir);
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
                boolean isRecall = "recall".equalsIgnoreCase(schema.type);
                if (isRecall) {
                    w.println("        recall: bool = True,");
                }
                w.println("    ) -> BaseOp:");
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
                w.printf("        return self._build(params, common_input, common_output, item_input, item_output%s)%n",
                        isRecall ? ", recall=recall" : "");
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
}
