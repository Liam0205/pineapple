package page.liam.pine;

import page.liam.pine.metrics.Provider;
import page.liam.pine.metrics.NopProvider;
import page.liam.pine.operators.AllOperators;

import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;

public class Engine {
    private final List<CompiledOperator> operators;
    private final DAG dag;
    private final Config.FlowContract contract;
    private final ResourceProvider resourceProvider;
    private final Stats stats;
    private final EngineMetrics engineMetrics;

    private Engine(List<CompiledOperator> operators, DAG dag, Config.FlowContract contract,
                   ResourceProvider resourceProvider, Stats stats, EngineMetrics engineMetrics) {
        this.operators = operators;
        this.dag = dag;
        this.contract = contract;
        this.resourceProvider = resourceProvider;
        this.stats = stats;
        this.engineMetrics = engineMetrics;
    }

    public static Engine create(byte[] jsonConfig) throws Exception {
        return create(jsonConfig, null, null);
    }

    public static Engine create(byte[] jsonConfig, ResourceProvider resources) throws Exception {
        return create(jsonConfig, resources, null);
    }

    public static Engine create(byte[] jsonConfig, ResourceProvider resources, Provider metricsProvider) throws Exception {
        AllOperators.ensureRegistered();
        Config cfg = Config.load(jsonConfig);
        Config.ExpandResult expanded = cfg.expandOperatorSequenceWithSubFlows();
        List<String> sequence = expanded.sequence;
        Map<String, String> opToSubFlow = expanded.opToSubFlow;

        List<CompiledOperator> compiledOps = new ArrayList<>(sequence.size());
        for (String name : sequence) {
            Config.OperatorConfig opCfg = cfg.pipelineConfig.operators.get(name);

            Operator op = Registry.buildOperator(opCfg.typeName, opCfg.rawParams);
            OperatorType opType = Registry.getType(opCfg.typeName);
            opCfg.operatorType = opType != null ? opType.name().toLowerCase() : "transform";

            if (opType == OperatorType.RECALL) {
                opCfg.recall = true;
            }

            if (op instanceof MetadataAware) {
                List<String> commonIn = new ArrayList<>(opCfg.metadata.commonInput);
                for (String skipField : opCfg.skip) {
                    commonIn.remove(skipField);
                }
                ((MetadataAware) op).setMetadata(
                        commonIn,
                        opCfg.metadata.commonOutput,
                        opCfg.metadata.itemInput,
                        opCfg.metadata.itemOutput
                );
            }

            if (opCfg.dataParallel < 0) {
                throw new IllegalArgumentException("operator \"" + name + "\": data_parallel must be >= 1, got " + opCfg.dataParallel);
            }
            if (opCfg.dataParallel == 0) {
                opCfg.dataParallel = 1;
            }
            if (opCfg.dataParallel > 1) {
                if (opType != OperatorType.TRANSFORM) {
                    throw new IllegalArgumentException("operator \"" + name + "\": data_parallel=" + opCfg.dataParallel + " is only supported for Transform operators");
                }
                if (!opCfg.metadata.commonOutput.isEmpty()) {
                    throw new IllegalArgumentException("operator \"" + name + "\": data_parallel=" + opCfg.dataParallel + " requires empty common_output");
                }
                if (!(op instanceof ConcurrentSafe)) {
                    throw new IllegalArgumentException("operator \"" + name + "\": data_parallel=" + opCfg.dataParallel + " requires ConcurrentSafe");
                }
            }

            compiledOps.add(new CompiledOperator(name, op, opCfg));
        }

        DAG dag = DAG.build(sequence, cfg.pipelineConfig.operators, opToSubFlow);
        Stats stats = new Stats();
        EngineMetrics em = metricsProvider != null ? new EngineMetrics(metricsProvider) : null;
        return new Engine(compiledOps, dag, cfg.flowContract, resources, stats, em);
    }

