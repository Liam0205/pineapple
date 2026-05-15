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
            if (d == Math.floor(d) && !Double.isInfinite(d)) {
                return Long.toString((long) d);
            }
            return Double.toString(d);
        }
        return v.toString();
    }
}
