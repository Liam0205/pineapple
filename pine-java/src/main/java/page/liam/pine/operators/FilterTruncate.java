package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.CancellationToken;
import page.liam.pine.OperatorParams;
import page.liam.pine.OperatorInput;
import page.liam.pine.OperatorParams;
import page.liam.pine.OperatorOutput;
import page.liam.pine.OperatorParams;

import java.util.Map;

/**
 * Operator: filter_truncate
 * Metadata contract
 *   CommonInput:  []
 *   CommonOutput: []
 *   ItemInput:    []
 *   ItemOutput:   []
 */
public class FilterTruncate extends AbstractOperator {
    private long topN;

    @Override
    public void init(OperatorParams params) {
        Object v = params.get("top_n");
        if (v instanceof Number) {
            topN = ((Number) v).longValue();
        } else {
            throw new IllegalArgumentException("filter_truncate: top_n must be numeric, got " + (v == null ? "null" : v.getClass().getSimpleName()));
        }
        if (topN < 0) {
            throw new IllegalArgumentException("filter_truncate: top_n must be non-negative, got " + topN);
        }
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        for (int i = (int) Math.min(topN, input.itemCount()); i < input.itemCount(); i++) {
            output.removeItem(i);
        }
    }
}
