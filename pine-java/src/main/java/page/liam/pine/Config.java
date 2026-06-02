package page.liam.pine;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.annotation.JsonProperty;
import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;

import java.util.*;

public class Config {
    private static final ObjectMapper mapper = new ObjectMapper();
    private static final Set<String> RESERVED_KEYS = new HashSet<>(Arrays.asList(
            "type_name", "$metadata", "$code_info", "skip", "recall", "sources",
            "debug", "consumes_row_set", "mutates_row_set", "additive_writes_row_set",
            "common_defaults", "item_defaults", "strict_common", "strict_item", "for_branch_control", "data_parallel"
    ));

    public String pineappleVersion;
    public String logPrefix = "";
    public boolean debug;
    public String storageMode = "row";
    public PipelineConfig pipelineConfig;
    public Map<String, SubFlowRef> pipelineGroup;
    public FlowContract flowContract;

    public static Config load(byte[] json) throws PineErrors.ConfigError {
        JsonNode root;
        try {
            root = mapper.readTree(json);
        } catch (Exception e) {
            throw new PineErrors.ConfigError("failed to parse config JSON: " + e.getMessage());
        }
        Config cfg;
        try {
            cfg = parseRoot(root);
        } catch (IllegalArgumentException e) {
            throw new PineErrors.ConfigError("invalid config structure: " + e.getMessage());
        }
        validate(cfg);
        return cfg;
    }

    private static Config parseRoot(JsonNode root) {
        Config cfg = new Config();
        cfg.pineappleVersion = root.has("_PINEAPPLE_VERSION") ? root.get("_PINEAPPLE_VERSION").asText() : "";
        cfg.logPrefix = root.has("log_prefix") ? root.get("log_prefix").asText() : "";
        cfg.debug = root.has("debug") && root.get("debug").asBoolean();
        cfg.storageMode = root.has("storage_mode") ? root.get("storage_mode").asText() : "row";

        // Parse flow_contract
        cfg.flowContract = new FlowContract();
        if (root.has("flow_contract")) {
            JsonNode fc = root.get("flow_contract");
            cfg.flowContract.commonInput = readStringList(fc, "common_input");
            cfg.flowContract.itemInput = readStringList(fc, "item_input");
            cfg.flowContract.commonOutput = readStringList(fc, "common_output");
            cfg.flowContract.itemOutput = readStringList(fc, "item_output");
        }

        // Parse pipeline_group
        cfg.pipelineGroup = new LinkedHashMap<>();
        if (root.has("pipeline_group")) {
            for (Iterator<Map.Entry<String, JsonNode>> it = root.get("pipeline_group").fields(); it.hasNext(); ) {
                Map.Entry<String, JsonNode> entry = it.next();
                SubFlowRef ref = new SubFlowRef();
                ref.pipeline = readStringList(entry.getValue(), "pipeline");
                cfg.pipelineGroup.put(entry.getKey(), ref);
            }
        }

        // Parse pipeline_config
        cfg.pipelineConfig = new PipelineConfig();
        cfg.pipelineConfig.operators = new LinkedHashMap<>();
        cfg.pipelineConfig.pipelineMap = new LinkedHashMap<>();

        if (root.has("pipeline_config")) {
            JsonNode pc = root.get("pipeline_config");

            // Parse pipeline_map
            if (pc.has("pipeline_map")) {
                for (Iterator<Map.Entry<String, JsonNode>> it = pc.get("pipeline_map").fields(); it.hasNext(); ) {
                    Map.Entry<String, JsonNode> entry = it.next();
                    SubFlowRef ref = new SubFlowRef();
                    ref.pipeline = readStringList(entry.getValue(), "pipeline");
                    cfg.pipelineConfig.pipelineMap.put(entry.getKey(), ref);
                }
            }

            // Parse operators
            if (pc.has("operators")) {
                for (Iterator<Map.Entry<String, JsonNode>> it = pc.get("operators").fields(); it.hasNext(); ) {
                    Map.Entry<String, JsonNode> entry = it.next();
                    String name = entry.getKey();
                    JsonNode opNode = entry.getValue();
                    OperatorConfig opCfg = parseOperatorConfig(opNode);
                    cfg.pipelineConfig.operators.put(name, opCfg);
                }
            }
        }

        return cfg;
    }

