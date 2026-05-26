package page.liam.pine;

import java.util.*;
import java.util.concurrent.locks.ReadWriteLock;
import java.util.concurrent.locks.ReentrantReadWriteLock;

public class ColumnFrame implements Frame {
    private final ReadWriteLock rwLock = new ReentrantReadWriteLock();
    private Map<String, Object> common;
    private Map<String, Object[]> columns;
    private Map<String, BitSet> presenceByField = new LinkedHashMap<>();
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

            List<Map<String, Object>> its = new ArrayList<>(rowCount);
            for (int i = 0; i < rowCount; i++) {
                Map<String, Object> row = new LinkedHashMap<>();

                // Strict item fields: must be present and non-nil
                for (String field : spec.strictItem) {
                    Object[] col = columns.get(field);
                    BitSet bits = presenceByField.get(field);
                    if (col == null || bits == null || !bits.get(i) || col[i] == null) {
                        throw new PineErrors.OperatorException(
                                "operator \"" + opName + "\": required field \"" + field + "\" is nil on item[" + i + "]");
                    }
                    row.put(field, col[i]);
                }
                // Defaulted item fields: substitute default on nil/missing
                for (InputFieldSpec.DefaultedField df : spec.defaultedItem) {
                    Object[] col = columns.get(df.name);
                    BitSet bits = presenceByField.get(df.name);
                    if (col != null && bits != null && bits.get(i) && col[i] != null) {
                        row.put(df.name, col[i]);
                    } else {
                        row.put(df.name, df.defaultValue);
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
    public void applyOutput(OperatorOutput out, String opName, boolean recall) {
        rwLock.writeLock().lock();
        try {
            // 1. Common writes
            for (Map.Entry<String, Object> entry : out.getCommonWrites().entrySet()) {
                String v = checkValue(entry.getKey(), entry.getValue());
                if (v != null) {
                    throw new PineErrors.ExecutionError(opName, "common write: " + v);
                }
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
                    String v = checkValue(field, value);
                    if (v != null) {
                        throw new PineErrors.ExecutionError(opName, "item[" + idx + "] write: " + v);
                    }
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
                    // SetItemOrder errors use ExecutionError uniformly across
                    // the four runtimes (pine-cpp / pine-python use the same
                    // class; pine-go wraps via the engine layer). The earlier
                    // IllegalArgumentException / IndexOutOfBoundsException
                    // diverged with no upside — runtime error parity treats
                    // exception types as part of the contract. (P2-26)
                    throw new PineErrors.ExecutionError(opName,
                        "SetItemOrder length " + order.size() + " does not match item count " + rowCount);
                }
                // Permutation check — without this, setItemOrder([0,0,0])
                // silently duplicates item 0 across the frame.
                boolean[] seen = new boolean[rowCount];
                int[] mapping = new int[rowCount];
                for (int i = 0; i < rowCount; i++) {
                    int origIdx = order.get(i);
                    if (origIdx < 0 || origIdx >= rowCount) {
                        throw new PineErrors.ExecutionError(opName,
                            "SetItemOrder index " + origIdx + " out of range [0, " + rowCount + ")");
                    }
                    if (seen[origIdx]) {
                        throw new PineErrors.ExecutionError(opName,
                            "SetItemOrder duplicate index " + origIdx + " (order must be a permutation)");
                    }
                    seen[origIdx] = true;
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
                        String v = checkValue(fe.getKey(), fe.getValue());
                        if (v != null) {
                            throw new PineErrors.ExecutionError(opName, "added item write: " + v);
                        }
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

    private static String checkValue(String field, Object v) {
        if (v == null) return null;
        if (v instanceof String) return null;
        if (v instanceof Number) {
            if (v instanceof Double) {
                double d = (Double) v;
                if (Double.isNaN(d) || Double.isInfinite(d)) {
                    return "field \"" + field + "\": NaN/Inf is not a valid JSON value";
                }
            } else if (v instanceof Float) {
                float f = (Float) v;
                if (Float.isNaN(f) || Float.isInfinite(f)) {
                    return "field \"" + field + "\": NaN/Inf is not a valid JSON value";
                }
            }
            return null;
        }
        if (v instanceof Boolean) return null;
        if (v instanceof Map) return null;
        if (v instanceof List) return null;
        return "field \"" + field + "\": unsupported value type: " + v.getClass().getName();
    }
}
