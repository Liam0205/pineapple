package page.liam.pine;

import java.util.ArrayDeque;
import java.util.List;
import java.util.Map;

/**
 * Write-time value check shared by {@link DataFrame} and {@link ColumnFrame}.
 * Mirrors pine-go {@code validateValue} (internal/dataframe/row_frame.go) and
 * pine-cpp {@code validate_frame_value}: the messages are part of the cross-runtime
 * error contract and must stay byte-identical.
 */
final class FrameValues {

    /**
     * Bound on the descent into nested composites: a composite at this depth
     * or deeper is not inspected (scalars are checked at any depth their
     * parent reaches). The rule depends only on the value, never on
     * iteration order. Same bound as pine-go {@code maxCompositeScanDepth}
     * and pine-cpp {@code kMaxCompositeScanDepth}.
     */
    static final int MAX_COMPOSITE_SCAN_DEPTH = 1000;

    private FrameValues() {
    }

    /** Returns the violation message (no {@code pine:}/op prefix), or null when the value is OK. */
    static String checkValue(String field, Object v) {
        if (v == null) return null;
        if (v instanceof String) return null;
        if (v instanceof Number) {
            if (isNonFinite((Number) v)) {
                return nonFiniteMessage(field);
            }
            return null;
        }
        if (v instanceof Boolean) return null;
        if (v instanceof Map || v instanceof List) {
            // A composite can still carry NaN/±Inf (e.g. a Lua table
            // {x * 2} whose element overflowed). Reject it with the scalar
            // message so every runtime fails at the same point with the
            // same bytes (issue #210). Only non-finite numbers are checked.
            if (containsNonFinite(v, 0, new ArrayDeque<>())) {
                return nonFiniteMessage(field);
            }
            return null;
        }
        return "field \"" + field + "\": unsupported value type: " + v.getClass().getName();
    }

    private static String nonFiniteMessage(String field) {
        return "field \"" + field + "\": NaN/Inf is not a valid JSON value";
    }

    private static boolean isNonFinite(Number n) {
        if (n instanceof Double) {
            double d = (Double) n;
            return Double.isNaN(d) || Double.isInfinite(d);
        }
        if (n instanceof Float) {
            float f = (Float) n;
            return Float.isNaN(f) || Float.isInfinite(f);
        }
        return false;
    }

    /**
     * A custom operator can hand over a self-referencing map or list; a
     * composite that is one of its own ancestors (by identity) is skipped,
     * which keeps a map with several self-keys linear instead of exponential.
     */
    private static boolean containsNonFinite(Object v, int depth, ArrayDeque<Object> ancestors) {
        if (v instanceof Number) {
            return isNonFinite((Number) v);
        }
        if (!(v instanceof Map) && !(v instanceof List)) {
            return false;
        }
        if (depth >= MAX_COMPOSITE_SCAN_DEPTH) {
            return false;
        }
        for (Object a : ancestors) {
            if (a == v) {
                return false;
            }
        }
        Iterable<?> children = v instanceof Map ? ((Map<?, ?>) v).values() : (List<?>) v;
        ancestors.push(v);
        try {
            for (Object e : children) {
                if (containsNonFinite(e, depth + 1, ancestors)) {
                    return true;
                }
            }
            return false;
        } finally {
            ancestors.pop();
        }
    }
}