    private static OperatorConfig parseOperatorConfig(JsonNode node) {
        OperatorConfig opCfg = new OperatorConfig();
        opCfg.typeName = node.has("type_name") ? node.get("type_name").asText() : "";
        opCfg.recall = node.has("recall") && node.get("recall").asBoolean();
        opCfg.debug = node.has("debug") ? node.get("debug").asBoolean() : null;
        opCfg.consumesRowSet = node.has("consumes_row_set") && node.get("consumes_row_set").asBoolean();
        opCfg.mutatesRowSet = node.has("mutates_row_set") && node.get("mutates_row_set").asBoolean();
        opCfg.additiveWritesRowSet = node.has("additive_writes_row_set") && node.get("additive_writes_row_set").asBoolean();
        opCfg.forBranchControl = node.has("for_branch_control") && node.get("for_branch_control").asBoolean();
        opCfg.dataParallel = 1;
        if (node.has("data_parallel")) {
            JsonNode dp = node.get("data_parallel");
            if (!dp.isInt()) {
                throw new IllegalArgumentException("field \"data_parallel\" must be an integer, got " + dp.getNodeType());
            }
            opCfg.dataParallel = dp.asInt();
        }
        opCfg.sources = node.has("sources") ? readStringList(node, "sources") : Collections.emptyList();

        // Parse skip
        if (node.has("skip")) {
            JsonNode skipNode = node.get("skip");
            if (skipNode.isArray()) {
                opCfg.skip = new ArrayList<>();
                for (JsonNode s : skipNode) {
                    opCfg.skip.add(s.asText());
                }
            } else if (skipNode.isTextual() && !skipNode.asText().isEmpty()) {
                opCfg.skip = Collections.singletonList(skipNode.asText());
            } else {
                opCfg.skip = Collections.emptyList();
            }
        } else {
            opCfg.skip = Collections.emptyList();
        }

        // Parse $metadata
        opCfg.metadata = new Metadata();
        if (node.has("$metadata")) {
            JsonNode meta = node.get("$metadata");
            opCfg.metadata.commonInput = readStringList(meta, "common_input");
            opCfg.metadata.commonInputSkip = readStringList(meta, "common_input_skip");
            opCfg.metadata.commonInputTemplate = readStringList(meta, "common_input_template");
            opCfg.metadata.commonOutput = readStringList(meta, "common_output");
            opCfg.metadata.itemInput = readStringList(meta, "item_input");
            opCfg.metadata.itemOutput = readStringList(meta, "item_output");
        }

        // Parse common_defaults and item_defaults
        opCfg.commonDefaults = node.has("common_defaults")
                ? mapper.convertValue(node.get("common_defaults"), new TypeReference<Map<String, Object>>() {})
                : Collections.emptyMap();
        opCfg.itemDefaults = node.has("item_defaults")
                ? mapper.convertValue(node.get("item_defaults"), new TypeReference<Map<String, Object>>() {})
                : Collections.emptyMap();

        // Parse strict_common and strict_item
        opCfg.strictCommon = node.has("strict_common")
                ? readStringList(node, "strict_common")
                : Collections.emptyList();
        opCfg.strictItem = node.has("strict_item")
                ? readStringList(node, "strict_item")
                : Collections.emptyList();

        // Extract raw params (non-reserved keys)
        opCfg.rawParams = new LinkedHashMap<>();
        for (Iterator<Map.Entry<String, JsonNode>> it = node.fields(); it.hasNext(); ) {
            Map.Entry<String, JsonNode> entry = it.next();
            if (!RESERVED_KEYS.contains(entry.getKey())) {
                opCfg.rawParams.put(entry.getKey(), mapper.convertValue(entry.getValue(), Object.class));
            }
        }

        return opCfg;
    }

