package page.liam.pine;

import java.util.*;
import java.util.concurrent.locks.ReadWriteLock;
import java.util.concurrent.locks.ReentrantReadWriteLock;

public class DataFrame implements Frame {
    private final ReadWriteLock rwLock = new ReentrantReadWriteLock();
    private Map<String, Object> common;
    private List<Map<String, Object>> items;

    public DataFrame(Map<String, Object> common, List<Map<String, Object>> items) {
        this.common = new LinkedHashMap<>(common != null ? common : Collections.emptyMap());
        this.items = new ArrayList<>();
        if (items != null) {
            for (Map<String, Object> item : items) {
                this.items.add(new LinkedHashMap<>(item));
            }
        }
    }

    public Object common(String field) {
        rwLock.readLock().lock();
        try {
            return common.get(field);
        } finally {
            rwLock.readLock().unlock();
        }
    }

    public void setCommon(String field, Object value) {
        rwLock.writeLock().lock();
        try {
            common.put(field, value);
        } finally {
            rwLock.writeLock().unlock();
        }
    }

    public int itemCount() {
        rwLock.readLock().lock();
        try {
            return items.size();
        } finally {
            rwLock.readLock().unlock();
        }
    }

    public Object item(int index, String field) {
        rwLock.readLock().lock();
        try {
            if (index < 0 || index >= items.size()) return null;
            return items.get(index).get(field);
        } finally {
            rwLock.readLock().unlock();
        }
    }

    public OperatorInput buildInput(List<String> commonFields, List<String> itemFields,
                                    Map<String, Object> commonDefaults, Map<String, Object> itemDefaults) {
        rwLock.readLock().lock();
        try {
            Map<String, Object> cs = new LinkedHashMap<>();
            for (String field : commonFields) {
                if (common.containsKey(field)) {
                    Object v = common.get(field);
                    if (v == null && commonDefaults.containsKey(field)) {
                        v = commonDefaults.get(field);
                    }
                    cs.put(field, v);
                } else if (commonDefaults.containsKey(field)) {
                    cs.put(field, commonDefaults.get(field));
                }
            }

            List<Map<String, Object>> its = new ArrayList<>(items.size());
            for (Map<String, Object> item : items) {
                Map<String, Object> row = new LinkedHashMap<>();
                for (String field : itemFields) {
                    if (item.containsKey(field)) {
                        Object v = item.get(field);
                        if (v == null && itemDefaults.containsKey(field)) {
                            v = itemDefaults.get(field);
                        }
                        row.put(field, v);
                    } else if (itemDefaults.containsKey(field)) {
                        row.put(field, itemDefaults.get(field));
                    }
                }
                its.add(row);
            }

            return new OperatorInput(cs, its);
        } finally {
            rwLock.readLock().unlock();
        }
    }

    public void applyOutput(OperatorOutput out, String opName, boolean recall) throws Exception {
        rwLock.writeLock().lock();
        try {
            // 1. Common writes
            for (Map.Entry<String, Object> entry : out.getCommonWrites().entrySet()) {
                validateValue(entry.getValue());
                common.put(entry.getKey(), entry.getValue());
            }

            // 2. Item writes
            for (Map.Entry<Integer, Map<String, Object>> entry : out.getItemWrites().entrySet()) {
                int idx = entry.getKey();
                if (idx < 0 || idx >= items.size()) {
                    throw new RuntimeException("SetItem index " + idx + " out of range [0, " + items.size() + ")");
                }
                for (Object v : entry.getValue().values()) {
                    validateValue(v);
                }
                items.get(idx).putAll(entry.getValue());
            }

            // 3. Removals
            Set<Integer> removed = out.getRemovedItems();
            if (!removed.isEmpty()) {
                for (int idx : removed) {
                    if (idx < 0 || idx >= items.size()) {
                        throw new RuntimeException("RemoveItem index " + idx + " out of range [0, " + items.size() + ")");
                    }
                }
                List<Map<String, Object>> surviving = new ArrayList<>();
                for (int i = 0; i < items.size(); i++) {
                    if (!removed.contains(i)) {
                        surviving.add(items.get(i));
                    }
                }
                items = surviving;
            }

            // 4. Reorder
            List<Integer> order = out.getItemOrder();
            if (order != null) {
                if (order.size() != items.size()) {
                    throw new RuntimeException("SetItemOrder length " + order.size() + " does not match item count " + items.size());
                }
                List<Map<String, Object>> reordered = new ArrayList<>(order.size());
                for (int origIdx : order) {
                    if (origIdx < 0 || origIdx >= items.size()) {
                        throw new RuntimeException("SetItemOrder index " + origIdx + " out of range [0, " + items.size() + ")");
                    }
                    reordered.add(items.get(origIdx));
                }
                items = reordered;
            }

            // 5. Additions
            for (Map<String, Object> added : out.getAddedItems()) {
                Map<String, Object> row = new LinkedHashMap<>(added);
                for (Map.Entry<String, Object> entry : row.entrySet()) {
                    validateValue(entry.getValue());
                }
                if (recall) {
                    row.put("_source", opName);
                }
                items.add(row);
            }
        } finally {
            rwLock.writeLock().unlock();
        }
    }

    public Map<String, Object> toResultCommon(List<String> commonOut) {
        rwLock.readLock().lock();
        try {
            Map<String, Object> result = new LinkedHashMap<>();
            for (String k : commonOut) {
                if (common.containsKey(k)) {
                    result.put(k, common.get(k));
                }
            }
            return result;
        } finally {
            rwLock.readLock().unlock();
        }
    }

    public List<Map<String, Object>> toResultItems(List<String> itemOut) {
        rwLock.readLock().lock();
        try {
            List<Map<String, Object>> result = new ArrayList<>(items.size());
            for (Map<String, Object> item : items) {
                Map<String, Object> row = new LinkedHashMap<>();
                for (String k : itemOut) {
                    if (item.containsKey(k)) {
                        row.put(k, item.get(k));
                    }
                }
                result.add(row);
            }
            return result;
        } finally {
            rwLock.readLock().unlock();
        }
    }

    private static void validateValue(Object v) {
        if (v == null) return;
        if (v instanceof String) return;
        if (v instanceof Number) return;
        if (v instanceof Boolean) return;
        if (v instanceof Map) return;
        if (v instanceof List) return;
        throw new RuntimeException("unsupported value type: " + v.getClass().getName());
    }
}