    public Result execute(Map<String, Object> common, List<Map<String, Object>> items) throws Exception {
        if (common == null) {
            throw new IllegalArgumentException("request common must not be null");
        }

        for (String field : contract.commonInput) {
            if (!common.containsKey(field)) {
                throw new IllegalArgumentException("missing required common input field \"" + field + "\"");
            }
        }

        DataFrame frame = new DataFrame(common, items);
        int n = operators.size();

        long dagStart = System.nanoTime();
        stats.recordRun();
        if (engineMetrics != null) {
            engineMetrics.schedulerRuns.inc();
        }

        CountDownLatch[] applied = new CountDownLatch[n];
        for (int i = 0; i < n; i++) {
            applied[i] = new CountDownLatch(1);
        }

        OpTrace[] traces = new OpTrace[n];
        List<Warning> warnings = Collections.synchronizedList(new ArrayList<>());
        AtomicReference<Exception> fatalError = new AtomicReference<>();
        AtomicLong activeOps = new AtomicLong();
        ForkJoinPool pool = ForkJoinPool.commonPool();
        CountDownLatch allDone = new CountDownLatch(n);

        for (int i = 0; i < n; i++) {
            final int idx = i;
            pool.execute(() -> {
                try {
                    DAG.Node node = dag.nodes.get(idx);
                    CompiledOperator cop = operators.get(idx);
                    Config.OperatorConfig opCfg = cop.config;

                    for (int pred : node.preds) {
                        applied[pred].await();
                        if (fatalError.get() != null) return;
                    }

                    if (fatalError.get() != null) return;

                    long startTime = System.nanoTime();

                    // Evaluate skip
                    boolean skipped = false;
                    for (String skipField : opCfg.skip) {
                        Object skipVal = frame.common(skipField);
                        if (skipVal != null && !Boolean.FALSE.equals(skipVal)) {
                            skipped = true;
                            break;
                        }
                    }

                    if (skipped) {
                        long duration = System.nanoTime() - startTime;
                        traces[idx] = new OpTrace(cop.name, startTime, duration, true, null, null);
                        stats.recordSkip(cop.name);
                        if (engineMetrics != null) {
                            engineMetrics.opSkipTotal.with(cop.name).inc();
                        }
                        return;
                    }

                    // Build input
                    List<String> commonInput = new ArrayList<>(opCfg.metadata.commonInput);
                    for (String skipField : opCfg.skip) {
                        commonInput.remove(skipField);
                    }

                    OperatorInput input = frame.buildInput(
                            commonInput,
                            opCfg.metadata.itemInput,
                            opCfg.commonDefaults,
                            opCfg.itemDefaults
                    );

                    // Debug: capture input snapshot
                    Map<String, Object> inputSnapshot = null;
                    if (opCfg.debug) {
                        inputSnapshot = snapshotInput(input);
                    }

                    // Inject resource provider
                    if (cop.instance instanceof ResourceAware) {
                        if (resourceProvider == null) {
                            throw new IllegalStateException(
                                    "operator \"" + cop.name + "\" implements ResourceAware but no ResourceProvider was supplied");
                        }
                        ((ResourceAware) cop.instance).setResourceProvider(resourceProvider);
                    }

                    // Execute
                    long current = activeOps.incrementAndGet();
                    stats.recordConcurrency(current);
                    if (engineMetrics != null) {
                        engineMetrics.activeOps.add(1);
                    }

                    OperatorOutput output;
                    Exception execErr = null;
                    try {
                        if (opCfg.dataParallel > 1) {
                            output = ParallelExecutor.execute(cop.instance, input, opCfg.dataParallel);
                        } else {
                            output = new OperatorOutput();
                            cop.instance.execute(input, output);
                        }
                    } catch (Exception e) {
                        output = null;
                        execErr = e;
                    }

                    long duration = System.nanoTime() - startTime;
                    activeOps.decrementAndGet();
                    if (engineMetrics != null) {
                        engineMetrics.activeOps.add(-1);
                    }

                    if (execErr != null) {
                        traces[idx] = new OpTrace(cop.name, startTime, duration, false, inputSnapshot, null);
                        stats.recordError(cop.name, duration);
                        if (engineMetrics != null) {
                            engineMetrics.opErrorTotal.with(cop.name).inc();
                            engineMetrics.opExecDuration.with(cop.name).observe(duration / 1_000_000_000.0);
                        }
                        fatalError.compareAndSet(null, execErr);
                        return;
                    }

                    // Collect warning
                    if (output.getWarning() != null) {
                        warnings.add(new Warning(cop.name, output.getWarning()));
                    }

                    // Debug: capture output snapshot
                    Map<String, Object> outputSnapshot = null;
                    if (opCfg.debug) {
                        outputSnapshot = snapshotOutput(output);
                    }

                    // Apply output
                    frame.applyOutput(output, cop.name, opCfg.recall);

                    traces[idx] = new OpTrace(cop.name, startTime, duration, false, inputSnapshot, outputSnapshot);
                    stats.recordExec(cop.name, duration);
                    if (engineMetrics != null) {
                        engineMetrics.opExecTotal.with(cop.name).inc();
                        engineMetrics.opExecDuration.with(cop.name).observe(duration / 1_000_000_000.0);
                    }

                } catch (Exception e) {
                    fatalError.compareAndSet(null, e);
                } finally {
                    applied[idx].countDown();
                    allDone.countDown();
                }
            });
        }

        allDone.await();

        // DAG-level metrics
        long dagDuration = System.nanoTime() - dagStart;
        if (engineMetrics != null) {
            engineMetrics.dagExecDuration.observe(dagDuration / 1_000_000_000.0);
            if (fatalError.get() != null) {
                engineMetrics.dagExecTotal.with("error").inc();
            } else {
                engineMetrics.dagExecTotal.with("success").inc();
            }
            int executed = 0;
            for (OpTrace t : traces) {
                if (t != null && !t.skipped) executed++;
            }
            engineMetrics.dagOpsExecuted.observe(executed);
        }

        Exception err = fatalError.get();
        if (err != null) {
            throw err;
        }

        // Collect non-null traces
        List<OpTrace> traceList = new ArrayList<>();
        for (OpTrace t : traces) {
            if (t != null) traceList.add(t);
        }

        // Project result
        Map<String, Object> resultCommon = frame.toResultCommon(contract.commonOutput);
        List<Map<String, Object>> resultItems = frame.toResultItems(contract.itemOutput);

        return new Result(resultCommon, resultItems, warnings, traceList);
    }

