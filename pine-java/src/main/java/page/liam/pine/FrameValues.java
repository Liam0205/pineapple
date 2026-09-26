package page.liam.pine;

import java.util.List;
import java.util.Map;

/**
 * Write-time value check shared by {@link DataFrame} and {@link ColumnFrame}.
 * Mirrors pine-go {@code validateValue} (internal/dataframe/row_frame.go) and
 * pine-cpp {@code validate_value}: the messages are part of the cross-runtime
 * error contract and must stay byte-identical.
 */
final class FrameValues {

    /**
     * Bound on the descent into nested composites. Values built from Lua or
     * JSON are acyclic, but a custom operator can hand over a self-referencing
     * map; hitting the bound abandons the whole scan (so a map with several
     * self-keys stays linear, not exponential) and leaves the value to the
     * serializer. Same bound as pine-go {@code maxCompositeScanDepth} and
     * pine-cpp {@code kMaxCompositeScanDepth}.
     */
    static final int MAX_COMPOSITE_SCAN_DEPTH = 1000;

    private enum Scan { CLEAN, FOUND, TOO_DEEP }

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
            if (scan(v, 0) == Scan.FOUND) {
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

    private static Scan scan(Object v, int depth) {
        if (v instanceof Number) {
            return isNonFinite((Number) v) ? Scan.FOUND : Scan.CLEAN;
        }
        if (!(v instanceof Map) && !(v instanceof List)) {
            return Scan.CLEAN;
        }
        if (depth >= MAX_COMPOSITE_SCAN_DEPTH) {
            return Scan.TOO_DEEP;
        }
        Iterable<?> children = v instanceof Map ? ((Map<?, ?>) v).values() : (List<?>) v;
        for (Object e : children) {
            Scan r = scan(e, depth + 1);
            if (r != Scan.CLEAN) {
                return r;
            }
        }
        return Scan.CLEAN;
    }
}
