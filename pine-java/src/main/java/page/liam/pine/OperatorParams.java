package page.liam.pine;

import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.Set;

public class OperatorParams {
    private final Map<String, Object> raw;

    public OperatorParams(Map<String, Object> raw) {
        this.raw = raw != null ? raw : Collections.emptyMap();
    }

    public Object get(String key) {
        return raw.get(key);
    }

    @SuppressWarnings("unchecked")
    public <T> T get(String key, Class<T> type) {
        Object v = raw.get(key);
        if (v == null) return null;
        return (T) v;
    }

    public String getString(String key) {
        Object v = raw.get(key);
        return v != null ? v.toString() : null;
    }

    public String getString(String key, String defaultValue) {
        Object v = raw.get(key);
        return v != null ? v.toString() : defaultValue;
    }

    public int getInt(String key, int defaultValue) {
        Object v = raw.get(key);
        if (v instanceof Number) return ((Number) v).intValue();
        return defaultValue;
    }

    public boolean getBoolean(String key, boolean defaultValue) {
        Object v = raw.get(key);
        if (v instanceof Boolean) return (Boolean) v;
        return defaultValue;
    }

    @SuppressWarnings("unchecked")
    public List<String> getStringList(String key) {
        Object v = raw.get(key);
        if (v instanceof List) return (List<String>) v;
        return Collections.emptyList();
    }

    public boolean containsKey(String key) {
        return raw.containsKey(key);
    }

    public Set<String> keys() {
        return raw.keySet();
    }

    public Map<String, Object> toMap() {
        return Collections.unmodifiableMap(raw);
    }
}
