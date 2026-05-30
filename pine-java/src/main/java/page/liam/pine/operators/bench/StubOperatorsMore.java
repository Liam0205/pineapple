package page.liam.pine.operators.bench;

import page.liam.pine.*;

class TransformQueryBlockedCreatorsStub extends AbstractOperator {
    private LatencySampler latency;

    @Override
    public void init(OperatorParams params) {
        latency = LatencySampler.parse(params.toMap());
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        input.common("user_id");
        input.common("blocked_creator_ids");
        output.setCommon("blocked_creator_ids", java.util.List.of());
        if (latency != null) output.setCommon("_bench_cpu_sink", latency.apply());
    }
}

class FilterImpressionStub extends AbstractOperator implements ConsumesRowSet, MutatesRowSet {
    private LatencySampler latency;

    @Override
    public void init(OperatorParams params) {
        latency = LatencySampler.parse(params.toMap());
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        input.common("impression_ids");
        input.common("size");
        for (int i = 0; i < input.itemCount(); i++) {
            input.item(i, "item_id");
            input.item(i, "type");
            if (i % 5 == 0) {
                output.removeItem(i);
            }
        }
        if (latency != null) output.setCommon("_bench_cpu_sink", latency.apply());
    }
}

class FilterBlockedCreatorStub extends AbstractOperator implements ConsumesRowSet, MutatesRowSet {
    private LatencySampler latency;

    @Override
    public void init(OperatorParams params) {
        latency = LatencySampler.parse(params.toMap());
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        input.common("blocked_creator_ids");
        for (int i = 0; i < input.itemCount(); i++) {
            input.item(i, "creator_id");
        }
        if (latency != null) output.setCommon("_bench_cpu_sink", latency.apply());
    }
}

class ReorderTopnBoostStub extends AbstractOperator implements ConsumesRowSet, MutatesRowSet {
    private LatencySampler latency;

    @Override
    public void init(OperatorParams params) {
        latency = LatencySampler.parse(params.toMap());
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        input.common("page");
        input.common("shuffle_salt");
        for (int i = 0; i < input.itemCount(); i++) {
            input.item(i, "id");
            input.item(i, "created_at");
        }
        if (latency != null) output.setCommon("_bench_cpu_sink", latency.apply());
    }
}

class ObserveDatahubStub extends AbstractOperator {
    private LatencySampler latency;

    @Override
    public void init(OperatorParams params) {
        latency = LatencySampler.parse(params.toMap());
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        for (String k : commonInput()) {
            input.common(k);
        }
        for (int i = 0; i < input.itemCount(); i++) {
            for (String k : itemInput()) {
                input.item(i, k);
            }
        }
        if (latency != null) output.setCommon("_bench_cpu_sink", latency.apply());
    }
}

class TransformGenerateRequestIdStub extends AbstractOperator {
    private String prefix = "bench";
    private LatencySampler latency;

    @Override
    public void init(OperatorParams params) {
        Object v = params.get("prefix");
        if (v instanceof String) {
            prefix = (String) v;
        }
        latency = LatencySampler.parse(params.toMap());
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        output.setCommon("request_id", prefix + ":550e8400-e29b-41d4-a716-446655440000");
        if (latency != null) output.setCommon("_bench_cpu_sink", latency.apply());
    }
}
