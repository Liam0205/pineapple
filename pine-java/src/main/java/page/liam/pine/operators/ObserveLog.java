package page.liam.pine.operators;

import com.fasterxml.jackson.databind.ObjectMapper;
import page.liam.pine.*;

import java.util.*;

/**
 * Operator: observe_log
 * Metadata contract
 *   CommonInput:  [<fields to observe>]
 *   CommonOutput: []
 *   ItemInput:    [<fields to observe>]
 *   ItemOutput:   []
 */
public class ObserveLog extends AbstractOperator {
    private static final ObjectMapper mapper = new ObjectMapper();
    private String prefix = "";

    @Override
    public void init(OperatorParams params) {
        Object v = params.get("log_prefix");
        if (v instanceof String) {
            prefix = (String) v;
        }
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        Map<String, Object> snapshot = new TreeMap<>();

        if (!commonInput().isEmpty()) {
            Map<String, Object> common = new TreeMap<>();
            for (String k : commonInput()) {
                common.put(k, input.common(k));
            }
            snapshot.put("common", common);
        }

        if (!itemInput().isEmpty() && input.itemCount() > 0) {
            int n = input.itemCount();
            List<Map<String, Object>> items = new ArrayList<>(n);
            for (int i = 0; i < n; i++) {
                items.add(new TreeMap<>());
            }
            for (String k : itemInput()) {
                Object[] col = input.itemColumn(k);
                for (int i = 0; i < n; i++) {
                    items.get(i).put(k, col[i]);
                }
            }
            snapshot.put("items", items);
        }

        try {
            String data = mapper.writeValueAsString(snapshot);
            if (!prefix.isEmpty()) {
                logf("[observe_log] %s %s", prefix, data);
            } else {
                logf("[observe_log] %s", data);
            }
        } catch (Exception e) {
            logf("[observe_log] %s marshal error: %s", prefix, e.getMessage());
        }
    }
}
