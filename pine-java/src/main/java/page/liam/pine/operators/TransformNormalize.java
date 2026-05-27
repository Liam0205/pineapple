package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.CancellationToken;
import page.liam.pine.GoTypeNames;
import page.liam.pine.OperatorInput;
import page.liam.pine.OperatorOutput;
import page.liam.pine.OperatorParams;
import page.liam.pine.PineErrors;

/**
 * Operator: transform_normalize
 * Metadata contract
 *   CommonInput:  []
 *   CommonOutput: []
 *   ItemInput:    [<field>]
 *   ItemOutput:   [<output_field>]
 */
public class TransformNormalize extends AbstractOperator {
    private String method;

    @Override
    public void init(OperatorParams params) {
        method = params.getString("method", "min_max");
        if (!"min_max".equals(method)) {
            throw new IllegalArgumentException("transform_normalize: unsupported method \"" + method + "\"");
        }
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) throws PineErrors.OperatorException {
        int n = input.itemCount();
        if (n == 0) return;

        String field = itemInput().get(0);
        String outputField = itemOutput().get(0);

        double[] vals = new double[n];
        for (int i = 0; i < n; i++) {
            try {
                vals[i] = toDouble(input.item(i, field));
            } catch (PineErrors.OperatorException e) {
                throw new PineErrors.OperatorException("transform_normalize: item[" + i + "]." + field + ": " + e.getMessage(), e);
            }
        }

        double min = vals[0], max = vals[0];
        for (int i = 1; i < n; i++) {
            if (vals[i] < min) min = vals[i];
            if (vals[i] > max) max = vals[i];
        }

        double range = max - min;
        for (int i = 0; i < n; i++) {
            double norm = range == 0 ? 0.0 : (vals[i] - min) / range;
            output.setItem(i, outputField, norm);
        }
    }

    private static double toDouble(Object v) throws PineErrors.OperatorException {
        if (v instanceof Number) {
            return ((Number) v).doubleValue();
        }
        // Render Java types using Go reflection terminology so error messages
        // remain byte-identical with pine-{go,cpp,python}.
        throw new PineErrors.OperatorException(
            "cannot convert " + GoTypeNames.of(v) + " to float64");
    }
}
