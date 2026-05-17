package page.liam.pine;

import java.util.*;
import java.util.concurrent.locks.ReadWriteLock;
import java.util.concurrent.locks.ReentrantReadWriteLock;

public class ColumnFrame implements Frame {
    private final ReadWriteLock rwLock = new ReentrantReadWriteLock();
    private Map<String, Object> common;
    private Map<String, Object[]> columns;
    private int rowCount;

    public ColumnFrame(Map<String, Object> common, List<Map<String, Object>> items) {
        this.common = new LinkedHashMap<>(common != null ? common : Collections.emptyMap());
        this.rowCount = items != null ? items.size() : 0;
        this.columns = new LinkedHashMap<>();

        if (items != null && !items.isEmpty()) {
            Set<String> allFields = new LinkedHashSet<>();
            for (Map<String, Object> item : items) {
                allFields.addAll(item.keySet());
            }
            Map<String, BitSet> presenceMap = new LinkedHashMap<>();

            for (String field : allFields) {
                Object[] col = new Object[rowCount];
                BitSet bits = new BitSet(rowCount);
                for (int i = 0; i < rowCount; i++) {
                    Map<String, Object> row = items.get(i);
                    if (row.containsKey(field)) {
                        col[i] = row.get(field);
                        bits.set(i);
                    }
                }
                columns.put(field, col);
                presenceMap.put(field, bits);
            }
            rebuildPresenceArray(presenceMap);
        }
    }

    private Map<String, BitSet> presenceByField = new LinkedHashMap<>();

    private void rebuildPresenceArray(Map<String, BitSet> pm) {
        this.presenceByField = pm;
    }

    @Override
    public Object common(String field) {
        rwLock.readLock().lock();
        try {
            return common.get(field);
        } finally {
            rwLock.readLock().unlock();
        }
    }

    @Override
    public int itemCount() {
        rwLock.readLock().lock();
        try {
            return rowCount;
        } finally {
            rwLock.readLock().unlock();
        }
    }

    @Override
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

            List<Map<String, Object>> its = new ArrayList<>(rowCount);
            for (int i = 0; i < rowCount; i++) {
                Map<String, Object> row = new LinkedHashMap<>();
                for (String field : itemFields) {
                    BitSet bits = presenceByField.get(field);
                    Object[] col = columns.get(field);
                    if (col != null && bits != null && bits.get(i)) {
                        Object v = col[i];
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

    @Override
    public void applyOutput(OperatorOutput out, String opName, boolean recall) throws Exception {
        rwLock.writeLock().lock();
        try {
            // 1. Common writes
            for (Map.Entry<String, Object> entry : out.getCommonWrites().entrySet()) {
                validateValue(entry.getValue());
                common.put(entry.getKey(), entry.getValue());
            }

            // 2. Item field writes
            for (Map.Entry<Integer, Map<String, Object>> entry : out.getItemWrites().entrySet()) {
                int idx = entry.getKey();
                if (idx < 0 || idx >= rowCount) {
                    throw new IndexOutOfBoundsException("SetItem index " + idx + " out of range [0, " + rowCount + ")");
                }
                for (Map.Entry<String, Object> fe : entry.getValue().entrySet()) {
                    String field = fe.getKey();
                    Object value = fe.getValue();
                    validateValue(value);
                    Object[] col = columns.computeIfAbsent(field, k -> new Object[rowCount]);
                    if (col.length < rowCount) {
                        col = Arrays.copyOf(col, rowCount);
                        columns.put(field, col);
                    }
                    col[idx] = value;
                    presenceByField.computeIfAbsent(field, k -> new BitSet(rowCount)).set(idx);
                }
            }

            // 3. Removals
            Set<Integer> removed = out.getRemovedItems();
            if (!removed.isEmpty()) {
                for (int idx : removed) {
                    if (idx < 0 || idx >= rowCount) {
                        throw new IndexOutOfBoundsException("RemoveItem index " + idx + " out of range [0, " + rowCount + ")");
                    }
                }
                int newCount = rowCount - removed.size();
                int[] mapping = new int[newCount];
                int j = 0;
                for (int i = 0; i < rowCount; i++) {
                    if (!removed.contains(i)) {
                        mapping[j++] = i;
                    }
                }
                compactColumns(mapping, newCount);
            }

            // 4. Reorder
            List<Integer> order = out.getItemOrder();
            if (order != null) {
                if (order.size() != rowCount) {
                    throw new IllegalArgumentException("SetItemOrder length " + order.size() + " does not match item count " + rowCount);
                }
                int[] mapping = new int[rowCount];
                for (int i = 0; i < rowCount; i++) {
                    int origIdx = order.get(i);
                    if (origIdx < 0 || origIdx >= rowCount) {
                        throw new IndexOutOfBoundsException("SetItemOrder index " + origIdx + " out of range [0, " + rowCount + ")");
                    }
                    mapping[i] = origIdx;
                }
                compactColumns(mapping, rowCount);
            }

            // 5. Additions
            List<Map<String, Object>> added = out.getAddedItems();
            if (!added.isEmpty()) {
                int oldCount = rowCount;
                rowCount = oldCount + added.size();
                for (Map.Entry<String, Object[]> entry : columns.entrySet()) {
                    if (entry.getValue().length < rowCount) {
                        columns.put(entry.getKey(), Arrays.copyOf(entry.getValue(), rowCount));
                    }
                }
                for (int i = 0; i < added.size(); i++) {
                    Map<String, Object> row = added.get(i);
                    if (recall) {
                        row = new LinkedHashMap<>(row);
                        row.put("_source", opName);
                    }
                    for (Map.Entry<String, Object> fe : row.entrySet()) {
                        validateValue(fe.getValue());
                        String field = fe.getKey();
                        Object[] col = columns.computeIfAbsent(field, k -> new Object[rowCount]);
                        if (col.length < rowCount) {
                            col = Arrays.copyOf(col, rowCount);
                            columns.put(field, col);
                        }
                        col[oldCount + i] = fe.getValue();
                        presenceByField.computeIfAbsent(field, k -> new BitSet(rowCount)).set(oldCount + i);
                    }
                }
            }
        } finally {
            rwLock.writeLock().unlock();
        }
    }

    private void compactColumns(int[] mapping, int newCount) {
        for (Map.Entry<String, Object[]> entry : columns.entrySet()) {
            Object[] oldCol = entry.getValue();
            Object[] newCol = new Object[newCount];
            for (int i = 0; i < newCount; i++) {
                newCol[i] = mapping[i] < oldCol.length ? oldCol[mapping[i]] : null;
            }
            entry.setValue(newCol);
        }
        for (Map.Entry<String, BitSet> entry : presenceByField.entrySet()) {
            BitSet oldBits = entry.getValue();
            BitSet newBits = new BitSet(newCount);
            for (int i = 0; i < newCount; i++) {
                if (oldBits.get(mapping[i])) {
                    newBits.set(i);
                }
            }
            entry.setValue(newBits);
        }
        rowCount = newCount;
    }

    @Override
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

    @Override
    public List<Map<String, Object>> toResultItems(List<String> itemOut) {
        rwLock.readLock().lock();
        try {
            List<Map<String, Object>> result = new ArrayList<>(rowCount);
            for (int i = 0; i < rowCount; i++) {
                Map<String, Object> row = new LinkedHashMap<>();
                for (String field : itemOut) {
                    BitSet bits = presenceByField.get(field);
                    Object[] col = columns.get(field);
                    if (col != null && bits != null && bits.get(i)) {
                        row.put(field, col[i]);
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
        throw new IllegalArgumentException("unsupported value type: " + v.getClass().getName());
    }
}
