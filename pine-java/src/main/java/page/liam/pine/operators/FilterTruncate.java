package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.OperatorInput;
import page.liam.pine.OperatorOutput;

import java.util.Map;

public class FilterTruncate extends AbstractOperator {
    private long topN;

    @Override
    public void init(Map<String, Object> params) throws Exception {
        Object v = params.get("top_n");
        if (v instanceof Number) {
            topN = ((Number) v).longValue();
        } else {
            throw new IllegalArgumentException("filter_truncate: top_n must be numeric");
        }
        if (topN < 0) {
            throw new IllegalArgumentException("filter_truncate: top_n must be non-negative");
        }
    }

    @Override
    public void execute(OperatorInput input, OperatorOutput output) {
        for (int i = (int) Math.min(topN, input.itemCount()); i < input.itemCount(); i++) {
            output.removeItem(i);
        }
    }
}
