package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.OperatorInput;
import page.liam.pine.OperatorOutput;

import java.util.*;

public class MergeDedup extends AbstractOperator {
    private String strategy;

    @Override
    public void init(Map<String, Object> params) throws Exception {
        strategy = (String) params.getOrDefault("strategy", "first");
        if (!"first".equals(strategy)) {
            throw new IllegalArgumentException("merge_dedup: unsupported strategy: " + strategy);
        }
    }

    @Override
    public void execute(OperatorInput input, OperatorOutput output) {
        String dedupBy = itemInput.get(0);
        Set<Object> seen = new LinkedHashSet<>();
        for (int i = 0; i < input.itemCount(); i++) {
            Object key = input.item(i, dedupBy);
            String keyStr = String.valueOf(key);
            if (seen.contains(keyStr)) {
                output.removeItem(i);
            } else {
                seen.add(keyStr);
            }
        }
    }
}
