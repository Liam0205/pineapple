package page.liam.pine;

import java.util.*;

public class OperatorOutput {
    private final Map<String, Object> commonWrites = new LinkedHashMap<>();
    private final Map<Integer, Map<String, Object>> itemWrites = new HashMap<>();
    private final List<DoubleColumnWrite> columnWrites = new ArrayList<>();
    private final List<Map<String, Object>> addedItems = new ArrayList<>();
    private final Set<Integer> removedItems = new HashSet<>();
    private List<Integer> itemOrder;
    private Exception warning;

    /**
     * Whole-column typed write: vals becomes the field's full column
     * (all slots present) when applied. Ownership of the array transfers
     * to the frame at applyOutput time — the operator must not read or
     * mutate it afterwards (same zero-copy convention as added-item maps).
     */
    public static final class DoubleColumnWrite {
        public final String field;
        public final double[] vals;

        DoubleColumnWrite(String field, double[] vals) {
            this.field = field;
            this.vals = vals;
        }
    }

    public void setWarning(Exception w) {
        if (this.warning == null) {
            this.warning = w;
        }
    }

    public Exception getWarning() {
        return warning;
    }

    public void setCommon(String field, Object value) {
        commonWrites.put(field, value);
    }

    public void setItem(int index, String field, Object value) {
        itemWrites.computeIfAbsent(index, k -> new LinkedHashMap<>()).put(field, value);
    }

    /**
     * Writes a whole double column in one call: vals[i] becomes the
     * field's value on item i, all slots present. vals.length must equal
     * the frame's item count at apply time (whole column or nothing).
     * Column writes apply AFTER per-element item writes within the same
     * output (a column write to the same field overwrites every element).
     *
     * <p>Write-side counterpart of itemColumnDouble: no per-element
     * boxing, no per-element write records; the column-store frame adopts
     * the array as the column's backing storage directly.
     */
    public void setItemColumnDouble(String field, double[] vals) {
        columnWrites.add(new DoubleColumnWrite(field, vals));
    }

    public void addItem(Map<String, Object> fields) {
        addedItems.add(fields);
    }

    public void removeItem(int index) {
        removedItems.add(index);
    }

    public void setItemOrder(List<Integer> order) {
        this.itemOrder = order;
    }

    public Map<String, Object> getCommonWrites() {
        return commonWrites;
    }

    public Map<Integer, Map<String, Object>> getItemWrites() {
        return itemWrites;
    }

    public List<DoubleColumnWrite> getColumnWrites() {
        return columnWrites;
    }

    /**
     * Reconstructs the per-item write view folding in whole-column writes
     * (which apply after and therefore override per-element writes on the
     * same field). Mirrors pine-go's ItemWriteMap. Intended for tests and
     * debug snapshots only — the apply path reads the raw collections.
     */
    public Map<Integer, Map<String, Object>> itemWriteMap() {
        Map<Integer, Map<String, Object>> m = new HashMap<>();
        for (Map.Entry<Integer, Map<String, Object>> entry : itemWrites.entrySet()) {
            m.put(entry.getKey(), new LinkedHashMap<>(entry.getValue()));
        }
        for (DoubleColumnWrite cw : columnWrites) {
            for (int i = 0; i < cw.vals.length; i++) {
                m.computeIfAbsent(i, k -> new LinkedHashMap<>()).put(cw.field, cw.vals[i]);
            }
        }
        return m;
    }

    public List<Map<String, Object>> getAddedItems() {
        return addedItems;
    }

    public Set<Integer> getRemovedItems() {
        return removedItems;
    }

    public List<Integer> getItemOrder() {
        return itemOrder;
    }
}
