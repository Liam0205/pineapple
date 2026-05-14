package com.tipsy.pine.operators;

import com.tipsy.pine.AbstractOperator;
import com.tipsy.pine.OperatorInput;
import com.tipsy.pine.OperatorOutput;

import java.util.Map;

public class FilterTruncate extends AbstractOperator {
    private int topN;

    @Override
    public void init(Map<String, Object> params) throws Exception {
        Object v = params.get("top_n");
        if (v instanceof Number) {
            topN = ((Number) v).intValue();
        } else {
            throw new IllegalArgumentException("filter_truncate: top_n must be numeric");
        }
        if (topN < 0) {
            throw new IllegalArgumentException("filter_truncate: top_n must be non-negative");
        }
    }

    @Override
    public void execute(OperatorInput input, OperatorOutput output) {
        for (int i = topN; i < input.itemCount(); i++) {
            output.removeItem(i);
        }
    }
}
