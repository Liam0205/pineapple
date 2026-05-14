package page.liam.pine;

import page.liam.pine.operators.AllOperators;

import java.util.*;

public class Engine {
    private final List<CompiledOperator> operators;
    private final DAG dag;
    private final Config.FlowContract contract;

    private Engine(List<CompiledOperator> operators, DAG dag, Config.FlowContract contract) {
        this.operators = operators;
        this.dag = dag;
        this.contract = contract;
    }

    public static Engine create(byte[] jsonConfig) throws Exception {
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

            compiledOps.add(new CompiledOperator(name, op, opCfg));
        }

        DAG dag = DAG.build(sequence, cfg.pipelineConfig.operators, opToSubFlow);
        return new Engine(compiledOps, dag, cfg.flowContract);
    }

    public Result execute(Map<String, Object> common, List<Map<String, Object>> items) throws Exception {
        if (common == null) {
            throw new IllegalArgumentException("request common must not be null");
        }

        // Validate common inputs
        for (String field : contract.commonInput) {
            if (!common.containsKey(field)) {
                throw new IllegalArgumentException("missing required common input field \"" + field + "\"");
            }
        }

        DataFrame frame = new DataFrame(common, items);

        // Execute in topological order
        List<Integer> order = dag.topologicalOrder();
        for (int idx : order) {
            CompiledOperator cop = operators.get(idx);
            Config.OperatorConfig opCfg = cop.config;

            // Evaluate skip
            boolean skipped = false;
            for (String skipField : opCfg.skip) {
                Object skipVal = frame.common(skipField);
                if (skipVal != null && !Boolean.FALSE.equals(skipVal)) {
                    skipped = true;
                    break;
                }
            }
            if (skipped) continue;

            // Build input (filter out skip fields from commonInput)
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

            OperatorOutput output = new OperatorOutput();
            cop.instance.execute(input, output);

            frame.applyOutput(output, cop.name, opCfg.recall);
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
