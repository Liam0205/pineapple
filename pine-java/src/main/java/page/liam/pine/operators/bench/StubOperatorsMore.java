package page.liam.pine.operators.bench;

import page.liam.pine.*;

class TransformQueryBlockedCreatorsStub extends AbstractOperator {
    @Override
    public void init(OperatorParams params) {}

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        input.common("user_id");
        input.common("blocked_creator_ids");
        output.setCommon("blocked_creator_ids", java.util.List.of());
    }
}

class FilterImpressionStub extends AbstractOperator implements ConsumesRowSet, MutatesRowSet {
    @Override
    public void init(OperatorParams params) {}

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
    }
}

class FilterBlockedCreatorStub extends AbstractOperator implements ConsumesRowSet, MutatesRowSet {
    @Override
    public void init(OperatorParams params) {}

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        input.common("blocked_creator_ids");
        for (int i = 0; i < input.itemCount(); i++) {
            input.item(i, "creator_id");
        }
    }
}

class ReorderTopnBoostStub extends AbstractOperator implements ConsumesRowSet, MutatesRowSet {
    @Override
    public void init(OperatorParams params) {}

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        input.common("page");
        input.common("shuffle_salt");
        for (int i = 0; i < input.itemCount(); i++) {
            input.item(i, "id");
            input.item(i, "created_at");
        }
    }
}

class ObserveDatahubStub extends AbstractOperator {
    @Override
    public void init(OperatorParams params) {}

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
    }
}

class TransformGenerateRequestIdStub extends AbstractOperator {
    private String prefix = "bench";

    @Override
    public void init(OperatorParams params) {
        Object v = params.get("prefix");
        if (v instanceof String) {
            prefix = (String) v;
        }
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        output.setCommon("request_id", prefix + ":550e8400-e29b-41d4-a716-446655440000");
    }
}
