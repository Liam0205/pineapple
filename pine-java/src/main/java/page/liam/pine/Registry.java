package page.liam.pine;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.SerializationFeature;

import java.util.*;
import java.util.function.Supplier;

public class Registry {
    private static final ObjectMapper mapper = new ObjectMapper().enable(SerializationFeature.INDENT_OUTPUT);

    private static final Set<String> RESERVED_KEYS = new HashSet<>(Arrays.asList(
            "type_name", "$metadata", "$code_info", "skip", "recall", "sources",
            "debug", "row_dependency", "common_defaults", "item_defaults",
            "for_branch_control", "data_parallel"
    ));

    private static final Registry GLOBAL = new Registry();

    private final Map<String, OperatorEntry> operators = new LinkedHashMap<>();

    public static Registry global() { return GLOBAL; }

    public void register(OperatorSchema schema, Supplier<Operator> factory) {
        if (schema.name == null || schema.name.isEmpty()) {
            throw new PineErrors.RegistryError("", "Register called with empty name");
        }
        if (schema.type == null) {
            throw new PineErrors.RegistryError(schema.name, "Type is required");
        }
        if (schema.description == null || schema.description.isEmpty()) {
            throw new PineErrors.RegistryError(schema.name, "Description is required");
        }
        for (Map.Entry<String, ParamSpec> entry : schema.params.entrySet()) {
            if (entry.getValue().description == null || entry.getValue().description.isEmpty()) {
                throw new PineErrors.RegistryError(schema.name,
                        "param \"" + entry.getKey() + "\": Description is required");
            }
        }
        if (operators.containsKey(schema.name)) {
            throw new PineErrors.RegistryError(schema.name, "already registered");
        }
        operators.put(schema.name, new OperatorEntry(schema, factory));
    }

    public Operator buildOperator(String typeName, Map<String, Object> rawParams) {
        OperatorEntry entry = operators.get(typeName);
        if (entry == null) {
            throw new PineErrors.RegistryError(typeName, "operator type not registered");
        }

        Map<String, Object> params = validateAndExtractParams(entry.schema, rawParams);

        Operator op = entry.factory.get();
        try {
            op.init(new OperatorParams(params));
        } catch (RuntimeException e) {
            throw new PineErrors.RegistryError(typeName, "Init failed: " + e.getMessage());
        }
        return op;
    }

    public OperatorType getType(String typeName) {
        OperatorEntry entry = operators.get(typeName);
        return entry != null ? entry.schema.type : null;
    }

    public OperatorSchema getSchema(String typeName) {
        OperatorEntry entry = operators.get(typeName);
        return entry != null ? entry.schema : null;
    }

    public List<OperatorSchema> all() {
        List<OperatorSchema> result = new ArrayList<>();
        for (OperatorEntry entry : operators.values()) {
            result.add(entry.schema);
        }
        result.sort(Comparator.comparing(s -> s.name));
        return result;
    }

    public String exportSchemaJSON() throws Exception {
        List<Map<String, Object>> schemas = new ArrayList<>();
        for (OperatorSchema s : all()) {
            Map<String, Object> obj = new LinkedHashMap<>();
            obj.put("Name", s.name);
            String typeName = s.type.name().charAt(0) + s.type.name().substring(1).toLowerCase();
            obj.put("Type", typeName);
            obj.put("Description", s.description);
            Map<String, Object> params = new LinkedHashMap<>();
            for (Map.Entry<String, ParamSpec> p : s.params.entrySet()) {
                Map<String, Object> spec = new LinkedHashMap<>();
                spec.put("Type", p.getValue().type);
                spec.put("Required", p.getValue().required);
                spec.put("Default", p.getValue().defaultValue);
                spec.put("Description", p.getValue().description);
                params.put(p.getKey(), spec);
            }
            obj.put("Params", params);
            schemas.add(obj);
        }
        return mapper.writeValueAsString(schemas);
    }

    public void reset() {
        operators.clear();
    }

    // --- Static convenience methods delegating to global instance ---

    public static void registerGlobal(OperatorSchema schema, Supplier<Operator> factory) {
        GLOBAL.register(schema, factory);
    }

    public static Map<String, Object> validateAndExtractParams(
            OperatorSchema schema, Map<String, Object> rawParams) {
        Map<String, Object> params = new LinkedHashMap<>();

        for (Map.Entry<String, Object> entry : rawParams.entrySet()) {
            if (!RESERVED_KEYS.contains(entry.getKey())) {
                params.put(entry.getKey(), entry.getValue());
            }
        }

        for (Map.Entry<String, ParamSpec> entry : schema.params.entrySet()) {
            String name = entry.getKey();
            ParamSpec spec = entry.getValue();
            if (!params.containsKey(name)) {
                if (spec.required) {
                    throw new PineErrors.RegistryError(schema.name,
                            "required parameter \"" + name + "\" missing for operator \"" + schema.name + "\"");
                }
                if (spec.defaultValue != null) {
                    params.put(name, spec.defaultValue);
                }
            }
        }

        for (String key : params.keySet()) {
            if (!schema.params.containsKey(key)) {
                throw new PineErrors.RegistryError(schema.name,
                        "unknown parameter \"" + key + "\" for operator \"" + schema.name + "\"");
            }
        }

        return params;
    }

    private static class OperatorEntry {
        final OperatorSchema schema;
        final Supplier<Operator> factory;

        OperatorEntry(OperatorSchema schema, Supplier<Operator> factory) {
            this.schema = schema;
            this.factory = factory;
        }
    }
}
