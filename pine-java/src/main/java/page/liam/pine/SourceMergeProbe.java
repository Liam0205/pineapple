package page.liam.pine;

import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.TreeMap;

import page.liam.pine.operators.AllOperators;

/**
 * Cross-runtime probe for the {@code _source} rewrite exemption of the
 * declared-output contract (issue #224).
 *
 * <p>A merge keeps the first item per "id" and rewrites the kept item's
 * {@code _source} with the sorted, comma-joined tags of its duplicates — the
 * shape the DSL cannot declare, because it rejects underscore-prefixed
 * outputs. Each scenario prints:
 *
 * <pre>
 *   scenario=&lt;name&gt; error=&lt;err or "nil"&gt; items=[&lt;id&gt;:&lt;_source&gt;;...]
 * </pre>
 *
 * Items are printed only when the run succeeds. cross-validate Section 24
 * asserts stdout is byte-identical to pine-go's pine-source-merge-probe and
 * pine-cpp's pineapple-source-merge-probe.
 */
public final class SourceMergeProbe {
    private SourceMergeProbe() {
    }

    /**
     * Dedups by "id" and rewrites the kept item's {@code _source}. With extra
     * set it also writes an undeclared "extra" field on every kept item.
     */
    public static class MergeOp implements Operator {
        private final boolean extra;

        MergeOp(boolean extra) {
            this.extra = extra;
        }

        @Override
        public void init(OperatorParams params) {
        }

        @Override
        public void execute(RequestContext ctx, OperatorInput input, OperatorOutput output) {
            Map<String, Integer> kept = new LinkedHashMap<>();
            Map<Integer, List<String>> tags = new TreeMap<>();
            for (int i = 0; i < input.itemCount(); i++) {
                String key = input.item(i, "id") instanceof String s ? s : "";
                // "" when _source is not projected
                String tag = input.item(i, "_source") instanceof String s ? s : "";
                Integer j = kept.get(key);
                if (j != null) {
                    tags.get(j).add(tag);
                    output.removeItem(i);
                    continue;
                }
                kept.put(key, i);
                tags.put(i, new ArrayList<>(List.of(tag)));
            }
            for (Map.Entry<Integer, List<String>> e : tags.entrySet()) {
                List<String> list = e.getValue();
                if (list.size() > 1) {
                    Collections.sort(list);
                    output.setItem(e.getKey(), "_source", String.join(",", list));
                }
                if (extra) {
                    output.setItem(e.getKey(), "extra", 1.0);
                }
            }
        }
    }

    /** Carries the row-set markers the exemption requires. */
    public static final class RowSetMerge extends MergeOp implements ConsumesRowSet, MutatesRowSet {
        RowSetMerge(boolean extra) {
            super(extra);
        }
    }

    private static String config(String typeName, String itemInput) {
        return "{\"pipeline_config\": {\"operators\": {\"m\": {\"type_name\": \"" + typeName + "\","
                + " \"$metadata\": {\"common_input\": [], \"common_output\": [], \"item_input\": " + itemInput
                + ", \"item_output\": []}}}, \"pipeline_map\": {}},"
                + " \"pipeline_group\": {\"main\": {\"pipeline\": [\"m\"]}},"
                + " \"flow_contract\": {\"common_input\": [], \"common_output\": [],"
                + " \"item_input\": [\"id\", \"_source\"], \"item_output\": [\"id\", \"_source\"]}}";
    }

    private static Map<String, Object> item(String id, String source) {
        Map<String, Object> item = new LinkedHashMap<>();
        item.put("id", id);
        item.put("_source", source);
        return item;
    }

    private static void run(String name, String config) throws Exception {
        Engine engine = Engine.create(config.getBytes(StandardCharsets.UTF_8));
        try {
            List<Map<String, Object>> items = new ArrayList<>(
                    List.of(item("x", "b"), item("x", "a"), item("y", "c")));
            Engine.Result res = engine.execute(new LinkedHashMap<>(), items);
            String err = "nil";
            List<String> values = new ArrayList<>();
            if (res.error != null) {
                err = res.error.getMessage();
            } else {
                for (Map<String, Object> it : res.items) {
                    values.add(it.get("id") + ":" + it.get("_source"));
                }
            }
            System.out.println("scenario=" + name + " error=" + err + " items=[" + String.join(";", values) + "]");
        } finally {
            engine.close();
        }
    }

    public static void main(String[] args) throws Exception {
        AllOperators.ensureRegistered();
        Registry.registerGlobal(new OperatorSchema("probe224_merge", OperatorType.MERGE,
                "probe: row-set merge rewriting _source", Map.of()), () -> new RowSetMerge(false));
        Registry.registerGlobal(new OperatorSchema("probe224_merge_extra", OperatorType.MERGE,
                "probe: row-set merge also writing an undeclared field", Map.of()), () -> new RowSetMerge(true));
        Registry.registerGlobal(new OperatorSchema("probe224_merge_plain", OperatorType.MERGE,
                "probe: merge without row-set markers", Map.of()), () -> new MergeOp(false));

        // MutatesRowSet and reads _source: the rewrite is exempt.
        run("exempt", config("probe224_merge", "[\"id\", \"_source\"]"));
        // Other undeclared writes are still reported, without _source.
        run("exempt_other_undeclared", config("probe224_merge_extra", "[\"id\", \"_source\"]"));
        // Does not read _source: no exemption.
        run("no_source_input", config("probe224_merge", "[\"id\"]"));
        // Not MutatesRowSet: no exemption.
        run("no_row_set_markers", config("probe224_merge_plain", "[\"id\", \"_source\"]"));
    }
}
