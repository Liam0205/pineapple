package page.liam.pine;

import java.util.*;
import java.util.concurrent.locks.ReadWriteLock;
import java.util.concurrent.locks.ReentrantReadWriteLock;

/**
 * Request-local column-store DataFrame backed by typed columns (see
 * Column.java — double/String/boolean flat storage + validity bitmap,
 * JsonColumn as the heterogeneous fallback), mirroring pine-cpp's Column
 * hierarchy and pine-go's typed ColumnFrame.
 */
public class ColumnFrame implements Frame {
    private final ReadWriteLock rwLock = new ReentrantReadWriteLock();
    private final Map<String, Object> common;
    private final Map<String, Column> columns;
    private int rowCount;

    public ColumnFrame(Map<String, Object> common, List<Map<String, Object>> items) {
        this.common = new LinkedHashMap<>(common != null ? common : Collections.emptyMap());
        List<Map<String, Object>> rows = items != null ? items : Collections.emptyList();
        this.rowCount = rows.size();
        this.columns = new LinkedHashMap<>();

        if (!rows.isEmpty()) {
            Set<String> allFields = new LinkedHashSet<>();
            for (Map<String, Object> item : rows) {
                allFields.addAll(item.keySet());
            }
            for (String field : allFields) {
                columns.put(field, Column.build(rows, field));
            }
        }
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
            Column col = columns.get(field);
            if (col == null) return null;
            return col.get(index);
        } finally {
            rwLock.readLock().unlock();
        }
    }

    /**
     * Batch read: the [offset, offset+count) window under a single lock
     * acquisition. JsonColumn windows may be zero-copy views of the live
     * backing array; typed columns box a copy. See Frame.itemColumnView
     * for the read-only/escape contract.
     */
    @Override
    public Object[] itemColumnView(String field, int offset, int count) {
        rwLock.readLock().lock();
        try {
            if (offset < 0 || count < 0 || offset + count > rowCount) {
                return null;
            }
            Column col = columns.get(field);
            if (col == null) {
                // Absent column: every slot reads as null, same as item().
                return new Object[count];
            }
            return col.view(offset, count);
        } finally {
            rwLock.readLock().unlock();
        }
    }

    /**
     * Typed batch read: raw double[] window when the field is stored as a
     * double column AND every slot in the window is present (no null
     * anywhere, so item-defaults can never fire and element i is exactly
     * what item(offset+i) would box). Null = caller falls back to
     * itemColumnView / item. Same read-only/escape contract.
     */
    @Override
    public double[] itemColumnDoubleView(String field, int offset, int count) {
        rwLock.readLock().lock();
        try {
            if (offset < 0 || count < 0 || offset + count > rowCount) {
                return null;
            }
            Column col = columns.get(field);
            if (!(col instanceof Column.DoubleColumn)) {
                return null;
            }
            return ((Column.DoubleColumn) col).rawWindow(offset, count);
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
            // Column resolution is hoisted out of the per-item loop;
            // iteration stays item-major so the first-error priority is
            // byte-identical across storage modes and runtimes.
            Column[] strictCols = new Column[spec.strictItem.size()];
            for (int k = 0; k < spec.strictItem.size(); k++) {
                strictCols[k] = columns.get(spec.strictItem.get(k));
            }
            Column[] nullableCols = new Column[spec.nullableItem.size()];
            for (int k = 0; k < spec.nullableItem.size(); k++) {
                nullableCols[k] = columns.get(spec.nullableItem.get(k));
            }
            for (int i = 0; i < rowCount; i++) {
                for (int k = 0; k < spec.strictItem.size(); k++) {
                    Column col = strictCols[k];
                    if (col == null || col.isNull(i)) {
                        throw new PineErrors.OperatorException(
                                "required field \"" + spec.strictItem.get(k) + "\" is nil on item[" + i + "]");
                    }
                }
                for (int k = 0; k < spec.nullableItem.size(); k++) {
                    Column col = nullableCols[k];
                    if (col == null || !col.present(i)) {
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
            Column lastCol = null;
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
                        Column col = columns.get(field);
                        if (col == null) {
                            col = Column.forValue(value, rowCount);
                            columns.put(field, col);
                        }
                        lastField = field;
                        lastCol = col;
                    }
                    if (!lastCol.set(idx, value)) {
                        // Type mismatch or present-null into a typed column:
                        // promote to JsonColumn and retry (cannot fail there).
                        Column promoted = lastCol.toJson();
                        columns.put(field, promoted);
                        lastCol = promoted;
                        lastCol.set(idx, value);
                    }
                }
            }

            // 2b. Whole-column typed writes: adopt vals as the column's
            // backing array (all slots present). Applied after per-element
            // writes so a column write to the same field wins. NaN/Inf
            // validation runs as a batch scan producing the same first
            // error the per-element path would.
            for (OperatorOutput.DoubleColumnWrite cw : out.getColumnWrites()) {
                if (cw.vals.length != rowCount) {
                    throw new PineErrors.ExecutionError(opName,
                        "SetItemColumnFloat64 \"" + cw.field + "\" length " + cw.vals.length
                        + " does not match item count " + rowCount);
                }
                for (int i = 0; i < cw.vals.length; i++) {
                    if (Double.isNaN(cw.vals[i]) || Double.isInfinite(cw.vals[i])) {
                        throw new PineErrors.ExecutionError(opName,
                            "item[" + i + "] write: field \"" + cw.field
                            + "\": NaN/Inf is not a valid JSON value");
                    }
                }
                columns.put(cw.field, Column.adoptDoubles(cw.vals));
            }

            // 3. Removals
            Set<Integer> removed = out.getRemovedItems();
            if (!removed.isEmpty()) {
                for (int idx : removed) {
                    if (idx < 0 || idx >= rowCount) {
                        throw new IndexOutOfBoundsException("RemoveItem index " + idx + " out of range [0, " + rowCount + ")");
                    }
                }
                boolean[] drop = new boolean[rowCount];
                for (int idx : removed) {
                    drop[idx] = true;
                }
                int kept = rowCount - removed.size();
                for (Column col : columns.values()) {
                    col.removeByBitmap(drop, kept);
                }
                rowCount = kept;
            }

            // 4. Reorder
            List<Integer> order = out.getItemOrder();
            if (order != null) {
                if (order.size() != rowCount) {
                    // SetItemOrder errors use ExecutionError uniformly across
                    // runtimes — exception types are part of the parity contract.
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
                // In-place cycle-following permutation; the visited scratch
                // is allocated once and shared across all columns.
                boolean[] visited = new boolean[rowCount];
                for (Column col : columns.values()) {
                    col.reorder(mapping, visited);
                }
            }

            // 5. Additions (column-major batch append)
            List<Map<String, Object>> added = out.getAddedItems();
            if (!added.isEmpty()) {
                int newCap = rowCount + added.size();

                // Pass 1: validate + inject _source + ensure columns exist.
                List<Map<String, Object>> rows = new ArrayList<>(added.size());
                for (Map<String, Object> row : added) {
                    if (recall) {
                        row = new LinkedHashMap<>(row);
                        row.put("_source", opName);
                    }
                    for (Map.Entry<String, Object> fe : row.entrySet()) {
                        String v = checkValue(fe.getKey(), fe.getValue());
                        if (v != null) {
                            throw new PineErrors.ExecutionError(opName, "added item write: " + v);
                        }
                        if (!columns.containsKey(fe.getKey())) {
                            Column col = Column.forValue(fe.getValue(), rowCount);
                            col.grow(newCap);
                            columns.put(fe.getKey(), col);
                        }
                    }
                    rows.add(row);
                }

                // Pass 2: column-major batch append — iterate the columns
                // map once instead of once per added item.
                for (Map.Entry<String, Column> entry : columns.entrySet()) {
                    String field = entry.getKey();
                    Column col = entry.getValue();
                    col.grow(newCap);
                    for (Map<String, Object> row : rows) {
                        if (!row.containsKey(field)) {
                            col.appendAbsent();
                            continue;
                        }
                        Object value = row.get(field);
                        if (!col.append(value)) {
                            Column promoted = col.toJson();
                            promoted.grow(newCap);
                            promoted.append(value);
                            entry.setValue(promoted);
                            col = promoted;
                        }
                    }
                }
                rowCount = newCap;
            }
        } finally {
            rwLock.writeLock().unlock();
        }
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
                result.add(new LinkedHashMap<>(itemOut.size(), 1.0f));
            }
            // Column-major projection: resolve each output column once,
            // then fill down the rows.
            for (String field : itemOut) {
                Column col = columns.get(field);
                if (col == null) {
                    continue;
                }
                for (int i = 0; i < rowCount; i++) {
                    if (col.present(i)) {
                        result.get(i).put(field, col.get(i));
                    }
                }
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
