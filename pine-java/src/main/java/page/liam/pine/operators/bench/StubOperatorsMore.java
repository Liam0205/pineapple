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
        if (latency != null) BenchSink.sink = latency.apply();
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
        if (latency != null) BenchSink.sink = latency.apply();
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
        if (latency != null) BenchSink.sink = latency.apply();
    }
}

class ReorderTopnBoostStub extends AbstractOperator implements ConsumesRowSet, MutatesRowSet {
    private int size = 10;
    private LatencySampler latency;

    @Override
    public void init(OperatorParams params) {
        size = params.getInt("size", 10);
        latency = LatencySampler.parse(params.toMap());
    }

    // Deterministic top-N boost: rank items by an FNV-1a hash of
    // "shuffle_salt | id" (mirroring reorder_shuffle_by_salt), boost the top
    // `size` items by hash to the front, and keep the rest in original order.
    // Exercises the row-set reorder path (setItemOrder) under load.
    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        int n = input.itemCount();
        if (n > 0) {
            String saltPrefix = anyToString(input.common("shuffle_salt")) + "|";

            double[] ranks = new double[n];
            long[] ids = new long[n];
            Integer[] indices = new Integer[n];
            for (int i = 0; i < n; i++) {
                String itemVal = anyToString(input.item(i, "id"));
                ranks[i] = hashToUnitInterval(saltPrefix + itemVal);
                ids[i] = parseUint64(itemVal);
                indices[i] = i;
            }

            java.util.Arrays.sort(indices, (a, b) -> {
                int cmp = Double.compare(ranks[a], ranks[b]);
                if (cmp != 0) return cmp;
                int idCmp = Long.compareUnsigned(ids[a], ids[b]);
                if (idCmp != 0) return idCmp;
                return Integer.compare(a, b);
            });

            int boost = Math.max(0, Math.min(size, n));
            boolean[] boosted = new boolean[n];
            java.util.List<Integer> order = new java.util.ArrayList<>(n);
            for (int i = 0; i < boost; i++) {
                order.add(indices[i]);
                boosted[indices[i]] = true;
            }
            for (int i = 0; i < n; i++) {
                if (!boosted[i]) order.add(i);
            }
            output.setItemOrder(order);
        }
        if (latency != null) BenchSink.sink = latency.apply();
    }

    private static double hashToUnitInterval(String s) {
        long h = fnv64a(s);
        double unsigned = (h & 0x7FFFFFFFFFFFFFFFL) + (h < 0 ? 9223372036854775808.0 : 0.0);
        return unsigned / 18446744073709551616.0;
    }

    private static long fnv64a(String s) {
        byte[] data = s.getBytes(java.nio.charset.StandardCharsets.UTF_8);
        long hash = 0xcbf29ce484222325L;
        for (byte b : data) {
            hash ^= (b & 0xFF);
            hash *= 0x100000001b3L;
        }
        return hash;
    }

    private static String anyToString(Object v) {
        if (v == null) return "";
        if (v instanceof String) return (String) v;
        if (v instanceof Boolean) return ((Boolean) v) ? "true" : "false";
        if (v instanceof Number) return GoFormat.formatG(((Number) v).doubleValue());
        if (v instanceof java.util.List || v instanceof java.util.Map) {
            try {
                return new com.fasterxml.jackson.databind.ObjectMapper().writeValueAsString(v);
            } catch (Exception e) {
                return v.toString();
            }
        }
        return v.toString();
    }

    private static long parseUint64(String s) {
        try {
            return Long.parseUnsignedLong(s);
        } catch (NumberFormatException e) {
            return 0L;
        }
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
        if (latency != null) BenchSink.sink = latency.apply();
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
        if (latency != null) BenchSink.sink = latency.apply();
    }
}
