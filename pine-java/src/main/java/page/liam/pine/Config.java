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
    /**
     * Metadata only, never read for behaviour — but parsed so that a wrong TYPE
     * is rejected here exactly as pine-go and pine-cpp reject it. Before issue
     * #187 this field was absent from this runtime entirely, so
     * {@code "_PINEAPPLE_CREATE_TIME": 123} failed the whole config load in
     * pine-go and was silently ignored here.
     */
    public String pineappleCreateTime;
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

    /**
     * Reads a root-level string field, rejecting a present-but-wrong-typed value.
     *
     * <p>Mirrors pine-go, where every RootConfig string field is declared
     * {@code string} and {@code encoding/json} fails the entire unmarshal when
     * the JSON value is a number, boolean, array or object.
     *
     * <p>These fields used to go through {@code asText()}, which coerces
     * anything: {@code 123} became {@code "123"}, {@code true} became
     * {@code "true"}, and an array or object became the empty string. So a
     * config that pine-go rejected outright ran here with a silently invented
     * value (issue #187). The divergence was never specific to
     * {@code storage_mode} — it applied to every root string field, and the
     * three runtimes disagreed three different ways.
     *
     * <p>JSON null is accepted and yields the default, matching Go: decoding a
     * null into a string field is a no-op there, leaving the zero value.
     */
    private static String rootString(JsonNode root, String field, String fallback)
            throws PineErrors.ConfigError {
        if (!root.has(field)) {
            return fallback;
        }
        JsonNode node = root.get(field);
        if (node.isNull()) {
            return fallback;
        }
        if (!node.isTextual()) {
            throw new PineErrors.ConfigError("config field \"" + field + "\" must be a string");
        }
        return node.asText();
    }

    private static Config parseRoot(JsonNode root) throws PineErrors.ConfigError {
        Config cfg = new Config();
        // ORDER MATTERS when more than one root field is wrong-typed: the field NAMED
        // in the error is externally observable. This order matches pine-cpp's
        // load_config, so those two always blame the same field.
        //
        // pine-go cannot be matched by any fixed order — encoding/json names whichever
        // wrong-typed field comes FIRST IN THE JSON DOCUMENT, so its answer moves with
        // the input's key order. Measured. Recorded in memory/doc-gaps.md.
        //
        // Add a new root string field to BOTH runtimes at the same position.
        cfg.pineappleVersion = rootString(root, "_PINEAPPLE_VERSION", "");
        cfg.pineappleCreateTime = rootString(root, "_PINEAPPLE_CREATE_TIME", "");
        cfg.storageMode = rootString(root, "storage_mode", "row");
        cfg.logPrefix = rootString(root, "log_prefix", "");
        cfg.debug = root.has("debug") && root.get("debug").asBoolean();

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
        // storage_mode accepts exactly "row", "column", or absent/empty; anything
        // else is rejected rather than silently becoming row storage (issue #187).
        //
        // Rejecting here rather than in Frame.create keeps the dispatch rule
        // untouched — that rule is deliberately "only the exact literal column
        // selects column storage, everything else is row", mirroring pine-go's
        // NewFrame default branch (issue #179). Validation makes the invalid
        // value unreachable instead of changing what dispatch does with it.
        //
        // Empty is allowed because it is pine-go's zero value: an omitted key and
        // a JSON null both arrive as "" there, so all three runtimes accept both.
        if (!cfg.storageMode.isEmpty()
                && !"row".equals(cfg.storageMode)
                && !"column".equals(cfg.storageMode)) {
            throw new ConfigException("storage_mode \"" + cfg.storageMode
                    + "\" is invalid, must be \"row\" or \"column\"");
        }
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
