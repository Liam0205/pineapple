package page.liam.pine;

import page.liam.pine.metrics.Provider;
import page.liam.pine.metrics.NopProvider;
import page.liam.pine.operators.AllOperators;

import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;

public class Engine {
    private static final AtomicBoolean logPrefixSet = new AtomicBoolean();

    private final List<CompiledOperator> operators;
    private final DAG dag;
    private final Config.FlowContract contract;
    private final ResourceProvider resourceProvider;
    private final Stats stats;
    private final EngineMetrics engineMetrics;
    private final String storageMode;
    private final ExecutorService executor;

    private Engine(List<CompiledOperator> operators, DAG dag, Config.FlowContract contract,
                   ResourceProvider resourceProvider, Stats stats, EngineMetrics engineMetrics,
                   String storageMode, ExecutorService executor) {
        this.operators = operators;
        this.dag = dag;
        this.contract = contract;
        this.resourceProvider = resourceProvider;
        this.stats = stats;
        this.engineMetrics = engineMetrics;
        this.storageMode = storageMode;
        this.executor = executor;
    }

    // --- Option pattern ---

    @FunctionalInterface
    public interface Option {
        void apply(EngineOptions opts);
    }

    public static class EngineOptions {
        Provider metricsProvider;
        ResourceProvider resources;
        String logPrefix;
        Boolean debug;
        ExecutorService executor;
    }

    public static Option withMetrics(Provider provider) {
        return opts -> opts.metricsProvider = provider;
    }

    public static Option withResources(ResourceProvider resources) {
        return opts -> opts.resources = resources;
    }

    public static Option withLogPrefix(String prefix) {
        return opts -> opts.logPrefix = prefix;
    }

    public static Option withDebug(boolean debug) {
        return opts -> opts.debug = debug;
    }

    public static Option withExecutor(ExecutorService executor) {
        return opts -> opts.executor = executor;
    }

    // --- Factory methods ---

    public static Engine create(byte[] jsonConfig, Option... options) throws PineErrors.ConfigError {
        EngineOptions eo = new EngineOptions();
        for (Option opt : options) {
            opt.apply(eo);
        }
        return createInternal(jsonConfig, eo);
    }

    public static Engine create(byte[] jsonConfig, ResourceProvider resources) throws PineErrors.ConfigError {
        EngineOptions eo = new EngineOptions();
        eo.resources = resources;
        return createInternal(jsonConfig, eo);
    }

    public static Engine create(byte[] jsonConfig, ResourceProvider resources, Provider metricsProvider) throws PineErrors.ConfigError {
        EngineOptions eo = new EngineOptions();
        eo.resources = resources;
        eo.metricsProvider = metricsProvider;
        return createInternal(jsonConfig, eo);
    }

