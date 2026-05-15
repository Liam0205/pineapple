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
            if (d == Math.floor(d) && !Double.isInfinite(d) && Math.abs(d) < 1e6) {
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
        if (Double.isInfinite(d) || Double.isNaN(d)) return Double.toString(d);

        String s = Double.toString(d);

        if (s.contains("E") || s.contains("e")) {
            s = s.toLowerCase();
            int eIdx = s.indexOf('e');
            String mantissa = s.substring(0, eIdx);
            String expPart = s.substring(eIdx + 1);
            if (mantissa.contains(".")) {
                mantissa = mantissa.replaceAll("0+$", "").replaceAll("\\.$", "");
            }
            if (!expPart.startsWith("-") && !expPart.startsWith("+")) {
                expPart = "+" + expPart;
            }
            boolean neg = expPart.startsWith("-");
            String digits = expPart.substring(1);
            if (digits.length() < 2) digits = "0" + digits;
            expPart = (neg ? "-" : "+") + digits;
            return mantissa + "e" + expPart;
        }

        // Double.toString uses non-scientific for |d| in [1e-3, 1e7).
        // Go uses scientific when integer part has > 6 digits (i.e., |d| >= 1e6 for integer-valued,
        // or more generally when the number of significant digits before decimal exceeds 6).
        String abs = s.startsWith("-") ? s.substring(1) : s;
        boolean negative = s.startsWith("-");
        int dotPos = abs.indexOf('.');
        int intPartLen = dotPos >= 0 ? dotPos : abs.length();
        if (intPartLen > 6) {
            // Convert to scientific notation matching Go format
            String allDigits = abs.replace(".", "");
            // Remove trailing zeros for precision
            int lastNonZero = allDigits.length() - 1;
            while (lastNonZero > 0 && allDigits.charAt(lastNonZero) == '0') lastNonZero--;
            allDigits = allDigits.substring(0, lastNonZero + 1);
            int exp = intPartLen - 1;
            String mantissa;
            if (allDigits.length() == 1) {
                mantissa = allDigits;
            } else {
                mantissa = allDigits.charAt(0) + "." + allDigits.substring(1);
            }
            String expStr = exp < 10 ? "0" + exp : String.valueOf(exp);
            String result = mantissa + "e+" + expStr;
            return negative ? "-" + result : result;
        }

        // Non-scientific: strip trailing zeros
        if (s.contains(".")) {
            s = s.replaceAll("0+$", "").replaceAll("\\.$", "");
        }
        return s;
    }
}
