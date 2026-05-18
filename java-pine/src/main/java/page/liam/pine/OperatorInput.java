package page.liam.pine;

import java.util.Collections;
import java.util.List;
import java.util.Map;

public class OperatorInput {
    private final Map<String, Object> common;
    private final List<Map<String, Object>> items;

    public OperatorInput(Map<String, Object> common, List<Map<String, Object>> items) {
        this.common = common != null ? common : Collections.emptyMap();
        this.items = items != null ? items : Collections.emptyList();
    }

    public Object common(String field) {
        return common.get(field);
    }

    public int itemCount() {
        return items.size();
    }

    public Object item(int index, String field) {
        if (index < 0 || index >= items.size()) {
            return null;
        }
        return items.get(index).get(field);
    }

    public Map<String, Object> rawCommon() {
        return common;
    }

    public List<Map<String, Object>> rawItems() {
        return items;
    }
}
