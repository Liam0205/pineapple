package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.CancellationToken;
import page.liam.pine.OperatorParams;
import page.liam.pine.OperatorInput;
import page.liam.pine.OperatorOutput;

import java.util.HashMap;
import java.util.List;
import java.util.Map;

/**
 * Operator: recall_static
 * Metadata contract
 *   CommonInput:  []
 *   CommonOutput: []
 *   ItemInput:    []
 *   ItemOutput:   [item_id, ...]
 */
public class RecallStatic extends AbstractOperator {
    private List<Map<String, Object>> items;

    @Override
    @SuppressWarnings("unchecked")
    public void init(OperatorParams params) {
        Object raw = params.get("items");
        if (raw == null) {
            throw new IllegalArgumentException("recall_static: missing required param 'items'");
        }
        if (!(raw instanceof List)) {
            throw new IllegalArgumentException("recall_static: 'items' must be a JSON array, got " + raw.getClass().getSimpleName());
        }
        List<?> list = (List<?>) raw;
        for (int i = 0; i < list.size(); i++) {
            if (!(list.get(i) instanceof Map)) {
                throw new IllegalArgumentException("recall_static: items[" + i + "] must be an object, got " + (list.get(i) == null ? "null" : list.get(i).getClass().getSimpleName()));
            }
        }
        items = new java.util.ArrayList<>(list.size());
        for (Object o : list) {
            items.add(new HashMap<>((Map<String, Object>) o));
        }
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        for (Map<String, Object> item : items) {
            Map<String, Object> copy = new HashMap<>(item);
            output.addItem(copy);
        }
    }
}
