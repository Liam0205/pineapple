package page.liam.pine;

import java.util.Collections;
import java.util.List;
import java.util.Map;

public class OperatorInput {
    private final Map<String, Object> common;

    // Lazy mode fields (frame != null)
    private final Frame frame;
    private final Map<String, Object> itemDefaults;
    private final int offset;
    private final int count;

    // Materialized mode field (items != null, frame == null)
    private final List<Map<String, Object>> items;

    public OperatorInput(Map<String, Object> common, List<Map<String, Object>> items) {
        this.common = common != null ? common : Collections.emptyMap();
        this.items = items != null ? items : Collections.emptyList();
        this.frame = null;
        this.itemDefaults = null;
        this.offset = 0;
        this.count = this.items.size();
    }

    OperatorInput(Map<String, Object> common, Frame frame, Map<String, Object> itemDefaults, int offset, int count) {
        this.common = common != null ? common : Collections.emptyMap();
        this.items = null;
        this.frame = frame;
        this.itemDefaults = itemDefaults;
        this.offset = offset;
        this.count = count;
    }

    public Object common(String field) {
        return common.get(field);
    }

    public int itemCount() {
        return count;
    }

    public Object item(int index, String field) {
        if (items != null) {
            if (index < 0 || index >= items.size()) {
                return null;
            }
            return items.get(index).get(field);
        }
        if (index < 0 || index >= count) {
            return null;
        }
        Object v = frame.item(offset + index, field);
        if (v == null && itemDefaults != null) {
            Object d = itemDefaults.get(field);
            if (d != null) return d;
        }
        return v;
    }

    public Map<String, Object> rawCommon() {
        return common;
    }

    public List<Map<String, Object>> rawItems() {
        return items != null ? items : Collections.emptyList();
    }

    boolean isLazy() {
        return frame != null;
    }

    Frame lazyFrame() {
        return frame;
    }

    Map<String, Object> lazyItemDefaults() {
        return itemDefaults;
    }

    int lazyOffset() {
        return offset;
    }
}
