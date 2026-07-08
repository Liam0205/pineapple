package page.liam.pine;

import java.util.BitSet;
import java.util.List;
import java.util.Map;

/**
 * Typed column storage for ColumnFrame, mirroring pine-cpp's Column
 * hierarchy (pine-cpp/include/pine/column.hpp) and pine-go's column.go:
 * fixed-width typed columns with a validity bitmap, plus a heterogeneous
 * JSON fallback.
 *
 * <p>A position can be ABSENT (validity bit clear — the row never wrote
 * the field). The JSON column additionally allows PRESENT-NULL (validity
 * set, value null) — typed columns cannot represent that state and must
 * be promoted to the JSON column first.
 *
 * <p>Typed dispatch is by exact runtime class (Double / String / Boolean),
 * matching pine-go's exact-type dispatch: JSON-sourced data always boxes
 * numbers as Double in this runtime, so the common case gets the typed
 * path, while other Number subtypes fall to the JSON column (downstream
 * contracts observe the concrete boxed type).
 */
abstract class Column {

    /** Number of rows in this column. */
    abstract int size();

    /** User-facing "value at i is nil": absent, or present-null (JSON only). */
    abstract boolean isNull(int i);

    /** Raw presence bit: the row explicitly wrote this field. */
    abstract boolean present(int i);

    /** Boxed value at i, or null when isNull(i). */
    abstract Object get(int i);

    /**
     * Writes v at i marking it present. Returns false when v cannot be
     * stored (type mismatch, or null into a typed column) — the caller
     * promotes to a JSON column and retries.
     */
    abstract boolean set(int i, Object v);

    /** Appends v as a present slot; false → promote first. */
    abstract boolean append(Object v);

    /** Appends one absent slot. */
    abstract void appendAbsent();

    /** Ensures backing capacity for newCap rows (size unchanged). */
    abstract void grow(int newCap);

    /**
     * Compacts in place: drop rows where drop[i] is true; kept is the
     * resulting size (computed once by the caller, shared across columns).
     */
    abstract void removeByBitmap(boolean[] drop, int kept);

    /**
     * Applies a validated permutation in place via cycle following.
     * visited is caller-owned scratch shared across columns; reset here.
     */
    abstract void reorder(int[] order, boolean[] visited);

    /**
     * The [offset, offset+count) window as boxed values with null in null
     * slots — element i must equal get(offset + i). The JSON column may
     * return its live backing array zero-copy; typed columns box a copy.
     */
    abstract Object[] view(int offset, int count);

    /** Materializes an equivalent JSON column (promotion path). */
    abstract JsonColumn toJson();

    // --- construction ---

    /**
     * Scans the field's values across all items and returns the
     * best-fitting column (construction-time type inference, mirrors
     * pine-cpp make_column). Present-null disqualifies typed storage.
     */
    static Column build(List<Map<String, Object>> items, String field) {
        Class<?> kind = null;
        boolean json = false;
        for (Map<String, Object> item : items) {
            if (!item.containsKey(field)) {
                continue;
            }
            Object v = item.get(field);
            Class<?> k;
            if (v instanceof Double) {
                k = Double.class;
            } else if (v instanceof String) {
                k = String.class;
            } else if (v instanceof Boolean) {
                k = Boolean.class;
            } else {
                json = true;
                break;
            }
            if (kind == null) {
                kind = k;
            } else if (kind != k) {
                json = true;
                break;
            }
        }

        int n = items.size();
        Column col;
        if (json || kind == null) {
            col = new JsonColumn(0);
        } else if (kind == Double.class) {
            col = new DoubleColumn(0);
        } else if (kind == String.class) {
            col = new StringColumn(0);
        } else {
            col = new BoolColumn(0);
        }
        col.grow(n);
        for (Map<String, Object> item : items) {
            if (item.containsKey(field)) {
                col.append(item.get(field)); // cannot fail: probed above
            } else {
                col.appendAbsent();
            }
        }
        return col;
    }

    /**
     * Picks a fresh column for a first-seen field written at runtime,
     * sized to n absent rows. Exact-type dispatch.
     */
    static Column forValue(Object v, int n) {
        if (v instanceof Double) {
            return new DoubleColumn(n);
        }
        if (v instanceof String) {
            return new StringColumn(n);
        }
        if (v instanceof Boolean) {
            return new BoolColumn(n);
        }
        return new JsonColumn(n);
    }

