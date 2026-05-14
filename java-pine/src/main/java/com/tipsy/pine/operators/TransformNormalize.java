package com.tipsy.pine.operators;

import com.tipsy.pine.AbstractOperator;
import com.tipsy.pine.OperatorInput;
import com.tipsy.pine.OperatorOutput;

import java.util.Map;

public class TransformNormalize extends AbstractOperator {
    private String method;

    @Override
    public void init(Map<String, Object> params) throws Exception {
        method = (String) params.getOrDefault("method", "min_max");
        if (!"min_max".equals(method)) {
            throw new IllegalArgumentException("transform_normalize: unsupported method: " + method);
        }
    }

    @Override
    public void execute(OperatorInput input, OperatorOutput output) throws Exception {
        int n = input.itemCount();
        if (n == 0) return;

        String field = itemInput.get(0);
        String outputField = itemOutput.get(0);

        double[] vals = new double[n];
        for (int i = 0; i < n; i++) {
            vals[i] = toDouble(input.item(i, field));
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

    private static double toDouble(Object v) throws Exception {
        if (v instanceof Number) {
            return ((Number) v).doubleValue();
        }
        throw new Exception("cannot convert " + (v == null ? "null" : v.getClass().getName()) + " to double");
    }
}
