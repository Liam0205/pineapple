package page.liam.pine;

/**
 * Replicates Go fmt.Sprint / strconv.FormatFloat / fmt.Sprintf("%g",...) formatting
 * for cross-runtime string consistency.
 */
public final class GoFormat {
    private GoFormat() {}

    /**
     * Replicates Go's fmt.Sprint(v) behavior:
     * - nil -> "<nil>"
     * - Boolean -> "true"/"false"
     * - Integer-valued float -> no decimal ("1" not "1.0")
     * - Other float -> shortest representation matching Go %v (e.g. "1e+20" not "1.0E20")
     * - String -> as-is
     * - Other -> toString()
     */
    public static String sprint(Object v) {
        if (v == null) return "<nil>";
        if (v instanceof Boolean) return v.toString();
        if (v instanceof Number) {
            double d = ((Number) v).doubleValue();
            if (v instanceof Long || v instanceof Integer) {
                return Long.toString(((Number) v).longValue());
            }
            if (d == Math.floor(d) && !Double.isInfinite(d) && Math.abs(d) < 1e18) {
                return Long.toString((long) d);
            }
            return formatG(d);
        }
        return v.toString();
    }

    /**
     * Replicates Go's strconv.FormatFloat(d, 'f', -1, 64).
     * Always uses decimal notation (no scientific notation).
     */
    public static String formatFloatF(double d) {
        if (d == Math.floor(d) && !Double.isInfinite(d) && Math.abs(d) < 1e18) {
            return Long.toString((long) d);
        }
        return new java.math.BigDecimal(d).stripTrailingZeros().toPlainString();
    }

    /**
     * Replicates Go's fmt.Sprintf("%g", d) behavior:
     * Go's %g uses the shortest representation, which for large numbers
     * produces scientific notation like "1.23456789e+08".
     * Unlike Java's %g which limits to 6 significant digits, Go preserves full precision.
     */
    public static String formatG(double d) {
        if (d == 0) return "0";
        // Use Double.toString which gives full precision
        String s = Double.toString(d);
        // Convert Java's "1.0E20" format to Go's "1e+20" format
        if (s.contains("E") || s.contains("e")) {
            s = s.toLowerCase();
            int eIdx = s.indexOf('e');
            String mantissa = s.substring(0, eIdx);
            String expPart = s.substring(eIdx + 1);
            // Remove trailing zeros from mantissa
            if (mantissa.contains(".")) {
                mantissa = mantissa.replaceAll("0+$", "").replaceAll("\\.$", "");
            }
            // Ensure exponent has sign
            if (!expPart.startsWith("-") && !expPart.startsWith("+")) {
                expPart = "+" + expPart;
            }
            // Pad exponent to at least 2 digits
            boolean neg = expPart.startsWith("-");
            String digits = neg ? expPart.substring(1) : expPart.substring(1);
            if (digits.length() < 2) digits = "0" + digits;
            expPart = (neg ? "-" : "+") + digits;
            return mantissa + "e" + expPart;
        }
        // Non-scientific: strip trailing zeros
        if (s.contains(".")) {
            s = s.replaceAll("0+$", "").replaceAll("\\.$", "");
        }
        return s;
    }
}