    // --- Public API ---

    public Map<String, Map<String, Object>> stats() {
        return stats.snapshot();
    }

    public Map<String, Object> schedulerStats() {
        return stats.schedulerSnapshot();
    }

    public Map<String, Map<String, Long>> operatorCustomStats() {
        Map<String, Map<String, Long>> result = new LinkedHashMap<>();
        for (CompiledOperator cop : operators) {
            if (cop.instance instanceof StatsProvider) {
                Map<String, Long> custom = ((StatsProvider) cop.instance).operatorStats();
                if (custom != null && !custom.isEmpty()) {
                    result.put(cop.name, custom);
                }
            }
        }
        return result.isEmpty() ? null : result;
    }

    public String renderDAG(String format, int collapseLevel) {
        if ("mermaid".equalsIgnoreCase(format)) {
            return collapseLevel > 0
                    ? DAGVisualizer.renderCollapsedMermaid(dag, collapseLevel)
                    : DAGVisualizer.renderMermaid(dag);
        }
        return collapseLevel > 0
                ? DAGVisualizer.renderCollapsedDot(dag, collapseLevel)
                : DAGVisualizer.renderDot(dag);
    }

    // --- Snapshot helpers ---

    private static Map<String, Object> snapshotInput(OperatorInput in) {
        Map<String, Object> snap = new LinkedHashMap<>();
        Map<String, Object> common = in.rawCommon();
        if (!common.isEmpty()) {
            snap.put("common", new LinkedHashMap<>(common));
        }
        List<Map<String, Object>> items = in.rawItems();
        if (!items.isEmpty()) {
            boolean hasData = false;
            List<Map<String, Object>> copy = new ArrayList<>(items.size());
            for (Map<String, Object> row : items) {
                copy.add(new LinkedHashMap<>(row));
                if (!row.isEmpty()) hasData = true;
            }
            if (hasData) snap.put("items", copy);
        }
        return snap;
    }

    private static Map<String, Object> snapshotOutput(OperatorOutput out) {
        Map<String, Object> snap = new LinkedHashMap<>();
        if (!out.getCommonWrites().isEmpty()) {
            snap.put("common_writes", new LinkedHashMap<>(out.getCommonWrites()));
        }
        if (!out.getItemWrites().isEmpty()) {
            snap.put("item_writes", new LinkedHashMap<>(out.getItemWrites()));
        }
        if (!out.getAddedItems().isEmpty()) {
            snap.put("added_items", new ArrayList<>(out.getAddedItems()));
        }
        if (!out.getRemovedItems().isEmpty()) {
            snap.put("removed_items", new ArrayList<>(out.getRemovedItems()));
        }
        return snap;
    }

    // --- Inner types ---

    public static class CompiledOperator {
        public final String name;
        public final Operator instance;
        public final Config.OperatorConfig config;

        public CompiledOperator(String name, Operator instance, Config.OperatorConfig config) {
            this.name = name;
            this.instance = instance;
            this.config = config;
        }
    }

    public static class Warning {
        public final String operator;
        public final Exception err;

        public Warning(String operator, Exception err) {
            this.operator = operator;
            this.err = err;
        }
    }

    public static class Result {
        public final Map<String, Object> common;
        public final List<Map<String, Object>> items;
        public final List<Warning> warnings;
        public final List<OpTrace> trace;

        public Result(Map<String, Object> common, List<Map<String, Object>> items,
                      List<Warning> warnings, List<OpTrace> trace) {
            this.common = common;
            this.items = items;
            this.warnings = warnings;
            this.trace = trace;
        }
    }
}