    /**
     * Wraps a caller-supplied double[] as a fully-present typed column
     * without copying (write-side zero-copy counterpart of rawWindow).
     * Ownership of vals transfers to the column.
     */
    static DoubleColumn adoptDoubles(double[] vals) {
        DoubleColumn col = new DoubleColumn(0);
        col.adopt(vals);
        return col;
    }

    // --- shared permutation helper ---

    /**
     * Cycle-following in-place permutation over positions [0, order.length):
     * ≤ n moves, zero allocation. move(dst, src) copies slot src to dst;
     * save(i)/restore(j) stash and place the cycle head.
     */
    interface Slots {
        void save(int i);

        void move(int dst, int src);

        void restore(int dst);
    }

    static void cycleReorder(int[] order, boolean[] visited, Slots slots) {
        int n = order.length;
        java.util.Arrays.fill(visited, 0, n, false);
        for (int i = 0; i < n; i++) {
            if (visited[i]) {
                continue;
            }
            if (order[i] == i) {
                visited[i] = true;
                continue;
            }
            slots.save(i);
            int j = i;
            while (true) {
                int src = order[j];
                if (src == i) {
                    slots.restore(j);
                    visited[j] = true;
                    break;
                }
                slots.move(j, src);
                visited[j] = true;
                j = src;
            }
        }
    }

    // --- JSON fallback ---

    /**
     * Heterogeneous fallback storing boxed values. Invariant: data[i] is
     * null whenever the validity bit is clear, so view() can return the
     * live backing array zero-copy with item()-identical null semantics.
     */
    static final class JsonColumn extends Column {
        private Object[] data;
        private final BitSet validity;
        private int size;

        JsonColumn(int n) {
            data = new Object[n];
            validity = new BitSet(n);
            size = n;
        }

        @Override
        int size() {
            return size;
        }

        @Override
        boolean isNull(int i) {
            return !validity.get(i) || data[i] == null;
        }

        @Override
        boolean present(int i) {
            return validity.get(i);
        }

        @Override
        Object get(int i) {
            return data[i];
        }

        @Override
        boolean set(int i, Object v) {
            data[i] = v;
            validity.set(i);
            return true;
        }

        @Override
        boolean append(Object v) {
            grow(size + 1);
            data[size] = v;
            validity.set(size);
            size++;
            return true;
        }

        @Override
        void appendAbsent() {
            grow(size + 1);
            data[size] = null;
            validity.clear(size);
            size++;
        }

        @Override
        void grow(int newCap) {
            if (data.length < newCap) {
                data = java.util.Arrays.copyOf(data, Math.max(newCap, data.length * 2));
            }
        }

        @Override
        void removeByBitmap(boolean[] drop, int kept) {
            int write = 0;
            for (int i = 0; i < size; i++) {
                if (!drop[i]) {
                    data[write] = data[i];
                    validity.set(write, validity.get(i));
                    write++;
                }
            }
            for (int i = kept; i < size; i++) {
                data[i] = null; // unpin dropped referents
                validity.clear(i);
            }
            size = kept;
        }

        @Override
        void reorder(int[] order, boolean[] visited) {
            cycleReorder(order, visited, new Slots() {
                private Object tmpVal;
                private boolean tmpPres;

                @Override
                public void save(int i) {
                    tmpVal = data[i];
                    tmpPres = validity.get(i);
                }

                @Override
                public void move(int dst, int src) {
                    data[dst] = data[src];
                    validity.set(dst, validity.get(src));
                }

                @Override
                public void restore(int dst) {
                    data[dst] = tmpVal;
                    validity.set(dst, tmpPres);
                }
            });
        }

        @Override
        Object[] view(int offset, int count) {
            if (offset == 0 && count == size && data.length == size) {
                return data;
            }
            return java.util.Arrays.copyOfRange(data, offset, offset + count);
        }

        @Override
        JsonColumn toJson() {
            return this;
        }
    }

    // --- typed columns ---

    /** Fixed-width double storage + validity bitmap. */
    static final class DoubleColumn extends Column {
        private double[] data;
        private final BitSet validity;
        private int size;

