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
     * Shortest decimal string that round-trips to {@code d}, which is what Go's
     * precision -1 means.
     *
     * <p>{@code Double.toString} is NOT that string in general: it renders
     * {@code Double.MIN_VALUE} as "4.9E-324" when the single digit "5E-324"
     * already round-trips to the same bits, and Go emits the latter.
     *
     * <p>Eight bit patterns in bits 1..200000 render with more digits than
     * needed, producing eight distinct Double.toString strings (4.9E-324,
     * 9.9E-324, 4.9E-323, 5.9E-323, 6.9E-323, 7.9E-323, 8.9E-323, 9.9E-323).
     * The count is stated per bit pattern over that scan range, since counting
     * by decimal target or over a wider enumeration gives a different number.
     *
     * <p>So search: try one significant digit, then two, and return the first
     * rendering that parses back to the identical double. The first hit is by
     * construction the shortest, since the candidates are generated in
     * increasing length.
     *
     * <p>Deliberately unoptimized. Earlier versions added a fast path that
     * skipped the search when Double.toString was already minimal, guarded by a
     * digit count. Three review rounds each found a different input class that
     * the guard silently excluded — integer-valued doubles, then everything
     * below 1.0 — because the digit count and the benchmark sample disagreed
     * about which values mattered. Each iteration was correct on output and
     * wrong on the claim in its own comment. The loop below cannot be wrong
     * about which inputs it covers, because it covers all of them the same way.
     * If this ever needs to be faster, benchmark [0.001,1), [1,1000) and
     * integer-valued separately: a sample drawn from any one of them will
     * confirm whatever you already believe.
     */
    private static String shortestRoundTrip(double d) {
        String repr = Double.toString(d);
        // Shorten only the digits Double.toString chose. Rounding the exact
        // binary value instead (BigDecimal(double) with a MathContext) picks a
        // different last digit for some values, because MathContext rounds
        // HALF_UP on the true expansion while Go's shortest algorithm reports
        // the digit nearest the double: 2209012388886329.2 in Go against
        // ...329.3 that way, over 50 such divergences in a 200k random sweep.
        // Double.toString's digits are already the correct ones; the only thing
        // wrong with them is that there can be too many.
        java.math.BigDecimal exact = new java.math.BigDecimal(repr);
        for (int precision = 1; precision < 17; precision++) {
            String candidate = exact.round(new java.math.MathContext(precision)).toString();
            if (Double.parseDouble(candidate) == d) {
                return candidate;
            }
        }
        return repr;
    }

    /**
     * Replicates Go's encoding/json number output for a float64, byte for byte.
     *
     * <p>Go's rule (encoding/json/encode.go floatEncoder): render with
     * strconv.FormatFloat(d, fmt, -1, 64), choosing 'e' when the magnitude is
     * below 1e-6 or at/above 1e21 and 'f' otherwise. Precision -1 means the
     * fewest digits that round-trip. Double.toString is that for normal
     * doubles but NOT for subnormals, so the digits come from
     * {@link #shortestRoundTrip} and only their placement differs here.
     *
     * <p>This is deliberately separate from {@link #formatFloatF} (always
     * decimal, used for Lua/field formatting) and from the %g emulation. The
     * three have different thresholds and are not interchangeable — conflating
     * the JSON path with the others is how issue #180 arose.
     *
     * <p>One quirk is load-bearing and was verified against encoding/json
     * rather than inferred: strconv pads exponents to two digits ("1e-07"),
     * and json then strips a single leading zero from NEGATIVE exponents only.
     * So 1e-7 prints as "1e-7", while 1e+21 keeps "+21" and 1e-100 keeps all
     * three digits.
     */
    public static String formatJsonNumber(double d) {
        if (Double.isNaN(d) || Double.isInfinite(d)) {
            // Go's encoding/json refuses these outright (UnsupportedValueError),
            // so there is no Go byte sequence to match and this method has no
            // correct answer to give. It throws rather than inventing one: the
            // caller decides, and the serializer keeps Jackson's quoted-string
            // form because that is the only shape that stays parseable JSON.
            //
            // An earlier version returned formatFloatF(d) here, i.e. "+Inf",
            // which the serializer then wrote as a bare token — invalid JSON.
            // Reachable in practice: the write path validates NaN/Inf, but a
            // request carrying 1e400 does not go through it, and Jackson
            // silently coerces that to Infinity on parse where Go and C++ both
            // reject it. See the isNonFinite tests.
            throw new IllegalArgumentException(
                    "NaN/Infinity has no Go encoding/json representation: " + d);
        }
        if (d == 0.0) {
            return (Double.doubleToRawLongBits(d) == Double.doubleToRawLongBits(-0.0)) ? "-0" : "0";
        }
        double abs = Math.abs(d);
        boolean scientific = abs < 1e-6 || abs >= 1e21;
        // new BigDecimal(String) is exact; new BigDecimal(double) would
        // reintroduce the full binary expansion we are trying to avoid.
        java.math.BigDecimal bd =
                new java.math.BigDecimal(shortestRoundTrip(d)).stripTrailingZeros();
        if (!scientific) {
            return bd.toPlainString();
        }
        String digits = bd.unscaledValue().abs().toString();
        int exp10 = digits.length() - bd.scale() - 1;
        StringBuilder sb = new StringBuilder();
        if (d < 0) {
            sb.append('-');
        }
        sb.append(digits.charAt(0));
        if (digits.length() > 1) {
            sb.append('.').append(digits, 1, digits.length());
        }
        sb.append('e');
        if (exp10 < 0) {
            sb.append('-');
        } else {
            sb.append('+');
        }
        // No zero-padding branch for positive exponents, deliberately. strconv
        // pads exponents to two digits and encoding/json un-pads negatives back
        // to one — but the scientific branch is only entered when |d| >= 1e21 or
        // |d| < 1e-6, so a positive exponent is never below 21 and is already
        // two digits. Verified: across 688k sampled doubles Go never emits a
        // single-digit positive exponent, and adding the pad changes no output.
        sb.append(Math.abs(exp10));
        return sb.toString();
    }

    /**
     * Replicates Go's strconv.FormatFloat(d, 'f', -1, 64).
     * Always uses decimal notation (no scientific notation).
     * Uses Double.toString, which is shortest-round-trip for normal doubles but
     * NOT for subnormals: MIN_VALUE renders "4.9E-324" where "5E-324"
     * round-trips, so the plain-decimal expansion here comes out one character
     * longer than Go's (327 vs 326).
     *
     * <p>KNOWN DIVERGENCE, deliberately not fixed here. The sole caller is
     * TransformResourceLookup's key coercion, and a request-supplied 5e-324 does
     * survive Jackson parsing and reach it, so this is reachable rather than
     * theoretical. All THREE runtimes disagree on that key, not just Java: Go
     * emits 326 characters, Java 327, and pine-cpp emits "5e-324" because
     * go_format_lookup_key's 64-byte to_chars buffer cannot hold a 326-character
     * expansion, so it returns value_too_large and falls back to scientific
     * notation. Whoever unifies this must fix the C++ buffer too, not only the
     * Java digit count. It is pre-existing and outside issue #180 (which is
     * about JSON output bytes), and changing a key-derivation function is a
     * behaviour change for anything already keyed on the current form.
     *
     * <p>An earlier version of this comment claimed the callers "never see
     * subnormals" and listed salt and condition formatting among them. Both were
     * wrong: salt uses formatG, conditions use sprint, and the one real caller is
     * reachable from a request. formatJsonNumber needs exact Go parity and uses
     * shortestRoundTrip instead.
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
        // Registered for BOTH the boxed and primitive types. Jackson dispatches
        // on the declared type, so a Double.class-only registration leaves
        // primitive `double` fields and double[] on Jackson's default path,
        // which emits "1.0E20" — the exact shape of issue #180. Nothing on
        // /execute or /stats hits that today because every number is boxed into
        // Double on the way through Variant, so this is closing a hole rather
        // than fixing a live defect; it is registered anyway because "everything
        // goes through formatJsonNumber" should be true of the mapper rather
        // than true only of the paths that happen to exist now.
        StdSerializer<Double> goDoubleSerializer = new StdSerializer<Double>(Double.class) {
            @Override
            public void serialize(Double value, JsonGenerator gen, SerializerProvider provider) throws IOException {
                // Every FINITE value goes through formatJsonNumber. Delegating
                // any of those to Jackson's writeNumber is what caused issue
                // #180: it formats via Double.toString, so everything the old
                // guard did not catch fell out as "1.0E20" where Go emits
                // "100000000000000000000". The guard only covered
                // integer-valued doubles within +-2^53, i.e. a small slice of
                // the range Go renders in plain decimal (up to 1e21).
                double d = value.doubleValue();
                if (Double.isNaN(d) || Double.isInfinite(d)) {
                    // No Go equivalent exists — encoding/json errors out on
                    // these. Keep Jackson's quoted-string form ("Infinity",
                    // "-Infinity", "NaN"): it is the only rendering that leaves
                    // the response parseable, which matters because a request
                    // carrying 1e400 reaches here without passing the write
                    // path's NaN/Inf validation. Parity is already broken
                    // upstream in that case (Go and C++ reject the request
                    // outright), so the goal here is valid JSON, not byte
                    // equality with a Go output that does not exist.
                    gen.writeNumber(d);
                    return;
                }
                gen.writeRawValue(formatJsonNumber(d));
            }
        };
        module.addSerializer(Double.class, goDoubleSerializer);
        module.addSerializer(Double.TYPE, goDoubleSerializer);
        // double[] needs its own registration: Jackson serializes primitive
        // arrays with a dedicated ArraySerializer that writes elements directly
        // rather than delegating to a per-element serializer, so neither of the
        // registrations above reaches them.
        module.addSerializer(double[].class, new StdSerializer<double[]>(double[].class) {
            @Override
            public void serialize(double[] values, JsonGenerator gen, SerializerProvider provider)
                    throws IOException {
                gen.writeStartArray();
                for (double v : values) {
                    goDoubleSerializer.serialize(v, gen, provider);
                }
                gen.writeEndArray();
            }
        });
        m.registerModule(module);
        return m;
    }
}
