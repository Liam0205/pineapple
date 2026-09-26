package page.liam.pine;

import java.util.IdentityHashMap;
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
            if (new Scanner().containsNonFinite(v, 0)) {
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
     * A custom operator can hand over a value whose maps / lists are shared
     * or form a cycle (a Lua- or JSON-built value is always a tree). Each
     * composite is remembered by identity with the shallowest depth it was
     * scanned at; reaching it again at the same or a greater depth cannot find
     * anything new, so it is skipped, while reaching it shallower rescans it
     * so the depth semantics equal a tree walk. No path enumeration happens:
     * a composite is scanned at most once per distinct depth it is reached
     * at, bounded by the depth limit. Same rule as pine-go containsNonFinite.
     * The map is only allocated once a second composite is entered, so a
     * flat list or map costs nothing extra.
     */
    private static final class Scanner {
        private Object first;
        private int firstDepth;
        private IdentityHashMap<Object, Integer> seen;

        private boolean enter(Object composite, int depth) {
            if (first == null) {
                first = composite;
                firstDepth = depth;
                return true;
            }
            if (first == composite) {
                if (firstDepth <= depth) {
                    return false;
                }
                firstDepth = depth;
                return true;
            }
            if (seen == null) {
                seen = new IdentityHashMap<>();
            } else {
                Integer d = seen.get(composite);
                if (d != null && d <= depth) {
                    return false;
                }
            }
            seen.put(composite, depth);
            return true;
        }

        boolean containsNonFinite(Object v, int depth) {
            if (v instanceof Number) {
                return isNonFinite((Number) v);
            }
            if (!(v instanceof Map) && !(v instanceof List)) {
                return false;
            }
            if (depth >= MAX_COMPOSITE_SCAN_DEPTH || !enter(v, depth)) {
                return false;
            }
            Iterable<?> children = v instanceof Map ? ((Map<?, ?>) v).values() : (List<?>) v;
            for (Object e : children) {
                if (containsNonFinite(e, depth + 1)) {
                    return true;
                }
            }
            return false;
        }
    }
}
