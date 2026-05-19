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

    public OperatorInput buildInput(String opName, InputFieldSpec spec) throws PineErrors.OperatorException {
        rwLock.readLock().lock();
        try {
            Map<String, Object> cs = new LinkedHashMap<>();

            // Strict common fields: must exist and be non-nil
            for (String field : spec.strictCommon) {
                Object v = common.get(field);
                if (!common.containsKey(field) || v == null) {
                    throw new PineErrors.OperatorException(
                            "operator \"" + opName + "\": required field \"" + field + "\" is nil in common");
                }
                cs.put(field, v);
            }
            // Defaulted common fields: substitute default on nil/missing
            for (InputFieldSpec.DefaultedField df : spec.defaultedCommon) {
                Object v = common.get(df.name);
                if (!common.containsKey(df.name) || v == null) {
                    cs.put(df.name, df.defaultValue);
                } else {
                    cs.put(df.name, v);
                }
            }

            List<Map<String, Object>> its = new ArrayList<>(items.size());
            for (int i = 0; i < items.size(); i++) {
                Map<String, Object> item = items.get(i);
                Map<String, Object> row = new LinkedHashMap<>();

                // Strict item fields: must exist and be non-nil
                for (String field : spec.strictItem) {
                    Object v = item.get(field);
                    if (!item.containsKey(field) || v == null) {
                        throw new PineErrors.OperatorException(
                                "operator \"" + opName + "\": required field \"" + field + "\" is nil on item[" + i + "]");
                    }
                    row.put(field, v);
                }
                // Defaulted item fields: substitute default on nil/missing
                for (InputFieldSpec.DefaultedField df : spec.defaultedItem) {
                    Object v = item.get(df.name);
                    if (!item.containsKey(df.name) || v == null) {
                        row.put(df.name, df.defaultValue);
                    } else {
                        row.put(df.name, v);
                    }
                }

                its.add(row);
            }

            return new OperatorInput(cs, its);
        } finally {
            rwLock.readLock().unlock();
        }
    }

    public void applyOutput(OperatorOutput out, String opName, boolean recall) {
        rwLock.writeLock().lock();
        try {
            // 1. Common writes
            for (Map.Entry<String, Object> entry : out.getCommonWrites().entrySet()) {
                validateValue(entry.getKey(), entry.getValue());
                common.put(entry.getKey(), entry.getValue());
            }

            // 2. Item writes
            for (Map.Entry<Integer, Map<String, Object>> entry : out.getItemWrites().entrySet()) {
                int idx = entry.getKey();
                if (idx < 0 || idx >= items.size()) {
                    throw new IndexOutOfBoundsException("SetItem index " + idx + " out of range [0, " + items.size() + ")");
                }
                for (Map.Entry<String, Object> fe : entry.getValue().entrySet()) {
                    validateValue(fe.getKey(), fe.getValue());
                }
                items.get(idx).putAll(entry.getValue());
            }

            // 3. Removals
            Set<Integer> removed = out.getRemovedItems();
            if (!removed.isEmpty()) {
                for (int idx : removed) {
                    if (idx < 0 || idx >= items.size()) {
                        throw new IndexOutOfBoundsException("RemoveItem index " + idx + " out of range [0, " + items.size() + ")");
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
                    throw new IllegalArgumentException("SetItemOrder length " + order.size() + " does not match item count " + items.size());
                }
                List<Map<String, Object>> reordered = new ArrayList<>(order.size());
                for (int origIdx : order) {
                    if (origIdx < 0 || origIdx >= items.size()) {
                        throw new IndexOutOfBoundsException("SetItemOrder index " + origIdx + " out of range [0, " + items.size() + ")");
                    }
                    reordered.add(items.get(origIdx));
                }
                items = reordered;
            }

            // 5. Additions
            for (Map<String, Object> added : out.getAddedItems()) {
                Map<String, Object> row = new LinkedHashMap<>(added);
                for (Map.Entry<String, Object> entry : row.entrySet()) {
                    validateValue(entry.getKey(), entry.getValue());
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

    private static void validateValue(String field, Object v) {
        if (v == null) return;
        if (v instanceof String) return;
        if (v instanceof Number) {
            if (v instanceof Double) {
                double d = (Double) v;
                if (Double.isNaN(d) || Double.isInfinite(d)) {
                    throw new IllegalArgumentException("field \"" + field + "\": NaN/Inf is not a valid JSON value");
                }
            } else if (v instanceof Float) {
                float f = (Float) v;
                if (Float.isNaN(f) || Float.isInfinite(f)) {
                    throw new IllegalArgumentException("field \"" + field + "\": NaN/Inf is not a valid JSON value");
                }
            }
            return;
        }
        if (v instanceof Boolean) return;
        if (v instanceof Map) return;
        if (v instanceof List) return;
        throw new IllegalArgumentException("field \"" + field + "\": unsupported value type: " + v.getClass().getName());
    }
}