    private static Engine createInternal(byte[] jsonConfig, EngineOptions eo) throws PineErrors.ConfigError {
        AllOperators.ensureRegistered();
        Config cfg = Config.load(jsonConfig);
        Config.ExpandResult expanded = cfg.expandOperatorSequenceWithSubFlows();
        List<String> sequence = expanded.sequence;
        Map<String, String> opToSubFlow = expanded.opToSubFlow;

        validateSourcesOrder(sequence, cfg.pipelineConfig.operators);

        // Resolve log_prefix: Option > JSON config (set once only, like Go's sync.Once)
        String logPrefix = eo.logPrefix != null ? eo.logPrefix : cfg.logPrefix;
        if (!logPrefix.isEmpty() && logPrefixSet.compareAndSet(false, true)) {
            System.setProperty("pine.log.prefix", logPrefix);
        } else if (!logPrefix.isEmpty()) {
            String current = System.getProperty("pine.log.prefix", "");
            if (!logPrefix.equals(current)) {
                System.err.println("[pine] WARNING: log_prefix changed to \"" + logPrefix + "\" but was already set to \"" + current + "\" (ignored, set-once semantics)");
            }
        }

        // Resolve global debug: Option > JSON config
        boolean globalDebug = eo.debug != null ? eo.debug : cfg.debug;

        List<CompiledOperator> compiledOps = new ArrayList<>(sequence.size());
        for (String name : sequence) {
            Config.OperatorConfig opCfg = cfg.pipelineConfig.operators.get(name);

            boolean effectiveDebug = opCfg.debug != null ? opCfg.debug : globalDebug;

            Operator op = Registry.global().buildOperator(opCfg.typeName, opCfg.rawParams);
            OperatorType opType = Registry.global().getType(opCfg.typeName)
                    .orElseThrow(() -> new IllegalStateException("operator type not registered: " + opCfg.typeName));
            String effectiveOperatorType = opType.name().toLowerCase();
            opCfg.operatorType = effectiveOperatorType;

            boolean effectiveRecall = opCfg.recall || opType == OperatorType.RECALL;

            if (op instanceof ConsumesRowSet) {
                opCfg.consumesRowSet = true;
            }
            if (op instanceof MutatesRowSet) {
                opCfg.mutatesRowSet = true;
            }
            if (op instanceof AdditiveWritesRowSet) {
                opCfg.additiveWritesRowSet = true;
            }
            // Validate row-set marker constraints
            if (opCfg.additiveWritesRowSet && opCfg.mutatesRowSet) {
                throw new PineErrors.ConfigError("operator \"" + name + "\": AdditiveWritesRowSet and MutatesRowSet are mutually exclusive");
            }
            if (opType == OperatorType.RECALL && !opCfg.additiveWritesRowSet) {
                throw new PineErrors.ConfigError("operator \"" + name + "\": Recall type must implement AdditiveWritesRowSet");
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

            if (op instanceof DebugAware) {
                ((DebugAware) op).setDebug(name, effectiveDebug);
            }
            if (op instanceof MetricsAware) {
                ((MetricsAware) op).setMetricsProvider(
                    eo.metricsProvider != null ? eo.metricsProvider : NopProvider.getInstance());
            }
            if (op instanceof ResourceAware) {
                // Align with pine-{go,cpp}: don't fail at init when ResourceProvider
                // is missing. The operator itself throws at execute time so that
                // pipelines without resource-dependent operators can construct even
                // when no provider is supplied.
                if (eo.resources != null) {
                    ((ResourceAware) op).setResourceProvider(eo.resources);
                }
            }

            int effectiveDataParallel = opCfg.dataParallel;
            if (effectiveDataParallel < 0) {
                throw new PineErrors.ValidationError("operator \"" + name + "\": data_parallel must be >= 1, got " + effectiveDataParallel);
            }
            if (effectiveDataParallel == 0) {
                effectiveDataParallel = 1;
            }
            if (effectiveDataParallel > 1) {
                if (opType != OperatorType.TRANSFORM) {
                    throw new PineErrors.ValidationError("operator \"" + name + "\": data_parallel=" + effectiveDataParallel + " is only supported for Transform operators, got " + opType.name().toLowerCase());
                }
                if (!opCfg.metadata.commonOutput.isEmpty()) {
                    throw new PineErrors.ValidationError("operator \"" + name + "\": data_parallel=" + effectiveDataParallel + " requires empty $metadata.common_output for Transform operators");
                }
                if (!(op instanceof ConcurrentSafe)) {
                    throw new PineErrors.ValidationError("operator \"" + name + "\": data_parallel=" + effectiveDataParallel + " requires the operator to implement ConcurrentSafe interface (type \"" + opCfg.typeName + "\" does not)");
                }
            }

            // Issue #74: build the per-op {{field}}-interpolation plan.
            // The resolved map is attached to OperatorInput per request,
            // so any operator can read it via input.templatedParam(name)
            // — no opt-in interface required. The Apple compiler has
            // already injected the referenced common fields into
            // common_input, so DAG dependencies guarantee the values
            // exist by the time this operator runs (mirrors how `if_`
            // skip-field dependencies are wired).
            OperatorSchema schema = Registry.global().getSchema(opCfg.typeName)
                    .orElseThrow(() -> new IllegalStateException("operator schema not registered: " + opCfg.typeName));
            List<TemplateResolver.TemplatedParam> templatedPlan =
                    TemplateResolver.buildPlan(name, schema, opCfg.rawParams);

            compiledOps.add(new CompiledOperator(name, op, opCfg, effectiveDebug, effectiveRecall, effectiveDataParallel, effectiveOperatorType, templatedPlan));

            // Pre-compute InputFieldSpec for BuildInput.
            opCfg.inputSpec = InputFieldSpec.compute(opCfg.metadata, opCfg.commonDefaults, opCfg.itemDefaults, opCfg.strictCommon, opCfg.strictItem, opCfg.skip);
        }

        DAG dag = DAG.build(sequence, cfg.pipelineConfig.operators, opToSubFlow);
        Stats stats = new Stats();
        EngineMetrics em = new EngineMetrics(eo.metricsProvider != null ? eo.metricsProvider : NopProvider.getInstance());
        List<String> opNames = compiledOps.stream().map(c -> c.name).toList();
        em.preInitOperators(opNames);
        stats.preInitOperators(opNames);
        ExecutorService pool = eo.executor != null ? eo.executor : Executors.newVirtualThreadPerTaskExecutor();
        return new Engine(compiledOps, dag, cfg.flowContract, eo.resources, stats, em, cfg.storageMode, pool);
    }

    public Result execute(Map<String, Object> common, List<Map<String, Object>> items) {
        return execute(CancellationToken.create(), common, items);
    }

    public Result execute(CancellationToken externalToken, Map<String, Object> common, List<Map<String, Object>> items) {
        if (common == null) {
            throw new PineErrors.ValidationError("request.Common must not be nil");
        }

        for (String field : contract.commonInput) {
            if (!common.containsKey(field)) {
                throw new PineErrors.ValidationError("missing required common input field \"" + field + "\"");
            }
        }

        if (items != null && !items.isEmpty() && !contract.itemInput.isEmpty()) {
            for (int i = 0; i < items.size(); i++) {
                Map<String, Object> item = items.get(i);
                for (String field : contract.itemInput) {
                    if (!item.containsKey(field)) {
                        throw new PineErrors.ValidationError(
                                "item[" + i + "] missing required item input field \"" + field + "\"");
                    }
                }
            }
        }

        Frame frame = Frame.create(storageMode, common, items);
        int n = operators.size();

        long dagStart = System.nanoTime();
        stats.recordRun();
        engineMetrics.schedulerRuns.inc();

        @SuppressWarnings("unchecked")
        CompletableFuture<Void>[] applied = new CompletableFuture[n];
        for (int i = 0; i < n; i++) {
            applied[i] = new CompletableFuture<>();
        }
        CompletableFuture<Void> allDone = CompletableFuture.allOf(applied);

        OpTrace[] traces = new OpTrace[n];
        List<Warning> warnings = Collections.synchronizedList(new ArrayList<>());
        AtomicReference<Exception> fatalError = new AtomicReference<>();
        CancellationToken cancellationToken = CancellationToken.childOf(externalToken);
        AtomicLong activeOps = new AtomicLong();

        ScheduleContext ctx = new ScheduleContext(frame, applied, traces, warnings, fatalError, cancellationToken, activeOps);

        for (int i = 0; i < n; i++) {
            final int idx = i;
            executor.execute(() -> {
                String opName = operators.get(idx).name;
                try {
                    DAG.Node node = dag.nodes.get(idx);
                    CompiledOperator cop = operators.get(idx);

                    for (int pred : node.preds) {
                        try {
                            applied[pred].join();
                        } catch (java.util.concurrent.CompletionException ignored) {
                            // predecessor failed — fatalError already set
                        }
                        if (fatalError.get() != null) return;
                        if (cancellationToken.isCancelled()) return;
                    }

                    if (fatalError.get() != null) return;

                    runOperator(ctx, idx, cop);

                } catch (Exception e) {
                    if (fatalError.compareAndSet(null,
                            new PineErrors.ExecutionError(opName, e))) {
                        cancellationToken.cancel();
                        for (CompletableFuture<Void> f : applied) {
                            f.complete(null);
                        }
                    }
                } catch (Error e) {
                    if (fatalError.compareAndSet(null,
                            new PineErrors.PanicError(opName, e))) {
                        cancellationToken.cancel();
                        for (CompletableFuture<Void> f : applied) {
                            f.complete(null);
                        }
                    }
                } finally {
                    applied[idx].complete(null);
                }
            });
        }

        try {
            allDone.join();
        } catch (java.util.concurrent.CompletionException ignored) {
            // all futures are complete (possibly via force-complete in fatal path)
        }

        // DAG-level metrics
        long dagDuration = System.nanoTime() - dagStart;
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

        Exception err = fatalError.get();

        // Collect non-null traces
        List<OpTrace> traceList = new ArrayList<>();
        for (OpTrace t : traces) {
            if (t != null) traceList.add(t);
        }

        // Project result (even on error, return partial result)
        Map<String, Object> resultCommon = frame.toResultCommon(contract.commonOutput);
        List<Map<String, Object>> resultItems = frame.toResultItems(contract.itemOutput);

        return new Result(resultCommon, resultItems, warnings, traceList, err);
    }

    private void runOperator(ScheduleContext ctx, int idx, CompiledOperator cop) throws PineErrors.OperatorException {
        Config.OperatorConfig opCfg = cop.config;

        long startTime = System.nanoTime();

        // Evaluate skip
        boolean skipped = false;
        for (String skipField : opCfg.skip) {
            Object skipVal = ctx.frame.common(skipField);
            if (skipVal != null && !Boolean.FALSE.equals(skipVal)) {
                skipped = true;
                break;
            }
        }

        if (skipped) {
            long duration = System.nanoTime() - startTime;
            ctx.traces[idx] = new OpTrace(cop.name, startTime, duration, true, null, null);
            stats.recordSkip(cop.name);
            engineMetrics.opSkipTotal.with(cop.name).inc();
            return;
        }

        // Build input
        OperatorInput input = ctx.frame.buildInput(cop.name, opCfg.inputSpec);

        // Resolve templated params (issue #74) — runs once per request
        // before any execute branch. The resolved map is attached to the
        // per-request OperatorInput, so data_parallel shards inherit it
        // via ParallelExecutor rather than the operator instance holding
        // cross-request mutable state.
        //
        // We read source fields from the raw frame rather than the
        // operator's filtered input: meta.common_input_template fields
        // are excluded from the operator-visible input by design, but
        // the DAG ordering guarantees they are present on the frame by
        // the time this call runs.
        if (!cop.templatedPlan.isEmpty()) {
            Map<String, Object> resolved = TemplateResolver.resolve(cop.name, cop.templatedPlan, ctx.frame);
            input.setTemplatedParams(resolved);
        }

        // Debug: capture input snapshot
        Map<String, Object> inputSnapshot = null;
        if (cop.debug) {
            inputSnapshot = snapshotInput(input);
        }

        // Execute
        long current = ctx.activeOps.incrementAndGet();
        stats.recordConcurrency(current);
        engineMetrics.activeOps.add(1);

        OperatorOutput output;
        Exception execErr = null;
        try {
            if (cop.dataParallel > 1) {
                output = ParallelExecutor.execute(ctx.cancellationToken, cop.instance, input, cop.dataParallel, cop.name, executor);
            } else {
                output = new OperatorOutput();
                cop.instance.execute(ctx.cancellationToken, input, output);
            }
        } catch (PineErrors.OperatorException e) {
            output = null;
            execErr = e;
        } catch (RuntimeException e) {
            output = null;
            execErr = e;
        }

        // Validate output type constraints
        if (execErr == null && output != null) {
            Optional<OperatorType> opTypeOpt2 = Registry.global().getType(opCfg.typeName);
            if (opTypeOpt2.isPresent()) {
                String violation = opTypeOpt2.get().validateOutput(output);
                if (violation != null) {
                    execErr = new PineErrors.OperatorException("type violation: " + violation);
                }
            }
        }

        long duration = System.nanoTime() - startTime;
        ctx.activeOps.decrementAndGet();
        engineMetrics.activeOps.add(-1);

        if (execErr != null) {
            ctx.traces[idx] = new OpTrace(cop.name, startTime, duration, false, inputSnapshot, null);
            stats.recordError(cop.name, duration);
            engineMetrics.opErrorTotal.with(cop.name).inc();
            engineMetrics.opExecDuration.with(cop.name).observe(duration / 1_000_000_000.0);
            Exception wrapped;
            if (execErr instanceof PineErrors.PanicError) {
                wrapped = execErr;
            } else if (execErr instanceof PineErrors.OperatorException) {
                wrapped = new PineErrors.ExecutionError(cop.name, execErr);
            } else {
                // RuntimeException (NPE, AIOOBE, etc.) -> PanicError
                wrapped = new PineErrors.PanicError(cop.name, execErr);
            }
            if (ctx.fatalError.compareAndSet(null, wrapped)) {
                ctx.cancellationToken.cancel();
                for (CompletableFuture<Void> f : ctx.applied) {
                    f.complete(null);
                }
            }
            return;
        }

        // Collect warning
        if (output.getWarning() != null) {
            ctx.warnings.add(new Warning(cop.name, output.getWarning()));
        }

        // Debug: capture output snapshot
        Map<String, Object> outputSnapshot = null;
        if (cop.debug) {
            outputSnapshot = snapshotOutput(output);
            int inputSize = input.itemCount();
            int outputSize = inputSize + output.getAddedItems().size() - output.getRemovedItems().size();
            String inputJson = inputSnapshot != null ? toJson(inputSnapshot) : "{}";
            String outputJson = toJson(outputSnapshot);
            System.err.printf("[pine-debug] operator=\"%s\" duration=%s input_size=%d output_size=%d input=%s output=%s%n",
                    cop.name, formatDuration(duration), inputSize, outputSize, inputJson, outputJson);
        }

        // Apply output
        try {
            ctx.frame.applyOutput(output, cop.name, cop.recall);
        } catch (Exception applyErr) {
            ctx.traces[idx] = new OpTrace(cop.name, startTime, duration, false, inputSnapshot, outputSnapshot);
            stats.recordError(cop.name, duration);
            engineMetrics.opErrorTotal.with(cop.name).inc();
            engineMetrics.opExecDuration.with(cop.name).observe(duration / 1_000_000_000.0);
            // ExecutionError thrown from applyOutput (e.g. NaN/Inf validation,
            // SetItemOrder permutation check) already carries the operator
            // name and a structured `pine: execution error in operator "X":
            // <segment>: <inner>` message — avoid double-wrapping with the
            // `apply output: ` prefix which would diverge from Go's output
            // shape.
            Exception wrapped;
            if (applyErr instanceof PineErrors.ExecutionError) {
                wrapped = applyErr;
            } else {
                wrapped = new PineErrors.ExecutionError(cop.name,
                        new Exception("apply output: " + applyErr.getMessage(), applyErr));
            }
            if (ctx.fatalError.compareAndSet(null, wrapped)) {
                ctx.cancellationToken.cancel();
                for (CompletableFuture<Void> f : ctx.applied) {
                    f.complete(null);
                }
            }
            return;
        }

        ctx.traces[idx] = new OpTrace(cop.name, startTime, duration, false, inputSnapshot, outputSnapshot);
        stats.recordExec(cop.name, duration);
        engineMetrics.opExecTotal.with(cop.name).inc();
        engineMetrics.opExecDuration.with(cop.name).observe(duration / 1_000_000_000.0);
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

    /**
     * Tears down every operator that implements {@link Closer}. Called when the
     * engine is retired — during a config hot-reload (on the swapped-out engine)
     * or on shutdown — so operator-held resources (e.g. Lua state pools) are
     * released instead of leaking. Exceptions from individual operators are
     * caught and logged so one failure does not skip the rest.
     */
    public void close() {
        for (CompiledOperator cop : operators) {
            if (cop.instance instanceof Closer) {
                try {
                    ((Closer) cop.instance).close();
                } catch (Exception e) {
                    System.err.println("[pine] operator \"" + cop.name + "\" close: " + e.getMessage());
                }
            }
        }
    }

    public String renderDAG(String format, int collapseLevel) {
        if ("mermaid".equals(format)) {
            return collapseLevel > 0
                    ? DAGVisualizer.renderCollapsedMermaid(dag, collapseLevel)
                    : DAGVisualizer.renderMermaid(dag);
        }
        if (!"dot".equals(format)) {
            throw new PineErrors.ValidationError("unsupported DAG format \"" + format + "\" (use \"dot\" or \"mermaid\")");
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

    private static final com.fasterxml.jackson.databind.ObjectMapper debugMapper = new com.fasterxml.jackson.databind.ObjectMapper();

    private static String toJson(Object obj) {
        try {
            return debugMapper.writeValueAsString(obj);
        } catch (Exception e) {
            return String.valueOf(obj);
        }
    }

    private static String formatDuration(long nanos) {
        if (nanos < 1_000_000) {
            return (nanos / 1000.0) + "µs";
        }
        return (nanos / 1_000_000.0) + "ms";
    }

    // --- Inner types ---

    private static class ScheduleContext {
        final Frame frame;
        final CompletableFuture<Void>[] applied;
        final OpTrace[] traces;
        final List<Warning> warnings;
        final AtomicReference<Exception> fatalError;
        final CancellationToken cancellationToken;
        final AtomicLong activeOps;

        ScheduleContext(Frame frame, CompletableFuture<Void>[] applied, OpTrace[] traces,
                        List<Warning> warnings, AtomicReference<Exception> fatalError,
                        CancellationToken cancellationToken, AtomicLong activeOps) {
            this.frame = frame;
            this.applied = applied;
            this.traces = traces;
            this.warnings = warnings;
            this.fatalError = fatalError;
            this.cancellationToken = cancellationToken;
            this.activeOps = activeOps;
        }
    }

    public static class CompiledOperator {
        public final String name;
        public final Operator instance;
        public final Config.OperatorConfig config;
        public final boolean debug;
        public final boolean recall;
        public final int dataParallel;
        public final String operatorType;
        // Pre-computed per-op plan for {{field}} param interpolation (#74).
        // Empty when the operator has no templated params.
        public final List<TemplateResolver.TemplatedParam> templatedPlan;

        public CompiledOperator(String name, Operator instance, Config.OperatorConfig config,
                                boolean debug, boolean recall, int dataParallel, String operatorType,
                                List<TemplateResolver.TemplatedParam> templatedPlan) {
            this.name = name;
            this.instance = instance;
            this.config = config;
            this.debug = debug;
            this.recall = recall;
            this.dataParallel = dataParallel;
            this.operatorType = operatorType;
            this.templatedPlan = templatedPlan;
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
        public final Exception error;

        public Result(Map<String, Object> common, List<Map<String, Object>> items,
                      List<Warning> warnings, List<OpTrace> trace) {
            this(common, items, warnings, trace, null);
        }

        public Result(Map<String, Object> common, List<Map<String, Object>> items,
                      List<Warning> warnings, List<OpTrace> trace, Exception error) {
            this.common = common;
            this.items = items;
            this.warnings = warnings;
            this.trace = trace;
            this.error = error;
        }
    }

    private static void validateSourcesOrder(List<String> sequence, Map<String, Config.OperatorConfig> operators) {
        Set<String> seen = new HashSet<>();
        for (String name : sequence) {
            Config.OperatorConfig opCfg = operators.get(name);
            if (opCfg != null) {
                for (String src : opCfg.sources) {
                    if (!seen.contains(src)) {
                        throw new PineErrors.ValidationError(
                                "operator \"" + name + "\": sources references \"" + src + "\" which is declared after the current operator (forward reference)");
                    }
                }
            }
            seen.add(name);
        }
    }

}
