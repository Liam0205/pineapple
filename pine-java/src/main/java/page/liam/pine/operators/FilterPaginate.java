package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.CancellationToken;
import page.liam.pine.OperatorParams;
import page.liam.pine.OperatorInput;
import page.liam.pine.OperatorOutput;

import java.util.Map;

/**
 * Operator: filter_paginate
 * Metadata contract
 *   CommonInput:  [<page_field>, <size_field>]
 *   CommonOutput: []
 *   ItemInput:    []
 *   ItemOutput:   []
 */
public class FilterPaginate extends AbstractOperator implements page.liam.pine.ConsumesRowSet, page.liam.pine.MutatesRowSet {
    @Override
    public void init(OperatorParams params) {}

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        int n = input.itemCount();
        if (n == 0) return;

        int page = toInt(input.common(commonInput().get(0)));
        int size = toInt(input.common(commonInput().get(1)));
        if (size <= 0) size = 10;
        if (page < 0) page = 0;

        int start = page * size;
        int end = Math.min(start + size, n);

        for (int i = 0; i < n; i++) {
            if (i < start || i >= end) {
                output.removeItem(i);
            }
        }
    }

    private static int toInt(Object v) {
        if (v instanceof Number) {
            return ((Number) v).intValue();
        }
        return 0;
    }
}
