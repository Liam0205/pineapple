package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.CancellationToken;
import page.liam.pine.GoTypeNames;
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
public class RecallStatic extends AbstractOperator implements page.liam.pine.AdditiveWritesRowSet {
    private List<Map<String, Object>> items;
    private Map<String, Object> setCommon;

    @Override
    @SuppressWarnings("unchecked")
    public void init(OperatorParams params) {
        Object raw = params.get("items");
        if (raw == null) {
            throw new IllegalArgumentException("recall_static: missing required param 'items'");
        }
        if (!(raw instanceof List)) {
            throw new IllegalArgumentException("recall_static: 'items' must be a JSON array, got " + GoTypeNames.of(raw));
        }
        List<?> list = (List<?>) raw;
        for (int i = 0; i < list.size(); i++) {
            if (!(list.get(i) instanceof Map)) {
                throw new IllegalArgumentException("recall_static: items[" + i + "] must be an object, got " + GoTypeNames.of(list.get(i)));
            }
        }
        items = new java.util.ArrayList<>(list.size());
        for (Object o : list) {
            items.add(new HashMap<>((Map<String, Object>) o));
        }
        Object sc = params.get("set_common");
        if (sc != null) {
            if (!(sc instanceof Map)) {
                throw new IllegalArgumentException("recall_static: 'set_common' must be a JSON object, got " + GoTypeNames.of(sc));
            }
            setCommon = new HashMap<>((Map<String, Object>) sc);
        }
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        if (setCommon != null) {
            for (Map.Entry<String, Object> e : setCommon.entrySet()) {
                output.setCommon(e.getKey(), e.getValue());
            }
        }
        for (Map<String, Object> item : items) {
            Map<String, Object> copy = new HashMap<>(item);
            output.addItem(copy);
        }
    }
}
