package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.OperatorInput;
import page.liam.pine.OperatorOutput;

import java.util.HashMap;
import java.util.List;
import java.util.Map;

public class RecallStatic extends AbstractOperator {
    private List<Map<String, Object>> items;

    @Override
    @SuppressWarnings("unchecked")
    public void init(Map<String, Object> params) throws Exception {
        Object raw = params.get("items");
        if (raw == null) {
            throw new IllegalArgumentException("recall_static: missing required param 'items'");
        }
        if (!(raw instanceof List)) {
            throw new IllegalArgumentException("recall_static: 'items' must be a list");
        }
        List<?> list = (List<?>) raw;
        for (int i = 0; i < list.size(); i++) {
            if (!(list.get(i) instanceof Map)) {
                throw new IllegalArgumentException("recall_static: items[" + i + "] must be a map");
            }
        }
        items = new java.util.ArrayList<>(list.size());
        for (Object o : list) {
            items.add(new HashMap<>((Map<String, Object>) o));
        }
    }

    @Override
    public void execute(OperatorInput input, OperatorOutput output) {
        for (Map<String, Object> item : items) {
            Map<String, Object> copy = new HashMap<>(item);
            output.addItem(copy);
        }
    }
}
