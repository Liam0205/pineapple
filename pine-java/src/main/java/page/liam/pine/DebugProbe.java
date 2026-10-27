package page.liam.pine;

import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import page.liam.pine.debug.Emitter;
import page.liam.pine.debug.NodeMeta;
import page.liam.pine.debug.Recorder;
import page.liam.pine.operators.AllOperators;

/**
 * Embedded-API request-level debug parity probe (issue #219).
 *
 * <p>Runs a fixed set of scenarios through {@link Engine#execute}, attaching
 * (or not) a collecting {@link Emitter}, and prints per scenario:
 *
 * <pre>
 *   scenario=&lt;name&gt; error=&lt;err or "nil"&gt; items=[&lt;per-item output values&gt;]
 *   scenario=&lt;name&gt; event=&lt;operator&gt;/&lt;type&gt;/&lt;subflow&gt;/&lt;kind&gt;/&lt;data&gt;
 * </pre>
 *
 * Event lines are sorted, so concurrent nodes and data_parallel shards cannot
 * reorder them. cross-validate Section 23 asserts stdout is byte-identical to
 * pine-go's pine-debug-probe and pine-cpp's pineapple-debug-probe.
 */
public final class DebugProbe {
    private DebugProbe() {
    }

    private static final Object REQUEST_KEY = new Object();

    /**
     * Copies item "value" into its configured field and, when a recorder is
     * attached, emits the shard's item count and whether the request value
     * survived node binding.
     */
    public static final class EmitOp extends AbstractOperator implements ConcurrentSafe {
        private String field;

        @Override
        public void init(OperatorParams params) {
            field = params.getString("field");
        }

        @Override
        public void execute(RequestContext ctx, OperatorInput input, OperatorOutput output) {
            int n = input.itemCount();
            Recorder r = Recorder.fromContext(ctx);
            if (r != null) {
                r.emit("items", n);
                r.emit("kept", "kept".equals(ctx.value(REQUEST_KEY)) ? 1 : 0);
            }
            for (int i = 0; i < n; i++) {
                output.setItem(i, field, input.item(i, "value"));
            }
        }
    }

    static final class Collector implements Emitter {
        final List<String> lines = new ArrayList<>();

        @Override
        public synchronized void emit(NodeMeta meta, String kind, Object data) {
            lines.add(meta.operator() + "/" + meta.type() + "/" + meta.subFlow() + "/" + kind + "/" + data);
        }
    }

    private static String opJson(String name, String field, int dataParallel) {
        String dp = dataParallel > 1 ? ", \"data_parallel\": " + dataParallel : "";
        return "\"" + name + "\": {\"type_name\": \"probe219_emit\", \"field\": \"" + field + "\"" + dp
                + ", \"$metadata\": {\"common_input\": [], \"common_output\": [], \"item_input\": [\"value\"],"
                + " \"item_output\": [\"" + field + "\"]}}";
    }

    // "top" sits directly in the pipeline group, "mid" in SubFlow "outer",
    // and "leaf" in SubFlow "inner", which is nested in "outer".
    private static final String NESTED_CONFIG = "{\"pipeline_config\": {\"operators\": {"
            + opJson("top", "a", 1) + ", " + opJson("mid", "b", 1) + ", " + opJson("leaf", "c", 1) + "},"
            + " \"pipeline_map\": {\"outer\": {\"pipeline\": [\"mid\", \"inner\"]}, \"inner\": {\"pipeline\": [\"leaf\"]}}},"
            + " \"pipeline_group\": {\"main\": {\"pipeline\": [\"top\", \"outer\"]}},"
            + " \"flow_contract\": {\"common_input\": [], \"common_output\": [], \"item_input\": [\"value\"],"
            + " \"item_output\": [\"a\", \"b\", \"c\"]}}";

    private static final String PARALLEL_CONFIG = "{\"pipeline_config\": {\"operators\": {"
            + opJson("rank", "a", 3) + "},"
            + " \"pipeline_map\": {\"scoring\": {\"pipeline\": [\"rank\"]}}},"
            + " \"pipeline_group\": {\"main\": {\"pipeline\": [\"scoring\"]}},"
            + " \"flow_contract\": {\"common_input\": [], \"common_output\": [], \"item_input\": [\"value\"],"
            + " \"item_output\": [\"a\"]}}";

    private static void run(String name, String config, int itemCount, Emitter emitter, Collector c)
            throws Exception {
        Engine engine = Engine.create(config.getBytes(StandardCharsets.UTF_8));
        try {
            List<String> fields = new ArrayList<>();
            for (String f : List.of("a", "b", "c")) {
                if (config.contains("\"field\": \"" + f + "\"")) {
                    fields.add(f);
                }
            }
            List<Map<String, Object>> items = new ArrayList<>();
            for (int i = 0; i < itemCount; i++) {
                Map<String, Object> item = new LinkedHashMap<>();
                item.put("value", i + 1);
                items.add(item);
            }
            RequestContext ctx = Recorder.withEmitter(
                    RequestContext.withValue(RequestContext.background(), REQUEST_KEY, "kept"), emitter);
            Engine.Result res = engine.execute(ctx, new LinkedHashMap<>(), items);
            String err = res.error == null ? "nil" : res.error.getMessage();
            List<String> values = new ArrayList<>();
            for (Map<String, Object> item : res.items) {
                List<String> vs = new ArrayList<>();
                for (String f : fields) {
                    vs.add(String.valueOf(item.get(f)));
                }
                values.add(String.join(":", vs));
            }
            System.out.println("scenario=" + name + " error=" + err + " items=[" + String.join(",", values) + "]");
            if (c != null) {
                Collections.sort(c.lines);
                for (String line : c.lines) {
                    System.out.println("scenario=" + name + " event=" + line);
                }
            }
        } finally {
            engine.close();
        }
    }

    public static void main(String[] args) throws Exception {
        AllOperators.ensureRegistered();
        Registry.registerGlobal(new OperatorSchema("probe219_emit", OperatorType.TRANSFORM,
                "probe: copies item value and emits request debug events",
                Map.of("field", ParamSpec.required("string", "item output field"))), EmitOp::new);

        // No emitter: no recorder reaches the operators, results are unchanged.
        run("no_emitter", NESTED_CONFIG, 2, null, null);

        // NodeMeta carries the operator, its type_name and its direct SubFlow.
        Collector c = new Collector();
        run("node_meta", NESTED_CONFIG, 2, c, c);

        // data_parallel: one event per shard, all with the node's meta.
        c = new Collector();
        run("data_parallel", PARALLEL_CONFIG, 6, c, c);

        // A throwing emitter is isolated: the business result is unchanged.
        run("panicking_emitter", PARALLEL_CONFIG, 6, (meta, kind, data) -> {
            throw new IllegalStateException("emitter broke");
        }, null);
    }
}
