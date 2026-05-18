package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.CancellationToken;
import page.liam.pine.OperatorInput;
import page.liam.pine.OperatorOutput;
import page.liam.pine.OperatorParams;

import java.util.*;

/**
 * Operator: merge_dedup
 * Metadata contract
 *   CommonInput:  []
 *   CommonOutput: []
 *   ItemInput:    [item_id, _source]
 *   ItemOutput:   [item_id]
 */
public class MergeDedup extends AbstractOperator {
    private String strategy;

    @Override
    public void init(OperatorParams params) {
        strategy = params.getString("strategy", "first");
        if (!"first".equals(strategy)) {
            throw new IllegalArgumentException("merge_dedup: unsupported strategy \"" + strategy + "\"");
        }
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        String dedupBy = itemInput().get(0);
        Set<Object> seen = new LinkedHashSet<>();
        for (int i = 0; i < input.itemCount(); i++) {
            Object key = normalizeKey(input.item(i, dedupBy));
            if (seen.contains(key)) {
                output.removeItem(i);
            } else {
                seen.add(key);
            }
        }
    }

    private static Object normalizeKey(Object v) {
        if (v instanceof Number) return ((Number) v).doubleValue();
        return v;
    }
}
