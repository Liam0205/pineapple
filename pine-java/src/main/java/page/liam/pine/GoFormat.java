package page.liam.pine;

import com.fasterxml.jackson.core.JsonGenerator;
import com.fasterxml.jackson.core.SerializableString;
import com.fasterxml.jackson.core.io.CharacterEscapes;
import com.fasterxml.jackson.core.io.SerializedString;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.SerializerProvider;
import com.fasterxml.jackson.databind.module.SimpleModule;
import com.fasterxml.jackson.databind.ser.std.StdSerializer;

import java.io.IOException;
import java.util.List;

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
     * - List/Array -> "[a b c]" (space-separated, no commas)
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
            if (Double.doubleToRawLongBits(d) == Double.doubleToRawLongBits(-0.0)) {
                return "-0";
            }
            if (d == Math.floor(d) && !Double.isInfinite(d) && Math.abs(d) < 1e6) {
                return Long.toString((long) d);
            }
            return formatG(d);
        }
        if (v instanceof List) {
            List<?> list = (List<?>) v;
            StringBuilder sb = new StringBuilder("[");
            for (int i = 0; i < list.size(); i++) {
                if (i > 0) sb.append(" ");
                sb.append(sprint(list.get(i)));
            }
            sb.append("]");
            return sb.toString();
        }
        if (v.getClass().isArray()) {
            Object[] arr = toObjectArray(v);
            StringBuilder sb = new StringBuilder("[");
            for (int i = 0; i < arr.length; i++) {
                if (i > 0) sb.append(" ");
                sb.append(sprint(arr[i]));
            }
            sb.append("]");
            return sb.toString();
        }
        return v.toString();
    }

    /**
     * Replicates Go's strconv.FormatFloat(d, 'f', -1, 64).
     * Always uses decimal notation (no scientific notation).
     * Uses Double.toString for shortest round-trip representation.
     */
    public static String formatFloatF(double d) {
        if (Double.doubleToRawLongBits(d) == Double.doubleToRawLongBits(-0.0)) {
            return "-0";
        }
        if (Double.isNaN(d)) return "NaN";
        if (d == Double.POSITIVE_INFINITY) return "+Inf";
        if (d == Double.NEGATIVE_INFINITY) return "-Inf";
        if (d == Math.floor(d) && !Double.isInfinite(d) && Math.abs(d) < 1e18) {
            return Long.toString((long) d);
        }
        // Double.toString gives shortest round-trip, but may use scientific notation.
        // Convert to plain decimal form (no 'E').
        String s = Double.toString(d);
        if (!s.contains("E") && !s.contains("e")) {
            return s;
        }
        // Has scientific notation — convert to plain decimal using BigDecimal(String)
        // Note: new BigDecimal(String) is exact; new BigDecimal(double) introduces binary error.
        return new java.math.BigDecimal(s).stripTrailingZeros().toPlainString();
    }

    /**
     * Replicates Go's fmt.Sprintf("%g", d) behavior:
     * Go's %g uses the shortest representation, which for large numbers
     * produces scientific notation like "1.23456789e+08".
     * Unlike Java's %g which limits to 6 significant digits, Go preserves full precision.
     */
    public static String formatG(double d) {
        if (d == 0) {
            if (Double.doubleToRawLongBits(d) == Double.doubleToRawLongBits(-0.0)) return "-0";
            return "0";
        }
        if (Double.isNaN(d)) return "NaN";
        if (d == Double.POSITIVE_INFINITY) return "+Inf";
        if (d == Double.NEGATIVE_INFINITY) return "-Inf";

        String s = Double.toString(d);

        if (s.contains("E") || s.contains("e")) {
            s = s.toLowerCase();
            int eIdx = s.indexOf('e');
            String mantissa = s.substring(0, eIdx);
            String expPart = s.substring(eIdx + 1);

            // Parse exponent value
            int expValue = Integer.parseInt(expPart);

            // Go uses scientific when exponent < -4 OR integer part would have > 6 digits.
            // In this branch, Java only gives scientific for exp <= -4 or exp >= 7.
            // Only exp == -4 (and theoretically -3 to 5) should convert to decimal.
            if (expValue >= -4 && expValue <= 5) {
                return new java.math.BigDecimal(Double.toString(d)).stripTrailingZeros().toPlainString();
            }

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
            String mantissaResult;
            if (allDigits.length() == 1) {
                mantissaResult = allDigits;
            } else {
                mantissaResult = allDigits.charAt(0) + "." + allDigits.substring(1);
            }
            String expStr = exp < 10 ? "0" + exp : String.valueOf(exp);
            String result = mantissaResult + "e+" + expStr;
            return negative ? "-" + result : result;
        }

        // Non-scientific: strip trailing zeros
        if (s.contains(".")) {
            s = s.replaceAll("0+$", "").replaceAll("\\.$", "");
        }
        return s;
    }

    /**
     * Converts primitive arrays to Object arrays for sprint formatting.
     */
    private static Object[] toObjectArray(Object arr) {
        if (arr instanceof Object[]) return (Object[]) arr;
        if (arr instanceof int[]) {
            int[] a = (int[]) arr;
            Object[] result = new Object[a.length];
            for (int i = 0; i < a.length; i++) result[i] = a[i];
            return result;
        }
        if (arr instanceof long[]) {
            long[] a = (long[]) arr;
            Object[] result = new Object[a.length];
            for (int i = 0; i < a.length; i++) result[i] = a[i];
            return result;
        }
        if (arr instanceof double[]) {
            double[] a = (double[]) arr;
            Object[] result = new Object[a.length];
            for (int i = 0; i < a.length; i++) result[i] = a[i];
            return result;
        }
        if (arr instanceof float[]) {
            float[] a = (float[]) arr;
            Object[] result = new Object[a.length];
            for (int i = 0; i < a.length; i++) result[i] = a[i];
            return result;
        }
        if (arr instanceof boolean[]) {
            boolean[] a = (boolean[]) arr;
            Object[] result = new Object[a.length];
            for (int i = 0; i < a.length; i++) result[i] = a[i];
            return result;
        }
        // Fallback for other primitive array types
        int len = java.lang.reflect.Array.getLength(arr);
        Object[] result = new Object[len];
        for (int i = 0; i < len; i++) result[i] = java.lang.reflect.Array.get(arr, i);
        return result;
    }

    /**
     * Creates an ObjectMapper that escapes &lt;, &gt;, &amp;, U+2028, U+2029
     * to match Go encoding/json's default HTML-safe output.
     */
    static ObjectMapper createGoCompatMapper() {
        ObjectMapper m = new ObjectMapper();
        m.getFactory().setCharacterEscapes(new CharacterEscapes() {
            private final int[] esc = initEsc();
            private int[] initEsc() {
                int[] e = standardAsciiEscapesForJSON();
                e['<'] = ESCAPE_CUSTOM;
                e['>'] = ESCAPE_CUSTOM;
                e['&'] = ESCAPE_CUSTOM;
                return e;
            }
            @Override public int[] getEscapeCodesForAscii() { return esc; }
            @Override public SerializableString getEscapeSequence(int ch) {
                switch (ch) {
                    case '<': return new SerializedString("\\u003c");
                    case '>': return new SerializedString("\\u003e");
                    case '&': return new SerializedString("\\u0026");
                    case 0x2028: return new SerializedString("\\u2028");
                    case 0x2029: return new SerializedString("\\u2029");
                    default: return null;
                }
            }
        });
        SimpleModule module = new SimpleModule();
        module.addSerializer(Double.class, new StdSerializer<Double>(Double.class) {
            @Override
            public void serialize(Double value, JsonGenerator gen, SerializerProvider provider) throws IOException {
                if (Double.doubleToRawLongBits(value) == Double.doubleToRawLongBits(-0.0)) {
                    gen.writeNumber(0);
                } else {
                    gen.writeNumber(value.doubleValue());
                }
            }
        });
        m.registerModule(module);
        return m;
    }
}
