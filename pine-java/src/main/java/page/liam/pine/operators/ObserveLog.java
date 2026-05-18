package page.liam.pine.operators;

import com.fasterxml.jackson.databind.ObjectMapper;
import page.liam.pine.*;

import java.util.*;

public class ObserveLog extends AbstractOperator {
    private static final ObjectMapper mapper = new ObjectMapper();
    private String prefix = "";

    @Override
    public void init(Map<String, Object> params) {
        Object v = params.get("log_prefix");
        if (v instanceof String) {
            prefix = (String) v;
        }
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        Map<String, Object> snapshot = new LinkedHashMap<>();

        if (!commonInput.isEmpty()) {
            Map<String, Object> common = new LinkedHashMap<>();
            for (String k : commonInput) {
                common.put(k, input.common(k));
            }
            snapshot.put("common", common);
        }

        if (!itemInput.isEmpty() && input.itemCount() > 0) {
            List<Map<String, Object>> items = new ArrayList<>(input.itemCount());
            for (int i = 0; i < input.itemCount(); i++) {
                Map<String, Object> row = new LinkedHashMap<>();
                for (String k : itemInput) {
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