    public List<String> expandOperatorSequence() throws PineErrors.ConfigError {
        return expandOperatorSequenceWithSubFlows().sequence;
    }

    public ExpandResult expandOperatorSequenceWithSubFlows() throws PineErrors.ConfigError {
        SubFlowRef group;
        if (pipelineGroup.containsKey("main")) {
            group = pipelineGroup.get("main");
        } else if (pipelineGroup.size() == 1) {
            group = pipelineGroup.values().iterator().next();
        } else {
            throw new ConfigException("pipeline_group must contain a \"main\" entry or exactly one entry");
        }

        // Reject ambiguous names
        for (String name : pipelineConfig.operators.keySet()) {
            if (pipelineConfig.pipelineMap.containsKey(name)) {
                throw new ConfigException("name \"" + name + "\" exists in both operators and pipeline_map");
            }
        }

        List<String> sequence = new ArrayList<>();
        Map<String, String> opToSubFlow = new LinkedHashMap<>();
        Set<String> visiting = new HashSet<>();
        Set<String> seen = new HashSet<>();

        expandEntries(group.pipeline, "", sequence, opToSubFlow, visiting, seen);
        return new ExpandResult(sequence, opToSubFlow);
    }

    private void expandEntries(List<String> entries, String parentPath,
                               List<String> sequence, Map<String, String> opToSubFlow,
                               Set<String> visiting, Set<String> seen) throws ConfigException {
        for (String entry : entries) {
            if (pipelineConfig.operators.containsKey(entry)) {
                if (seen.contains(entry)) {
                    throw new ConfigException("operator \"" + entry + "\" referenced more than once in pipeline tree");
                }
                seen.add(entry);
                sequence.add(entry);
                opToSubFlow.put(entry, parentPath);
            } else if (pipelineConfig.pipelineMap.containsKey(entry)) {
                if (visiting.contains(entry)) {
                    throw new ConfigException("cycle detected in sub-flow expansion: \"" + entry + "\"");
                }
                visiting.add(entry);
                expandEntries(pipelineConfig.pipelineMap.get(entry).pipeline, entry,
                        sequence, opToSubFlow, visiting, seen);
                visiting.remove(entry);
            } else {
                throw new ConfigException("pipeline entry \"" + entry + "\" is neither an operator nor a sub-flow");
            }
        }
    }

    private static void validate(Config cfg) throws ConfigException {
        if (cfg.pipelineConfig.operators.isEmpty()) {
            throw new ConfigException("pipeline_config.operators is empty");
        }
        if (cfg.pipelineGroup.isEmpty()) {
            throw new ConfigException("pipeline_group is empty");
        }
        for (Map.Entry<String, OperatorConfig> entry : cfg.pipelineConfig.operators.entrySet()) {
            if (entry.getValue().typeName.isEmpty()) {
                throw new ConfigException("operator \"" + entry.getKey() + "\": missing type_name");
            }
        }
        for (Map.Entry<String, OperatorConfig> entry : cfg.pipelineConfig.operators.entrySet()) {
            for (String src : entry.getValue().sources) {
                if (!cfg.pipelineConfig.operators.containsKey(src)) {
                    throw new ConfigException("operator \"" + entry.getKey() + "\": sources references undefined operator \"" + src + "\"");
                }
            }
        }
        for (Map.Entry<String, OperatorConfig> entry : cfg.pipelineConfig.operators.entrySet()) {
            String name = entry.getKey();
            OperatorConfig opCfg = entry.getValue();
            for (String skipField : opCfg.skip) {
                if (!skipField.startsWith("_")) {
                    throw new ConfigException("operator \"" + name + "\": skip field \"" + skipField + "\" must start with '_' (control fields are engine-internal)");
                }
                // Skip fields may live in either common_input (legacy
                // layout) or common_input_skip (#74 buckets). Either is
                // sufficient for DAG ordering; the operator-visible
                // input filter strips them regardless.
                if (!opCfg.metadata.commonInput.contains(skipField)
                        && !opCfg.metadata.commonInputSkip.contains(skipField)) {
                    throw new ConfigException("operator \"" + name + "\": skip field \"" + skipField + "\" must also appear in $metadata.common_input or $metadata.common_input_skip to ensure correct DAG ordering");
                }
            }
        }
    }

