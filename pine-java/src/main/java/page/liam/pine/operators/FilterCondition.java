package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.OperatorInput;
import page.liam.pine.OperatorOutput;

import java.util.Map;
import java.util.Objects;

public class FilterCondition extends AbstractOperator {
    private Object value;

    @Override
    public void init(Map<String, Object> params) {
        this.value = params.get("value");
    }

    @Override
    public void execute(OperatorInput input, OperatorOutput output) {
        String field = itemInput.get(0);
        for (int i = 0; i < input.itemCount(); i++) {
            if (Objects.equals(formatValue(input.item(i, field)), formatValue(value))) {
                output.removeItem(i);
            }
        }
    }

    private static String formatValue(Object v) {
        if (v == null) return "<nil>";
        if (v instanceof Number) {
            double d = ((Number) v).doubleValue();
            if (d == Math.floor(d) && !Double.isInfinite(d) && Math.abs(d) < 1e15) {
                return Long.toString((long) d);
            }
            return formatFloatG(d);
        }
        return v.toString();
    }

    private static String formatFloatG(double d) {
        String s = String.format("%g", d);
        // Remove trailing zeros after decimal point (Go %g behavior)
        if (s.contains(".") && !s.contains("e") && !s.contains("E")) {
            s = s.replaceAll("0+$", "");
            s = s.replaceAll("\\.$", "");
        }
        // Normalize scientific notation: Java 1.00000e+08 → 1e+08
        if (s.contains("e") || s.contains("E")) {
            s = s.toLowerCase();
            int eIdx = s.indexOf('e');
            String mantissa = s.substring(0, eIdx).replaceAll("0+$", "").replaceAll("\\.$", "");
            String exp = s.substring(eIdx);
            s = mantissa + exp;
        }
        return s;
    }
}
