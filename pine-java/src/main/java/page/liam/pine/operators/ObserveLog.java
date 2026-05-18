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
            List<Map<String, Object>> items = new ArrayList<>(input.itemCount());
            for (int i = 0; i < input.itemCount(); i++) {
                Map<String, Object> row = new TreeMap<>();
                for (String k : itemInput()) {
                    row.put(k, input.item(i, k));
                }
                items.add(row);
            }
            snapshot.put("items", items);
        }

        try {
            String data = mapper.writeValueAsString(snapshot);
            if (!prefix.isEmpty()) {
                System.err.printf("[observe_log] %s %s%n", prefix, data);
            } else {
                System.err.printf("[observe_log] %s%n", data);
            }
        } catch (Exception e) {
            System.err.printf("[observe_log] %s marshal error: %s%n", prefix, e.getMessage());
        }
    }
}
