package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.CancellationToken;
import page.liam.pine.OperatorInput;
import page.liam.pine.OperatorOutput;
import page.liam.pine.PineErrors;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

/**
 * Operator: reorder_sort
 * Metadata contract
 *   CommonInput:  []
 *   CommonOutput: []
 *   ItemInput:    [<field>]
 *   ItemOutput:   []
 */
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
                throw new IllegalArgumentException("reorder_sort: unsupported order \"" + order + "\"");
        }
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) throws PineErrors.OperatorException {
        int n = input.itemCount();
        if (n == 0) return;

        String field = itemInput.get(0);
        List<int[]> entries = new ArrayList<>(n);
        double[] vals = new double[n];
        for (int i = 0; i < n; i++) {
            try {
                vals[i] = toDouble(input.item(i, field));
            } catch (PineErrors.OperatorException e) {
                throw new PineErrors.OperatorException("reorder_sort: item[" + i + "]." + field + ": " + e.getMessage(), e);
            }
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

    private static double toDouble(Object v) throws PineErrors.OperatorException {
        if (v instanceof Number) {
            return ((Number) v).doubleValue();
        }
        throw new PineErrors.OperatorException("cannot convert " + (v == null ? "null" : v.getClass().getName()) + " to double");
    }
}