        DoubleColumn(int n) {
            data = new double[n];
            validity = new BitSet(n);
            size = n;
        }

        @Override
        int size() {
            return size;
        }

        @Override
        boolean isNull(int i) {
            return !validity.get(i);
        }

        @Override
        boolean present(int i) {
            return validity.get(i);
        }

        @Override
        Object get(int i) {
            return validity.get(i) ? data[i] : null;
        }

        @Override
        boolean set(int i, Object v) {
            if (!(v instanceof Double)) {
                return false;
            }
            data[i] = (Double) v;
            validity.set(i);
            return true;
        }

        @Override
        boolean append(Object v) {
            if (!(v instanceof Double)) {
                return false;
            }
            grow(size + 1);
            data[size] = (Double) v;
            validity.set(size);
            size++;
            return true;
        }

        @Override
        void appendAbsent() {
            grow(size + 1);
            data[size] = 0.0;
            validity.clear(size);
            size++;
        }

        @Override
        void grow(int newCap) {
            if (data.length < newCap) {
                data = java.util.Arrays.copyOf(data, Math.max(newCap, data.length * 2));
            }
        }

        @Override
        void removeByBitmap(boolean[] drop, int kept) {
            int write = 0;
            for (int i = 0; i < size; i++) {
                if (!drop[i]) {
                    data[write] = data[i];
                    validity.set(write, validity.get(i));
                    write++;
                }
            }
            for (int i = kept; i < size; i++) {
                validity.clear(i);
            }
            size = kept;
        }

        @Override
        void reorder(int[] order, boolean[] visited) {
            cycleReorder(order, visited, new Slots() {
                private double tmpVal;
                private boolean tmpPres;

                @Override
                public void save(int i) {
                    tmpVal = data[i];
                    tmpPres = validity.get(i);
                }

                @Override
                public void move(int dst, int src) {
                    data[dst] = data[src];
                    validity.set(dst, validity.get(src));
                }

                @Override
                public void restore(int dst) {
                    data[dst] = tmpVal;
                    validity.set(dst, tmpPres);
                }
            });
        }

        @Override
        Object[] view(int offset, int count) {
            Object[] out = new Object[count];
            for (int i = 0; i < count; i++) {
                if (validity.get(offset + i)) {
                    out[i] = data[offset + i];
                }
            }
            return out;
        }

        @Override
        JsonColumn toJson() {
            JsonColumn j = new JsonColumn(size);
            for (int i = 0; i < size; i++) {
                if (validity.get(i)) {
                    j.set(i, data[i]);
                }
            }
            return j;
        }

        /**
         * Zero-copy raw window, non-null only when every slot in the window
         * is present (so element i is exactly what item(offset+i) would box).
         */
        double[] rawWindow(int offset, int count) {
            for (int i = offset; i < offset + count; i++) {
                if (!validity.get(i)) {
                    return null;
                }
            }
            if (offset == 0 && count == size && data.length == size) {
                return data;
            }
            return java.util.Arrays.copyOfRange(data, offset, offset + count);
        }

        /** Takes ownership of vals as the full column, all slots present. */
        void adopt(double[] vals) {
            data = vals;
            size = vals.length;
            validity.set(0, size);
        }
    }

    /** Fixed-width String storage + validity bitmap. */
    static final class StringColumn extends Column {
        private String[] data;
        private final BitSet validity;
        private int size;

        StringColumn(int n) {
            data = new String[n];
            validity = new BitSet(n);
            size = n;
        }

        @Override
        int size() {
            return size;
        }

        @Override
        boolean isNull(int i) {
            return !validity.get(i);
        }

        @Override
        boolean present(int i) {
            return validity.get(i);
        }

        @Override
        Object get(int i) {
            return validity.get(i) ? data[i] : null;
        }

        @Override
        boolean set(int i, Object v) {
            if (!(v instanceof String)) {
                return false;
            }
            data[i] = (String) v;
            validity.set(i);
            return true;
        }

        @Override
        boolean append(Object v) {
            if (!(v instanceof String)) {
                return false;
            }
            grow(size + 1);
            data[size] = (String) v;
            validity.set(size);
            size++;
            return true;
        }

        @Override
        void appendAbsent() {
            grow(size + 1);
            data[size] = null;
            validity.clear(size);
            size++;
        }

