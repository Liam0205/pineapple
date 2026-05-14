package com.tipsy.pine.operators;

import com.tipsy.pine.AbstractOperator;
import com.tipsy.pine.OperatorInput;
import com.tipsy.pine.OperatorOutput;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

public class ReorderSort extends AbstractOperator {
    private boolean ascending;

    @Override
    public void init(Map<String, Object> params) throws Exception {
        String order = (String) params.getOrDefault("order", "desc");
        switch (order) {
            case "asc":
                ascending = true;
                break;
            case "desc":
                ascending = false;
                break;
            default:
                throw new IllegalArgumentException("reorder_sort: unsupported order: " + order);
        }
    }

    @Override
    public void execute(OperatorInput input, OperatorOutput output) throws Exception {
        int n = input.itemCount();
        if (n == 0) return;

        String field = itemInput.get(0);
        List<int[]> entries = new ArrayList<>(n);
        double[] vals = new double[n];
        for (int i = 0; i < n; i++) {
            vals[i] = toDouble(input.item(i, field));
            entries.add(new int[]{i});
        }

        Integer[] indices = new Integer[n];
        for (int i = 0; i < n; i++) indices[i] = i;

        final double[] sortVals = vals;
        java.util.Arrays.sort(indices, (a, b) -> {
            int cmp = Double.compare(sortVals[a], sortVals[b]);
            return ascending ? cmp : -cmp;
        });

        List<Integer> order = new ArrayList<>(n);
        for (int idx : indices) {
            order.add(idx);
        }
        output.setItemOrder(order);
    }

    private static double toDouble(Object v) throws Exception {
        if (v instanceof Number) {
            return ((Number) v).doubleValue();
        }
        throw new Exception("cannot convert " + (v == null ? "null" : v.getClass().getName()) + " to double");
    }
}
