package page.liam.pine;

import page.liam.pine.operators.AllOperators;

import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicReference;

public class Engine {
    private final List<CompiledOperator> operators;
    private final DAG dag;
    private final Config.FlowContract contract;
    private final ResourceProvider resourceProvider;

    private Engine(List<CompiledOperator> operators, DAG dag, Config.FlowContract contract,
                   ResourceProvider resourceProvider) {
        this.operators = operators;
        this.dag = dag;
        this.contract = contract;
        this.resourceProvider = resourceProvider;
    }

    public static Engine create(byte[] jsonConfig) throws Exception {
        return create(jsonConfig, null);
    }

    public static Engine create(byte[] jsonConfig, ResourceProvider resources) throws Exception {
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

            // Validate data_parallel constraints
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
        return new Engine(compiledOps, dag, cfg.flowContract, resources);
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

        // Per-node latch: signaled after the node's output has been applied to the frame.
        CountDownLatch[] applied = new CountDownLatch[n];
        for (int i = 0; i < n; i++) {
            applied[i] = new CountDownLatch(1);
        }

        AtomicReference<Exception> fatalError = new AtomicReference<>();
        ForkJoinPool pool = ForkJoinPool.commonPool();
        CountDownLatch allDone = new CountDownLatch(n);

        for (int i = 0; i < n; i++) {
            final int idx = i;
            pool.execute(() -> {
                try {
                    DAG.Node node = dag.nodes.get(idx);
                    CompiledOperator cop = operators.get(idx);
                    Config.OperatorConfig opCfg = cop.config;

                    // Wait for all DAG predecessors to have applied their output
                    for (int pred : node.preds) {
                        applied[pred].await();
                        if (fatalError.get() != null) return;
                    }

                    if (fatalError.get() != null) return;

                    // Evaluate skip
                    boolean skipped = false;
                    for (String skipField : opCfg.skip) {
                        Object skipVal = frame.common(skipField);
                        if (skipVal != null && !Boolean.FALSE.equals(skipVal)) {
                            skipped = true;
                            break;
                        }
                    }

                    if (skipped) return;

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

                    // Inject resource provider
                    if (cop.instance instanceof ResourceAware) {
                        if (resourceProvider == null) {
                            throw new IllegalStateException(
                                    "operator \"" + cop.name + "\" implements ResourceAware but no ResourceProvider was supplied");
                        }
                        ((ResourceAware) cop.instance).setResourceProvider(resourceProvider);
                    }

                    // Execute
                    OperatorOutput output;
                    if (opCfg.dataParallel > 1) {
                        output = ParallelExecutor.execute(cop.instance, input, opCfg.dataParallel);
                    } else {
                        output = new OperatorOutput();
                        cop.instance.execute(input, output);
                    }

                    // Apply output
                    frame.applyOutput(output, cop.name, opCfg.recall);

                } catch (Exception e) {
                    fatalError.compareAndSet(null, e);
                } finally {
                    applied[idx].countDown();
                    allDone.countDown();
                }
            });
        }

        allDone.await();

        Exception err = fatalError.get();
        if (err != null) {
            throw err;
        }

        // Project result
        Map<String, Object> resultCommon = frame.toResultCommon(contract.commonOutput);
        List<Map<String, Object>> resultItems = frame.toResultItems(contract.itemOutput);

        return new Result(resultCommon, resultItems);
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

    public static class Result {
        public final Map<String, Object> common;
        public final List<Map<String, Object>> items;

        public Result(Map<String, Object> common, List<Map<String, Object>> items) {
            this.common = common;
            this.items = items;
        }
    }
}
