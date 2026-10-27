package page.liam.pine;

import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;

import page.liam.pine.operators.AllOperators;

/**
 * Embedded-API cancellation parity probe (issue #217).
 *
 * <p>Runs a fixed set of scenarios through {@link Engine#execute} with a
 * cancelled or timing-out {@link RequestContext} and prints one line per
 * scenario:
 *
 * <pre>
 *   scenario=&lt;name&gt; error=&lt;err or "nil"&gt; common=[&lt;sorted common keys&gt;]
 * </pre>
 *
 * cross-validate Section 22 asserts stdout is byte-identical to pine-go's
 * pine-cancel-probe and pine-cpp's pineapple-cancel-probe. Where a cancel must
 * land mid-run, an operator issues it itself, so no timing race decides which
 * nodes run.
 */
public final class CancelProbe {
    private CancelProbe() {
    }

    // Set per scenario; CancelSelf calls it during execute.
    private static volatile Runnable cancelHook = () -> { };

    /** Cancels the request's context from inside the run, ignores it, writes common x. */
    public static final class CancelSelf extends AbstractOperator {
        @Override
        public void init(OperatorParams params) {
        }

        @Override
        public void execute(RequestContext ctx, OperatorInput input, OperatorOutput output) {
            cancelHook.run();
            output.setCommon("x", true);
        }
    }

    /** Writes common y from x. */
    public static final class CopyXToY extends AbstractOperator {
        @Override
        public void init(OperatorParams params) {
        }

        @Override
        public void execute(RequestContext ctx, OperatorInput input, OperatorOutput output) {
            output.setCommon("y", input.common("x"));
        }
    }

    private static final String CHAIN_CONFIG = """
        {
          "pipeline_config": {
            "operators": {
              "op_a": {"type_name": "probe217_cancel_self",
                       "$metadata": {"common_input": [], "common_output": ["x"], "item_input": [], "item_output": []}},
              "op_b": {"type_name": "probe217_copy_x_y",
                       "$metadata": {"common_input": ["x"], "common_output": ["y"], "item_input": [], "item_output": []}}
            },
            "pipeline_map": {"s": {"pipeline": ["op_a", "op_b"]}}
          },
          "pipeline_group": {"main": {"pipeline": ["s"]}},
          "flow_contract": {"common_input": [], "common_output": ["x", "y"], "item_input": [], "item_output": []}
        }""";

    private static final String SINGLE_CONFIG = """
        {
          "pipeline_config": {
            "operators": {
              "op_a": {"type_name": "probe217_cancel_self",
                       "$metadata": {"common_input": [], "common_output": ["x"], "item_input": [], "item_output": []}}
            },
            "pipeline_map": {"s": {"pipeline": ["op_a"]}}
          },
          "pipeline_group": {"main": {"pipeline": ["s"]}},
          "flow_contract": {"common_input": [], "common_output": ["x"], "item_input": [], "item_output": []}
        }""";

    private static final String SLEEP_CONFIG = """
        {
          "pipeline_config": {
            "operators": {
              "sleep": {"type_name": "transform_bench_sleep", "delay_ms": 10000,
                        "$metadata": {"common_input": [], "common_output": [], "item_input": [], "item_output": []}}
            },
            "pipeline_map": {"s": {"pipeline": ["sleep"]}}
          },
          "pipeline_group": {"main": {"pipeline": ["s"]}},
          "flow_contract": {"common_input": [], "common_output": [], "item_input": [], "item_output": []}
        }""";

    private static void run(String name, String config, RequestContext ctx) throws Exception {
        Engine engine = Engine.create(config.getBytes(StandardCharsets.UTF_8));
        try {
            Engine.Result res = engine.execute(ctx, new LinkedHashMap<>(), new ArrayList<>());
            String err = res.error == null ? "nil" : res.error.getMessage();
            List<String> keys = new ArrayList<>();
            for (Map.Entry<String, Object> e : res.common.entrySet()) {
                if (e.getValue() != null) {
                    keys.add(e.getKey());
                }
            }
            Collections.sort(keys);
            System.out.println("scenario=" + name + " error=" + err + " common=[" + String.join(",", keys) + "]");
        } finally {
            engine.close();
        }
    }

    public static void main(String[] args) throws Exception {
        AllOperators.ensureRegistered();
        Registry.registerGlobal(new OperatorSchema("probe217_cancel_self", OperatorType.TRANSFORM,
                "probe: cancels the request context, then writes x", Map.of()), CancelSelf::new);
        Registry.registerGlobal(new OperatorSchema("probe217_copy_x_y", OperatorType.TRANSFORM,
                "probe: copies common x to y", Map.of()), CopyXToY::new);

        // A context cancelled before the run: nothing runs.
        RequestContext.Cancelable pre = RequestContext.withCancel(RequestContext.background());
        pre.cancel();
        cancelHook = () -> { };
        run("pre_cancel", CHAIN_CONFIG, pre);

        // op_a cancels mid-run and completes; op_b is skipped.
        RequestContext.Cancelable mid = RequestContext.withCancel(RequestContext.background());
        cancelHook = mid::cancel;
        run("cancel_mid_run", CHAIN_CONFIG, mid);

        // Cancelled while the only node runs: nothing was skipped, so success.
        RequestContext.Cancelable last = RequestContext.withCancel(RequestContext.background());
        cancelHook = last::cancel;
        run("cancel_last_node", SINGLE_CONFIG, last);

        // transform_bench_sleep stops at the deadline and fails itself.
        try (RequestContext.Cancelable dl =
                     RequestContext.withTimeout(RequestContext.background(), Duration.ofMillis(50))) {
            run("deadline_sleep", SLEEP_CONFIG, dl);
        }

        // ... and on a cancel from another thread.
        ScheduledExecutorService timer = Executors.newSingleThreadScheduledExecutor();
        try (RequestContext.Cancelable c = RequestContext.withCancel(RequestContext.background())) {
            timer.schedule(c::cancel, 50, TimeUnit.MILLISECONDS);
            run("cancel_sleep", SLEEP_CONFIG, c);
        } finally {
            timer.shutdownNow();
        }
    }
}
