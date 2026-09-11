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
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

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
            if (Double.doubleToRawLongBits(d) == Double.doubleToRawLongBits(-0.0)) {
                return "-0";
            }
            // The integral cutoff must NOT depend on the box type. Go reaches this
            // path with fmt.Sprintf("%v", ...) on values that came out of
            // encoding/json, and JSON has no integer type — so BOTH sides of a
            // comparison are float64 there and both obey the same 1e6 switch to %g.
            //
            // Java's Jackson decodes a JSON config literal to Integer while pipeline
            // data arrives as Double. Branching on the box type therefore formatted
            // the two sides of one comparison under different rules: filter_condition
            // with value 2000000 stopped matching a Lua-produced 2000000, because the
            // config side printed "2000000" (Integer branch) and the data side
            // "2e+06" (Double branch). Go and pine-cpp both removed the item; Java
            // kept it. Found by review while fixing issues #189/#190 — the earlier
            // narrowing in TransformByLua.fromLua had been masking this by making
            // both sides integral by accident.
            //
            // There IS a Long/Integer branch here, and it mirrors Go: %v prints a
            // genuine int plainly at any magnitude and applies the 1e6 switch to %g only
            // to float64. `transform_size` writes in.ItemCount(), a real Go int, without
            // passing through encoding/json, so this branch is what keeps Redis keys,
            // Redis member values and templated params matching Go.
            //
            // But it is NOT a proxy for "static type", and nothing may treat it as one.
            // Jackson decodes a JSON config literal to Integer and pipeline data to
            // Double for the SAME number, so any site that COMPARES two such values must
            // normalize both sides itself. FilterCondition does that (see
            // normalizeForCompare there); comparing raw sprint output made value 2000000
            // stop matching a Lua-produced 2000000 once fromLua stopped narrowing.
            //
            // I first "fixed" that by deleting this branch, flattening everything to the
            // float rule. It repaired filter_condition and broke the other three
            // consumers, and I recorded the result as an accepted trade-off. PR review
            // rejected that and was right: the free variable is whether the COMPARISON
            // normalizes, not what the shared formatter emits. Both paths now match Go.
            // Issues #189/#190.
            if (v instanceof Long || v instanceof Integer) {
                return Long.toString(((Number) v).longValue());
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
        // new BigDecimal(String) is exact; new BigDecimal(double) would
        // reintroduce the full binary expansion we are trying to avoid.
        return formatDecimal(new java.math.BigDecimal(shortestRoundTrip(d)).stripTrailingZeros(),
                Math.abs(d), d < 0);
    }
    /**
     * Go's encoding/json output for a float32, byte for byte.
     *
     * <p>Go calls strconv.AppendFloat with bitSize=32, so the digits are the
     * shortest that round-trip through a float32 — NOT through a double. That
     * distinction is the whole reason this method exists: widening first and
     * formatting as a double surfaces the binary noise the narrower type was
     * hiding. float32 0.1 must print "0.1", but (double) 0.1f is
     * 0.10000000149011612, and 1e20f widens to 100000002004087730000 where Go
     * emits 100000000000000000000.
     *
     * <p>Float.toString supplies the digits, but is not shortest for subnormals
     * — the same defect the double path has with Double.toString. It renders
     * Float.MIN_VALUE as "1.4E-45" when "1E-45" round-trips, and Go emits the
     * latter. Exhaustively over the float32 subnormals (bits 1..0x7FFFFF), nine
     * bit patterns render with more digits than needed — 1, 2, 3, 4, 6, 7, 21,
     * 29 and 71 — counted by output bytes changing, the same convention the
     * double figure above uses. So the digits go through
     * shortestRoundTrip(float) first, which shortens against float precision.
     * Placement and thresholds are then identical to the double case, which is
     * why this delegates rather than duplicating them.
     */
    public static String formatJsonNumber(float f) {
        if (Float.isNaN(f) || Float.isInfinite(f)) {
            throw new IllegalArgumentException(
                    "NaN/Infinity has no Go encoding/json representation: " + f);
        }
        if (f == 0.0f) {
            return (Float.floatToRawIntBits(f) == Float.floatToRawIntBits(-0.0f)) ? "-0" : "0";
        }
        // Re-parse the shortest float digits as a decimal, then run the same
        // placement rules as the double path over exactly those digits.
        //
        // The threshold is compared against the SHORTENED decimal, not against
        // the widened double. Go's floatEncoder tests abs(float64(f)) — but it
        // does so after strconv has already produced the 32-bit shortest form,
        // and for one float32 near the boundary the two disagree: bits
        // 897988541 widens to 9.999999974752427e-07, which is below 1e-6, while
        // its shortest float rendering is 1e-06, which is not. Go prints
        // 0.000001; comparing the widened double gives 1e-6.
        java.math.BigDecimal shortened =
                new java.math.BigDecimal(shortestRoundTrip(f)).stripTrailingZeros();
        return formatDecimal(shortened, shortened.abs().doubleValue(), f < 0);
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
        // ...329.3 that way. The count depends entirely on how you sample:
        // 11-13 over 200k uniform random bit patterns, and far more when drawing
        // by magnitude. The mechanism does not depend on the draw; see
        // llmdoc/reference/number-formatting-parity.md.
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
     * Shortest decimal string that round-trips to {@code f} through FLOAT
     * precision. Mirrors the double overload, including the reason it operates
     * on Float.toString's digits rather than on the exact binary expansion.
     */
    private static String shortestRoundTrip(float f) {
        String repr = Float.toString(f);
        java.math.BigDecimal exact = new java.math.BigDecimal(repr);
        for (int precision = 1; precision < 9; precision++) {
            String candidate = exact.round(new java.math.MathContext(precision)).toString();
            if (Float.parseFloat(candidate) == f) {
                return candidate;
            }
        }
        return repr;
    }
    /**
     * Places the decimal point for an already-shortest set of digits, applying
     * Go's encoding/json thresholds. Shared by the double and float paths: the
     * bit width only affects which digits are shortest, never where the point
     * goes or how the exponent is spelled.
     *
     * @param bd shortest round-tripping digits for the value
     * @param abs magnitude, deciding fixed versus scientific
     * @param negative whether to emit a leading '-' (passed separately so -0.0
     *     and negative zero-scale values are handled by the caller)
     */
    private static String formatDecimal(java.math.BigDecimal bd, double abs, boolean negative) {
        boolean scientific = abs < 1e-6 || abs >= 1e21;
        if (!scientific) {
            return bd.toPlainString();
        }
        String digits = bd.unscaledValue().abs().toString();
        int exp10 = digits.length() - bd.scale() - 1;
        StringBuilder sb = new StringBuilder();
        if (negative) {
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
    /**
     * Marks a map whose keys must be emitted in Go's sorted order.
     *
     * <p>Go's encoding/json sorts map keys but leaves struct fields in
     * declaration order. pine-go's response envelope is a struct
     * (`executeResponse`: common, items, warnings, trace, error) while the
     * common/items payloads inside it are maps. Java models both as Map, so
     * without an explicit marker there is nothing to distinguish "sort this" from
     * "keep declaration order" — and sorting everything reorders the envelope,
     * which is what broke the partial-error byte-exact fixture on the first
     * attempt at issue #183.
     *
     * <p>Wrap payloads with {@link #sorted}; leave the envelope unwrapped.
     */
    static final class SortedByUtf8 {
        final Map<String, Object> delegate;
        /** When true, sort this map's own keys only and do not descend. */
        final boolean shallow;

        SortedByUtf8(Map<String, Object> delegate) {
            this(delegate, false);
        }

        SortedByUtf8(Map<String, Object> delegate, boolean shallow) {
            this.delegate = delegate;
            this.shallow = shallow;
        }
    }

    /**
     * Copies a map to String keys via String.valueOf, so non-String key types
     * can be wrapped and sorted like Go does.
     *
     * <p>Needed because Go sorts map keys by their JSON representation whatever
     * the Go key type is, including map[int]...: encoding/json renders int keys
     * as strings and sorts those strings, so item index 10 sorts between 1 and 2
     * rather than after 9. trace's output_snapshot.item_writes is exactly that
     * shape — Map&lt;Integer, Map&lt;String, Object&gt;&gt; on the Java side — so
     * casting its keys to String would throw at serialization time.
     */
    private static Map<String, Object> withStringKeys(Map<?, ?> m) {
        Map<String, Object> out = new LinkedHashMap<>(Math.max(4, m.size() * 2));
        for (Map.Entry<?, ?> e : m.entrySet()) {
            out.put(String.valueOf(e.getKey()), e.getValue());
        }
        return out;
    }

    /** Wraps a payload map so its keys emit in Go's order. Null-safe. */
    static Object sorted(Map<String, Object> m) {
        return m == null ? null : new SortedByUtf8(withStringKeys(m));
    }

    /**
     * Sorts only this map's own keys, leaving its values untouched.
     *
     * <p>For responses that mix the two Go rules at different depths. /stats is
     * one: Go builds the top level as a map[string]any so it sorts, but
     * `scheduler` beneath it is SchedulerStatsSnapshot, a STRUCT, so it keeps
     * declaration order (run_count, peak_concurrency). Wrapping the whole tree
     * would sort that struct too — which is what a first attempt here did.
     *
     * <p>Use {@link #sorted} when every level is a map; use this when only the
     * level you name is.
     */
    static Object sortedShallow(Map<String, Object> m) {
        return m == null ? null : new SortedByUtf8(withStringKeys(m), true);
    }

    /**
     * Recursively wraps nested maps found inside an already-wrapped payload.
     * Go sorts at every depth, so a map nested inside a list inside a map must
     * sort too — the pine-cpp side has a test for exactly that (dump_json L5).
     */
    static Object wrapPayload(Object v) {
        if (v instanceof SortedByUtf8) {
            return v;
        }
        if (v instanceof Map) {
            // withStringKeys, not a cast: map keys are not always String here.
            return new SortedByUtf8(withStringKeys((Map<?, ?>) v));
        }
        if (v instanceof List) {
            List<?> in = (List<?>) v;
            List<Object> out = new ArrayList<>(in.size());
            for (Object e : in) {
                out.add(wrapPayload(e));
            }
            return out;
        }
        return v;
    }

    /**
     * Compares two strings by their UTF-8 byte sequences, unsigned, which is what
     * Go's `<` on strings does and therefore what encoding/json's key sort does.
     *
     * <p>Not String.compareTo: that compares UTF-16 code units and disagrees with
     * UTF-8 order for any key containing a character above the BMP. See the
     * comment at the Map serializer registration for the worked example.
     *
     * <p>Fast path for the common case: while both strings are pure ASCII the two
     * orders coincide, so the byte arrays are only materialized when a non-ASCII
     * character is actually present.
     */
    static int compareUtf8(String a, String b) {
        int n = Math.min(a.length(), b.length());
        for (int i = 0; i < n; i++) {
            char ca = a.charAt(i);
            char cb = b.charAt(i);
            if (ca == cb) {
                continue;
            }
            if (ca < 0x80 && cb < 0x80) {
                return ca - cb;
            }
            byte[] ba = a.getBytes(java.nio.charset.StandardCharsets.UTF_8);
            byte[] bb = b.getBytes(java.nio.charset.StandardCharsets.UTF_8);
            return java.util.Arrays.compareUnsigned(ba, bb);
        }
        // One is a prefix of the other, or they are equal. A shorter prefix sorts
        // first under both encodings, so length comparison is safe here.
        return Integer.compare(a.length(), b.length());
    }

    // Shared, thread-safe after construction. Built once because the mapper
    // carries the custom serializers and escape table above; constructing one
    // per call would also be the slow path in the operators that use this.
    private static final ObjectMapper GO_JSON_MARSHAL = createGoCompatMapper();

    /**
     * Replicates Go's {@code json.Marshal(v)} for a frame value: Go's float64
     * number spelling ({@link #formatJsonNumber}) for every Number carrier
     * Jackson can decode a literal into (Double, Float, Integer, Long,
     * BigInteger — Go has only float64 on the way in, see the integer
     * serializer registration in {@link #createGoCompatMapper}), maps sorted by
     * UTF-8 key order at every depth, and Go's HTML-safe escaping of
     * {@code <>&}.
     *
     * <p>Use this whenever a composite (List/Map) frame value is turned into a
     * string whose bytes feed a hash or a comparison — reorder_shuffle_by_salt
     * hashes it for the item rank. A plain Jackson mapper writes {@code 28.0}
     * and {@code 2.0E100} where Go writes {@code 28} and {@code 2e+100}, so the
     * same Lua table {@code {item_score*2, item_score*3}} hashed to different
     * ranks in the two runtimes and the shuffle came out in a different order
     * (issue #201). The response path already used this mapper; this exposes
     * the same rules to operator code so no second copy grows.
     *
     * <p>Where it deliberately differs from Go: NaN and ±Infinity inside the
     * composite are written as the quoted strings {@code "NaN"} /
     * {@code "Infinity"} rather than failing, exactly as the response path
     * does (see the NaN/Inf branch of the Double serializer). Go's
     * json.Marshal returns an error there, so no Go bytes exist to match;
     * the frame write path rejects non-finite scalars, and nested composites
     * are not validated, so this is reachable only from a Lua table holding
     * {@code 0/0}.
     *
     * @throws IOException only on a Jackson failure unrelated to the value's
     *         numbers (e.g. a self-referencing structure); non-finite numbers
     *         do not throw.
     */
    public static String marshalJson(Object v) throws IOException {
        return GO_JSON_MARSHAL.writeValueAsString(wrapPayload(v));
    }

    static ObjectMapper createGoCompatMapper() {
        ObjectMapper m = new ObjectMapper();
        m.getFactory().setCharacterEscapes(new CharacterEscapes() {
            private final int[] esc = initEsc();
            private int[] initEsc() {
                int[] e = standardAsciiEscapesForJSON();
                e['<'] = ESCAPE_CUSTOM;
                e['>'] = ESCAPE_CUSTOM;
                e['&'] = ESCAPE_CUSTOM;
                // Every control character Jackson would render as a 6-char hex escape has to
                // be taken over too, because Jackson emits UPPERCASE hex digits
                // and Go emits lowercase (000B versus 000b). Only the
                // code points whose hex contains a digit above 9 actually differ
                // (0x0B, 0x0E, 0x0F, 0x1A-0x1F), but claiming the whole range is
                // simpler than enumerating them and cannot drift.
                //
                // Jackson's own two-character escapes (backspace, tab, newline,
                // form feed, carriage return) match Go
                // already and are left alone by standardAsciiEscapesForJSON.
                for (int c = 0; c < 0x20; c++) {
                    if (e[c] == ESCAPE_STANDARD) {
                        e[c] = ESCAPE_CUSTOM;
                    }
                }
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
                    default:
                        if (ch < 0x20) {
                            // Lowercase, matching Go. String.format("%04x") is
                            // lowercase by contract; %04X would reintroduce the bug.
                            return new SerializedString(String.format("\\u%04x", ch));
                        }
                        return null;
                }
            }
        });
        SimpleModule module = new SimpleModule();
        // Registered for the boxed, primitive and array forms of both widths.
        // Jackson dispatches on the declared type, so a Double.class-only
        // registration left primitive `double`, double[], and every float form
        // on Jackson's default path emitting "1.0E20" — the shape of issue #180.
        //
        // Scope of this claim, stated precisely because earlier versions of this
        // comment overreached: these six registrations cover every carrier that
        // a frame value can take on a response path. Frame values are Double or
        // Float (pine-go row_frame.go, pine-java DataFrame/ColumnFrame), and
        // arrays and primitives are covered so the mapper does not depend on
        // which of those forms a caller happens to declare.
        //
        // NOT covered, deliberately: JsonNode carriers (DoubleNode, FloatNode,
        // DecimalNode) and BigDecimal, which still emit Jackson's default form.
        // Checked rather than assumed — readTree appears only in Config and
        // ResourceManager, both parsing configuration on the way IN, and no
        // response is assembled from a JsonNode. If a future change serializes a
        // JsonNode outward, these need registering too.
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
        // Float gets the same three registrations. It is an accepted frame value
        // type in all three runtimes (pine-go row_frame.go's `case float32`,
        // pine-java DataFrame/ColumnFrame's `instanceof Float`), so a custom
        // operator writing one reaches the serializer even though no built-in
        // operator does today — the same "closing a hole" reasoning as
        // Double.TYPE above, and the reason the type list has to be complete
        // rather than just covering the paths that exist.
        StdSerializer<Float> goFloatSerializer = new StdSerializer<Float>(Float.class) {
            @Override
            public void serialize(Float value, JsonGenerator gen, SerializerProvider provider)
                    throws IOException {
                float f = value.floatValue();
                if (Float.isNaN(f) || Float.isInfinite(f)) {
                    gen.writeNumber(f);
                    return;
                }
                gen.writeRawValue(formatJsonNumber(f));
            }
        };
        module.addSerializer(Float.class, goFloatSerializer);
        module.addSerializer(Float.TYPE, goFloatSerializer);
        module.addSerializer(float[].class, new StdSerializer<float[]>(float[].class) {
            @Override
            public void serialize(float[] values, JsonGenerator gen, SerializerProvider provider)
                    throws IOException {
                gen.writeStartArray();
                for (float v : values) {
                    goFloatSerializer.serialize(v, gen, provider);
                }
                gen.writeEndArray();
            }
        });
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
        // Integer carriers take the SAME float64 path. Go's encoding/json has no
        // integer type on the way in: every JSON number literal in a request,
        // resource or config becomes float64, so 9007199254740993 is already
        // 9007199254740992 before Go ever prints it, and a 30-digit literal
        // prints as 1.2345678901234568e+29. Jackson decodes the same literals to
        // Integer / Long / BigInteger and they reach the frame unchanged
        // (Column.java dispatches on the exact class), so Jackson's default
        // exact-decimal output diverged from Go for every integer past 2^53 —
        // on the response body and, via GoFormat.marshalJson, in the bytes
        // reorder_shuffle_by_salt hashes (found by review of issue #201).
        //
        // Converting through double is lossless below 2^53 and matches Go's
        // float64 spelling above it. The one case it does NOT match is a Java
        // Long that stands for a genuine Go int (item counts, /stats counters):
        // Go prints those exactly at any magnitude. Every such value in this
        // codebase is a count far below 2^53, where the two spellings are
        // byte-identical, so the float64 rule is the one that agrees with Go
        // on every value a response can actually carry. BigInteger past the
        // double range becomes ±Infinity and takes the quoted-string branch,
        // as Go would have failed to parse it in the first place.
        StdSerializer<Number> goIntegerSerializer = new StdSerializer<Number>(Number.class) {
            @Override
            public void serialize(Number value, JsonGenerator gen, SerializerProvider provider)
                    throws IOException {
                goDoubleSerializer.serialize(value.doubleValue(), gen, provider);
            }
        };
        module.addSerializer(Long.class, goIntegerSerializer);
        module.addSerializer(Long.TYPE, goIntegerSerializer);
        module.addSerializer(Integer.class, goIntegerSerializer);
        module.addSerializer(Integer.TYPE, goIntegerSerializer);
        module.addSerializer(Short.class, goIntegerSerializer);
        module.addSerializer(Short.TYPE, goIntegerSerializer);
        module.addSerializer(Byte.class, goIntegerSerializer);
        module.addSerializer(Byte.TYPE, goIntegerSerializer);
        module.addSerializer(java.math.BigInteger.class, goIntegerSerializer);
        module.addSerializer(long[].class, new StdSerializer<long[]>(long[].class) {
            @Override
            public void serialize(long[] values, JsonGenerator gen, SerializerProvider provider)
                    throws IOException {
                gen.writeStartArray();
                for (long v : values) {
                    goDoubleSerializer.serialize((double) v, gen, provider);
                }
                gen.writeEndArray();
            }
        });
        module.addSerializer(int[].class, new StdSerializer<int[]>(int[].class) {
            @Override
            public void serialize(int[] values, JsonGenerator gen, SerializerProvider provider)
                    throws IOException {
                gen.writeStartArray();
                for (int v : values) {
                    goDoubleSerializer.serialize((double) v, gen, provider);
                }
                gen.writeEndArray();
            }
        });
        // Go sorts MAP keys but not STRUCT fields, and pine-go models the
        // response envelope as a struct (`executeResponse`) while common/items
        // payloads are `map[string]any`. So `{"common":...,"items":...,"error":...}`
        // keeps declaration order while the payloads inside it sort. An earlier
        // version of this registration sorted every Map and moved "error" ahead
        // of "items", breaking the byte-exact fixture that covers partial errors.
        //
        // Java has no struct/map distinction to key off, so the distinction is
        // made explicit: payload maps are wrapped in SortedByUtf8 and the
        // envelope is a plain LinkedHashMap whose order is the declaration order.
        module.addSerializer(SortedByUtf8.class, new StdSerializer<SortedByUtf8>(SortedByUtf8.class) {
            @Override
            public void serialize(SortedByUtf8 value, JsonGenerator gen, SerializerProvider provider)
                    throws IOException {
                List<String> keys = new ArrayList<>(value.delegate.keySet());
                keys.sort(GoFormat::compareUtf8);
                gen.writeStartObject();
                for (String k : keys) {
                    gen.writeFieldName(k);
                    Object v = value.delegate.get(k);
                    provider.defaultSerializeValue(value.shallow ? v : wrapPayload(v), gen);
                }
                gen.writeEndObject();
            }
        });
        m.registerModule(module);
        return m;
    }
}
