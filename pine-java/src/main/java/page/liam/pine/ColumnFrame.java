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
    public Object item(int index, String field) {
        rwLock.readLock().lock();
        try {
            if (index < 0 || index >= rowCount) return null;
            Object[] col = columns.get(field);
            if (col == null) return null;
            BitSet bits = presenceByField.get(field);
            if (bits == null || !bits.get(index)) return null;
            return col[index];
        } finally {
            rwLock.readLock().unlock();
        }
    }

    /**
     * Zero-copy batch read: the live column array is returned directly when
     * the window spans the whole frame (absent slots are null by
     * construction, matching item()'s null-on-absent semantics). See
     * Frame.itemColumnView for the read-only/escape contract.
     */
    @Override
    public Object[] itemColumnView(String field, int offset, int count) {
        rwLock.readLock().lock();
        try {
            if (offset < 0 || count < 0 || offset + count > rowCount) {
                return null;
            }
            Object[] col = columns.get(field);
            if (col == null) {
                // Absent column: every slot reads as null, same as item().
                return new Object[count];
            }
            if (offset == 0 && count == rowCount && col.length == rowCount) {
                return col;
            }
            return Arrays.copyOfRange(col, offset, offset + count);
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
                            "required field \"" + field + "\" is nil in common");
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
            // Nullable common fields: missing -> error, nil -> pass through
            for (String field : spec.nullableCommon) {
                if (!common.containsKey(field)) {
                    throw new PineErrors.OperatorException(
                            "required field \"" + field + "\" is missing in common");
                }
                cs.put(field, common.get(field));
            }

            // Validate strict item fields upfront (fail fast).
            // Column resolution is hoisted out of the per-item loop
            // (previously a map lookup per item x field). Iteration stays
            // item-major so the first-error priority is byte-identical
            // across storage modes and runtimes.
            Object[][] strictCols = new Object[spec.strictItem.size()][];
            BitSet[] strictBits = new BitSet[spec.strictItem.size()];
            for (int k = 0; k < spec.strictItem.size(); k++) {
                strictCols[k] = columns.get(spec.strictItem.get(k));
                strictBits[k] = presenceByField.get(spec.strictItem.get(k));
            }
            Object[][] nullableCols = new Object[spec.nullableItem.size()][];
            BitSet[] nullableBits = new BitSet[spec.nullableItem.size()];
            for (int k = 0; k < spec.nullableItem.size(); k++) {
                nullableCols[k] = columns.get(spec.nullableItem.get(k));
                nullableBits[k] = presenceByField.get(spec.nullableItem.get(k));
            }
            for (int i = 0; i < rowCount; i++) {
                for (int k = 0; k < spec.strictItem.size(); k++) {
                    Object[] col = strictCols[k];
                    BitSet bits = strictBits[k];
                    if (col == null || bits == null || !bits.get(i) || col[i] == null) {
                        throw new PineErrors.OperatorException(
                                "required field \"" + spec.strictItem.get(k) + "\" is nil on item[" + i + "]");
                    }
                }
                for (int k = 0; k < spec.nullableItem.size(); k++) {
                    Object[] col = nullableCols[k];
                    BitSet bits = nullableBits[k];
                    if (col == null || bits == null || !bits.get(i)) {
                        throw new PineErrors.OperatorException(
                                "required field \"" + spec.nullableItem.get(k) + "\" is missing on item[" + i + "]");
                    }
                }
            }

            // Build item defaults map for lazy access
            Map<String, Object> itemDefaults = null;
            if (!spec.defaultedItem.isEmpty()) {
                itemDefaults = new HashMap<>(spec.defaultedItem.size());
                for (InputFieldSpec.DefaultedField df : spec.defaultedItem) {
                    itemDefaults.put(df.name, df.defaultValue);
                }
            }

            return new OperatorInput(cs, this, itemDefaults, 0, rowCount);
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
            // Cache the last-resolved column: operators typically write the
            // same field set for consecutive indices, so this turns the
            // per-write map lookups into one pair per field run.
            String lastField = null;
            Object[] lastCol = null;
            BitSet lastBits = null;
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
                    if (!field.equals(lastField) || lastCol == null) {
                        Object[] col = columns.computeIfAbsent(field, k -> new Object[rowCount]);
                        if (col.length < rowCount) {
                            col = Arrays.copyOf(col, rowCount);
                            columns.put(field, col);
                        }
                        lastField = field;
                        lastCol = col;
                        lastBits = presenceByField.computeIfAbsent(field, k -> new BitSet(rowCount));
                    }
                    lastCol[idx] = value;
                    lastBits.set(idx);
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
                boolean[] bitmap = new boolean[rowCount];
                for (int idx : removed) {
                    bitmap[idx] = true;
                }
                int newCount = rowCount - removed.size();
                int[] mapping = new int[newCount];
                int j = 0;
                for (int i = 0; i < rowCount; i++) {
                    if (!bitmap[i]) {
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
                    // exception types as part of the contract.
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
                reorderColumnsInPlace(mapping);
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

    // In-place permutation via cycle following. `order` MUST be a valid
    // length-rowCount permutation of [0, rowCount) — the apply_output reorder
    // branch validates this before invoking. Each cycle is walked once,
    // performing ≤ N moves per column with no per-column allocation.
    // Replaces the prior compactColumns-based path which allocated a fresh
    // Object[] / BitSet per column. The cycle structure depends only on
    // `order`, so `visited` is allocated once and reset per column.
    private void reorderColumnsInPlace(int[] order) {
        int n = order.length;
        if (n == 0) {
            return;
        }
        boolean[] visited = new boolean[n];
        for (Map.Entry<String, Object[]> entry : columns.entrySet()) {
            Object[] col = entry.getValue();
            Arrays.fill(visited, false);
            for (int i = 0; i < n; i++) {
                if (visited[i]) {
                    continue;
                }
                if (order[i] == i) {
                    visited[i] = true;
                    continue;
                }
                Object tmp = col[i];
                int j = i;
                while (true) {
                    int src = order[j];
                    if (src == i) {
                        col[j] = tmp;
                        visited[j] = true;
                        break;
                    }
                    col[j] = col[src];
                    visited[j] = true;
                    j = src;
                }
            }
        }
        for (Map.Entry<String, BitSet> entry : presenceByField.entrySet()) {
            BitSet bits = entry.getValue();
            Arrays.fill(visited, false);
            for (int i = 0; i < n; i++) {
                if (visited[i]) {
                    continue;
                }
                if (order[i] == i) {
                    visited[i] = true;
                    continue;
                }
                boolean tmp = bits.get(i);
                int j = i;
                while (true) {
                    int src = order[j];
                    if (src == i) {
                        bits.set(j, tmp);
                        visited[j] = true;
                        break;
                    }
                    bits.set(j, bits.get(src));
                    visited[j] = true;
                    j = src;
                }
            }
        }
        // rowCount unchanged (permutation has length rowCount).
    }

    @Override
    public Map<String, Object> toResultCommon(List<String> commonOut) {
        rwLock.readLock().lock();
        try {
            Map<String, Object> result = new LinkedHashMap<>(commonOut.size(), 1.0f);
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
                Map<String, Object> row = new LinkedHashMap<>(itemOut.size(), 1.0f);
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
