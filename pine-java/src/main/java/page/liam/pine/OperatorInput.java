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

    // Resolved {{field}} interpolation map for this request (issue #74).
    // Engine attaches it after BuildInput / before execute; shards inherit
    // by reference via ParallelExecutor.
    private Map<String, Object> templated;

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

    /**
     * Returns all values of the given item field for this input's window as
     * an array indexed by item position. Element i is identical to
     * item(i, field), including item-default substitution for null slots.
     *
     * <p>The returned array is READ-ONLY and valid only for the duration of
     * the current execute call: when backed by a ColumnFrame without
     * defaults for this field it may be a zero-copy view of the frame's
     * column. Operators must not mutate it or retain it past execute.
     *
     * <p>Compared to an item() loop this collapses the per-element lock +
     * map lookup to once per column, which is where column storage's
     * contiguous layout actually pays off.
     */
    public Object[] itemColumn(String field) {
        // Materialized mode: gather from row maps.
        if (items != null) {
            Object[] col = new Object[items.size()];
            for (int i = 0; i < col.length; i++) {
                col[i] = items.get(i).get(field);
            }
            return col;
        }

        Object defaultVal = itemDefaults != null ? itemDefaults.get(field) : null;

        Object[] view = frame.itemColumnView(field, offset, count);
        if (view != null) {
            if (defaultVal == null) {
                return view;
            }
            // Defaults force a copy: null slots substitute the default
            // value, matching item()'s per-element semantics.
            Object[] col = new Object[view.length];
            for (int i = 0; i < view.length; i++) {
                col[i] = view[i] != null ? view[i] : defaultVal;
            }
            return col;
        }

        // Fallback: per-element gather through the Frame interface.
        Object[] col = new Object[count];
        for (int i = 0; i < count; i++) {
            Object v = frame.item(offset + i, field);
            col[i] = v != null ? v : defaultVal;
        }
        return col;
    }

    /**
     * Typed batch read: the field's whole window as a raw double[] when
     * the backing frame stores it as a typed double column AND every slot
     * is non-null (so item-default substitution can never apply). Null
     * means the caller must use itemColumn / item instead. Zero-copy
     * where possible: read-only, valid only for the current execute.
     *
     * <p>This is the fully-unboxed fast path: scan loops avoid both the
     * per-element boxing and the per-element instanceof checks that
     * itemColumn callers pay.
     */
    public double[] itemColumnDouble(String field) {
        if (items != null || frame == null) {
            return null;
        }
        return frame.itemColumnDoubleView(field, offset, count);
    }

    /**
     * Returns the resolved + coerced templated param value for {@code name}
     * (issue #74), or {@code null} if the param was not templated or no
     * templated params were resolved for this request. The map is shared
     * across data_parallel shards and must be treated read-only.
     */
    public Object templatedParam(String name) {
        if (templated == null) return null;
        return templated.get(name);
    }

    /**
     * Engine-internal: install the per-request resolved {{field}} map.
     * Invoked once by the scheduler after BuildInput and before execute
     * (or before splitting for data_parallel).
     */
    void setTemplatedParams(Map<String, Object> resolved) {
        this.templated = resolved;
    }

    /**
     * Engine-internal: returns the underlying map so ParallelExecutor can
     * propagate it to shards.
     */
    Map<String, Object> rawTemplated() {
        return templated;
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