    private static List<String> readStringList(JsonNode parent, String field) {
        if (!parent.has(field)) return Collections.emptyList();
        JsonNode arr = parent.get(field);
        if (!arr.isArray()) {
            throw new IllegalArgumentException("field \"" + field + "\" must be an array, got " + arr.getNodeType());
        }
        List<String> result = new ArrayList<>(arr.size());
        for (JsonNode n : arr) {
            if (!n.isTextual()) {
                throw new IllegalArgumentException("field \"" + field + "\" array elements must be strings, got " + n.getNodeType());
            }
            result.add(n.asText());
        }
        return result;
    }

    // --- Inner types ---

    public static class PipelineConfig {
        public Map<String, OperatorConfig> operators;
        public Map<String, SubFlowRef> pipelineMap;
    }

    public static class SubFlowRef {
        public List<String> pipeline;
    }

    public static class FlowContract {
        public List<String> commonInput = Collections.emptyList();
        public List<String> itemInput = Collections.emptyList();
        public List<String> commonOutput = Collections.emptyList();
        public List<String> itemOutput = Collections.emptyList();
    }

    public static class Metadata {
        public List<String> commonInput = Collections.emptyList();
        public List<String> commonInputSkip = Collections.emptyList();
        public List<String> commonInputTemplate = Collections.emptyList();
        public List<String> commonOutput = Collections.emptyList();
        public List<String> itemInput = Collections.emptyList();
        public List<String> itemOutput = Collections.emptyList();

        /**
         * Union of the three common_input buckets in declaration order
         * (business → skip → template), deduped. Used by the DAG to
         * derive per-operator read dependencies. The operator-visible
         * input view is filtered separately via {@link InputFieldSpec}.
         */
        public List<String> commonReadFields() {
            if (commonInputSkip.isEmpty() && commonInputTemplate.isEmpty()) {
                return commonInput;
            }
            LinkedHashSet<String> seen = new LinkedHashSet<>(commonInput.size()
                    + commonInputSkip.size() + commonInputTemplate.size());
            seen.addAll(commonInput);
            seen.addAll(commonInputSkip);
            seen.addAll(commonInputTemplate);
            return new ArrayList<>(seen);
        }
    }

    public static class OperatorConfig {
        public String typeName;
        public Metadata metadata;
        public List<String> skip;
        public boolean recall;
        public List<String> sources;
        public Boolean debug;
        public boolean consumesRowSet;
        public boolean mutatesRowSet;
        public boolean additiveWritesRowSet;
        public boolean forBranchControl;
        public int dataParallel = 1;
        public Map<String, Object> commonDefaults = Collections.emptyMap();
        public Map<String, Object> itemDefaults = Collections.emptyMap();
        public List<String> strictCommon = Collections.emptyList();
        public List<String> strictItem = Collections.emptyList();
        public Map<String, Object> rawParams;
        public String operatorType; // populated at engine build time
        public InputFieldSpec inputSpec; // pre-computed at engine build time
    }

    public static class ExpandResult {
        public final List<String> sequence;
        public final Map<String, String> opToSubFlow;

        public ExpandResult(List<String> sequence, Map<String, String> opToSubFlow) {
            this.sequence = sequence;
            this.opToSubFlow = opToSubFlow;
        }
    }

    public static class ConfigException extends PineErrors.ConfigError {
        public ConfigException(String message) {
            super(message);
        }
    }
}