        @Override
        void grow(int newCap) {
            if (data.length < newCap) {
                data = java.util.Arrays.copyOf(data, Math.max(newCap, data.length * 2));
            }
        }

        @Override
        void removeByBitmap(boolean[] drop, int kept) {
            int write = 0;
            for (int i = 0; i < size; i++) {
                if (!drop[i]) {
                    data[write] = data[i];
                    validity.set(write, validity.get(i));
                    write++;
                }
            }
            for (int i = kept; i < size; i++) {
                data[i] = null;
                validity.clear(i);
            }
            size = kept;
        }

        @Override
        void reorder(int[] order, boolean[] visited) {
            cycleReorder(order, visited, new Slots() {
                private String tmpVal;
                private boolean tmpPres;

                @Override
                public void save(int i) {
                    tmpVal = data[i];
                    tmpPres = validity.get(i);
                }

                @Override
                public void move(int dst, int src) {
                    data[dst] = data[src];
                    validity.set(dst, validity.get(src));
                }

                @Override
                public void restore(int dst) {
                    data[dst] = tmpVal;
                    validity.set(dst, tmpPres);
                }
            });
        }

        @Override
        Object[] view(int offset, int count) {
            Object[] out = new Object[count];
            for (int i = 0; i < count; i++) {
                if (validity.get(offset + i)) {
                    out[i] = data[offset + i];
                }
            }
            return out;
        }

        @Override
        JsonColumn toJson() {
            JsonColumn j = new JsonColumn(size);
            for (int i = 0; i < size; i++) {
                if (validity.get(i)) {
                    j.set(i, data[i]);
                }
            }
            return j;
        }
    }

    /** Fixed-width boolean storage + validity bitmap. */
    static final class BoolColumn extends Column {
        private final BitSet data;
        private final BitSet validity;
        private int size;

        BoolColumn(int n) {
            data = new BitSet(n);
            validity = new BitSet(n);
            size = n;
        }

        @Override
        int size() {
            return size;
        }

        @Override
        boolean isNull(int i) {
            return !validity.get(i);
        }

        @Override
        boolean present(int i) {
            return validity.get(i);
        }

        @Override
        Object get(int i) {
            return validity.get(i) ? data.get(i) : null;
        }

        @Override
        boolean set(int i, Object v) {
            if (!(v instanceof Boolean)) {
                return false;
            }
            data.set(i, (Boolean) v);
            validity.set(i);
            return true;
        }

        @Override
        boolean append(Object v) {
            if (!(v instanceof Boolean)) {
                return false;
            }
            data.set(size, (Boolean) v);
            validity.set(size);
            size++;
            return true;
        }

        @Override
        void appendAbsent() {
            data.clear(size);
            validity.clear(size);
            size++;
        }

        @Override
        void grow(int newCap) {
            // BitSet grows automatically.
        }

        @Override
        void removeByBitmap(boolean[] drop, int kept) {
            int write = 0;
            for (int i = 0; i < size; i++) {
                if (!drop[i]) {
                    data.set(write, data.get(i));
                    validity.set(write, validity.get(i));
                    write++;
                }
            }
            for (int i = kept; i < size; i++) {
                validity.clear(i);
            }
            size = kept;
        }

        @Override
        void reorder(int[] order, boolean[] visited) {
            cycleReorder(order, visited, new Slots() {
                private boolean tmpVal;
                private boolean tmpPres;

                @Override
                public void save(int i) {
                    tmpVal = data.get(i);
                    tmpPres = validity.get(i);
                }

                @Override
                public void move(int dst, int src) {
                    data.set(dst, data.get(src));
                    validity.set(dst, validity.get(src));
                }

                @Override
                public void restore(int dst) {
                    data.set(dst, tmpVal);
                    validity.set(dst, tmpPres);
                }
            });
        }

        @Override
        Object[] view(int offset, int count) {
            Object[] out = new Object[count];
            for (int i = 0; i < count; i++) {
                if (validity.get(offset + i)) {
                    out[i] = data.get(offset + i);
                }
            }
            return out;
        }

        @Override
        JsonColumn toJson() {
            JsonColumn j = new JsonColumn(size);
            for (int i = 0; i < size; i++) {
                if (validity.get(i)) {
                    j.set(i, data.get(i));
                }
            }
            return j;
        }
    }
}
