package page.liam.pine.operators.bench;

import page.liam.pine.*;

import java.util.LinkedHashMap;
import java.util.Map;

class RecallFeedDataStub extends AbstractOperator implements AdditiveWritesRowSet {
    private int itemCount = 3000;
    private LatencySampler latency;

    @Override
    public void init(OperatorParams params) {
        Object v = params.get("bench_item_count");
        if (v instanceof Number) {
            itemCount = ((Number) v).intValue();
        }
        latency = LatencySampler.parse(params.toMap());
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        for (int i = 0; i < itemCount; i++) {
            Map<String, Object> row = new LinkedHashMap<>();
            row.put("id", (double) (i + 1));
            row.put("item_id", String.valueOf(10000 + i));
            row.put("type", (double) (i % 3 + 1));
            row.put("score", (double) (1000 - i));
            row.put("created_at", "2026-01-01T00:00:00Z");
            output.addItem(row);
        }
        if (latency != null) output.setCommon("_bench_cpu_sink", latency.apply());
    }
}

class TransformRedisZrangebyscoreStub extends AbstractOperator {
    private LatencySampler latency;

    @Override
    public void init(OperatorParams params) {
        latency = LatencySampler.parse(params.toMap());
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        input.common("user_id");
        output.setCommon("impression_ids", java.util.List.of());
        output.setCommon("impression_cache_hit", true);
        output.setCommon("impression_ids_len", 0.0);
        if (latency != null) output.setCommon("_bench_cpu_sink", latency.apply());
    }
}

class TransformHydrateStub extends AbstractOperator implements ConsumesRowSet {
    private LatencySampler latency;

    @Override
    public void init(OperatorParams params) {
        latency = LatencySampler.parse(params.toMap());
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        for (int i = 0; i < input.itemCount(); i++) {
            input.item(i, "item_id");
            input.item(i, "type");
            output.setItem(i, "creator_id", (double) (i % 1000));
        }
        if (latency != null) output.setCommon("_bench_cpu_sink", latency.apply());
    }
}
