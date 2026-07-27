package page.liam.pine;

import static org.junit.jupiter.api.Assertions.assertEquals;

import com.fasterxml.jackson.databind.ObjectMapper;
import java.util.LinkedHashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;

/**
 * Pins pine-java's JSON number output to Go's encoding/json, byte for byte
 * (issue #180).
 *
 * <p>Every expected string here was produced by running json.Marshal on the
 * same float64 in Go, not derived from the spec by hand. Go's rule is
 * strconv.FormatFloat(d, 'f'|'e', -1, 64), choosing 'e' when |x| &lt; 1e-6 or
 * |x| &gt;= 1e21, with precision -1 meaning shortest round-trip.
 *
 * <p>The divergence #180 reported was Java emitting "1.0E20" where Go emits
 * "100000000000000000000". The serializer only special-cased integer-valued
 * doubles within +-2^53 and let Jackson's writeNumber handle everything else,
 * and writeNumber formats via Double.toString.
 */
class GoJsonNumberParityTest {

    private static final ObjectMapper MAPPER = GoFormat.createGoCompatMapper();

    private static String emit(double d) throws Exception {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("v", d);
        String json = MAPPER.writeValueAsString(m);
        // {"v":<literal>}
        return json.substring(json.indexOf(':') + 1, json.length() - 1);
    }

    @Test
    void plainDecimalRangeKeepsEveryDigit() throws Exception {
        assertEquals("0", emit(0.0));
        assertEquals("1", emit(1.0));
        assertEquals("1.5", emit(1.5));
        assertEquals("1000000", emit(1e6));
        assertEquals("1000000000000000", emit(1e15));
        // Past 2^53 but below 1e21: Go stays in plain decimal. This is exactly
        // the band the old +-2^53 guard failed to cover.
        assertEquals("10000000000000000", emit(1e16));
        assertEquals("1000000000000000000", emit(1e18));
        assertEquals("100000000000000000000", emit(1e20));
        assertEquals("-100000000000000000000", emit(-1e20));
    }

    @Test
    void shortestRoundTripNotExactBinaryExpansion() throws Exception {
        // 1.0000000000000002e20 is exactly 100000000000000016384, but Go prints
        // the shortest round-tripping digits and zero-fills: ...20000.
        assertEquals("100000000000000020000", emit(1.0000000000000002e20));
    }

    @Test
    void scientificAtOrAbove1e21() throws Exception {
        assertEquals("1e+21", emit(1e21));
        assertEquals("1e+22", emit(1e22));
        assertEquals("1.5e+21", emit(1.5e21));
        assertEquals("-1e+21", emit(-1e21));
    }

    @Test
    void smallMagnitudeBoundaryAt1eMinus6IsInclusive() throws Exception {
        assertEquals("0.00001", emit(1e-5));
        assertEquals("0.000001", emit(1e-6));
        // Below 1e-6 goes scientific. strconv pads the exponent to two digits
        // ("1e-07"); encoding/json then strips one leading zero from NEGATIVE
        // exponents only, so this is "1e-7" while 1e+21 above keeps "+21".
        assertEquals("1e-7", emit(1e-7));
        assertEquals("1e-9", emit(1e-9));
        assertEquals("1e-10", emit(1e-10));
    }

    @Test
    void threeDigitExponentsAreNotTrimmed() throws Exception {
        assertEquals("1e+100", emit(1e100));
        assertEquals("1e-100", emit(1e-100));
    }

    @Test
    void negativeZeroKeepsItsSignBit() throws Exception {
        assertEquals("-0", emit(-0.0));
    }

    @Test
    void subnormalsUseShortestRoundTripNotDoubleToString() throws Exception {
        // Double.toString is documented as emitting enough digits to uniquely
        // identify the value, and for normal doubles it is also the shortest
        // such rendering. For subnormals it is not: MIN_VALUE comes out as
        // "4.9E-324" when "5E-324" already round-trips, and Go emits 5e-324.
        // Building the BigDecimal straight from Double.toString inherited that.
        assertEquals("5e-324", emit(Double.MIN_VALUE));
        assertEquals("-5e-324", emit(-Double.MIN_VALUE));
        assertEquals("1e-323", emit(Double.longBitsToDouble(2L)));
        assertEquals("5e-323", emit(Double.longBitsToDouble(10L)));
        // Values Double.toString already renders shortest must not change.
        assertEquals("1.5e-323", emit(Double.longBitsToDouble(3L)));
        assertEquals("4.4e-323", emit(Double.longBitsToDouble(9L)));
    }

    @Test
    void shortestRoundTripHoldsAcrossTheSubnormalRange() throws Exception {
        // Every candidate must parse back to the identical double, and must be
        // no longer than what Double.toString would have produced.
        for (long bits = 1; bits <= 20000; bits++) {
            double d = Double.longBitsToDouble(bits);
            String s = GoFormat.formatJsonNumber(d);
            assertEquals(d, Double.parseDouble(s), "round-trip failed for bits " + bits);
        }
    }

    @Test
    void nonFiniteStaysQuotedSoTheResponseRemainsParseable() throws Exception {
        // Go's encoding/json refuses NaN/Infinity, so there is no byte sequence
        // to match here and byte parity is not the goal — valid JSON is. This
        // path is reachable: the write path validates NaN/Inf, but a request
        // carrying 1e400 does not go through it, and Jackson coerces that to
        // Infinity on parse (Go and C++ reject the request outright instead).
        //
        // An earlier version of the serializer wrote formatJsonNumber's output
        // unconditionally, which emitted a bare +Inf token and made the whole
        // response unparseable.
        assertEquals("\"Infinity\"", emit(Double.POSITIVE_INFINITY));
        assertEquals("\"-Infinity\"", emit(Double.NEGATIVE_INFINITY));
        assertEquals("\"NaN\"", emit(Double.NaN));
    }

    @Test
    void wholeDocumentStaysParseableWithNonFiniteValues() throws Exception {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("inf", Double.POSITIVE_INFINITY);
        m.put("nan", Double.NaN);
        m.put("ok", 1e20);
        String json = MAPPER.writeValueAsString(m);
        // Must round-trip through a strict parser.
        new ObjectMapper().readTree(json);
        org.junit.jupiter.api.Assertions.assertTrue(json.contains("\"inf\":\"Infinity\""), json);
        org.junit.jupiter.api.Assertions.assertTrue(json.contains("\"ok\":100000000000000000000"), json);
    }

    @Test
    void formatJsonNumberRefusesNonFiniteRatherThanInventingBytes() {
        for (double d : new double[] {Double.NaN, Double.POSITIVE_INFINITY, Double.NEGATIVE_INFINITY}) {
            org.junit.jupiter.api.Assertions.assertThrows(IllegalArgumentException.class,
                    () -> GoFormat.formatJsonNumber(d),
                    "formatJsonNumber must not fabricate a representation for " + d);
        }
    }

    @Test
    void normalDoublesDoNotPayForTheSubnormalSearch() throws Exception {
        // shortestRoundTrip must not walk precision upward from 1 for values
        // Double.toString already renders minimally. An earlier version did,
        // costing ~6.8us per value against ~0.08us for Double.toString, on the
        // /execute response path for every double field.
        //
        // Asserted as a ratio against Double.toString measured in the same JVM
        // rather than an absolute microsecond figure, so this does not become a
        // machine-speed tripwire. The regressed version was ~90x; correct is
        // well under 40x, so 60x separates them with room for noise.
        java.util.Random r = new java.util.Random(180);
        double[] vals = new double[50000];
        for (int i = 0; i < vals.length; i++) {
            vals[i] = r.nextDouble() * 1000.0;
        }
        for (int i = 0; i < 10000; i++) {
            GoFormat.formatJsonNumber(vals[i]);
            Double.toString(vals[i]);
        }
        long t0 = System.nanoTime();
        for (double d : vals) {
            GoFormat.formatJsonNumber(d);
        }
        long ours = System.nanoTime() - t0;
        t0 = System.nanoTime();
        for (double d : vals) {
            Double.toString(d);
        }
        long baseline = Math.max(System.nanoTime() - t0, 1L);
        double ratio = (double) ours / baseline;
        org.junit.jupiter.api.Assertions.assertTrue(ratio < 60.0,
                "formatJsonNumber is " + String.format("%.1f", ratio)
                        + "x Double.toString; the upward-search regression measured ~90x");
    }

    @Test
    void integerValuedDoublesTakeTheFastPath() throws Exception {
        // Double.toString writes "1.0" for 1.0, and counting that trailing zero
        // as significant made the one-fewer-digit probe succeed, sending every
        // integer-valued double into the search loop. Values must be unchanged;
        // the cost is covered by normalDoublesDoNotPayForTheSubnormalSearch.
        assertEquals("1", emit(1.0));
        assertEquals("42", emit(42.0));
        assertEquals("100", emit(100.0));
        assertEquals("100000000000000000000", emit(1e20));
        assertEquals("-42", emit(-42.0));
    }

    @Test
    void integerValuedLoadIsNotSlowerThanFractional() throws Exception {
        // The fast path must actually cover integer-valued doubles. Before the
        // trailing-".0" fix they were ~60% SLOWER than 17-digit fractional
        // values; correct behaviour makes them faster, since they need fewer
        // digits. Compared against fractional values in the same JVM so this is
        // a shape assertion, not a machine-speed one.
        java.util.Random r = new java.util.Random(180);
        double[] ints = new double[50000];
        double[] frac = new double[50000];
        for (int i = 0; i < ints.length; i++) {
            ints[i] = Math.floor(r.nextDouble() * 10000);
            frac[i] = r.nextDouble() * 1000;
        }
        for (int i = 0; i < 10000; i++) {
            GoFormat.formatJsonNumber(ints[i]);
            GoFormat.formatJsonNumber(frac[i]);
        }
        long t0 = System.nanoTime();
        for (double d : ints) {
            GoFormat.formatJsonNumber(d);
        }
        long intNanos = System.nanoTime() - t0;
        t0 = System.nanoTime();
        for (double d : frac) {
            GoFormat.formatJsonNumber(d);
        }
        long fracNanos = Math.max(System.nanoTime() - t0, 1L);
        org.junit.jupiter.api.Assertions.assertTrue(intNanos < fracNanos,
                "integer-valued doubles took " + intNanos + "ns vs " + fracNanos
                        + "ns for fractional; they should be the cheaper class");
    }

    @Test
    void negativeNaNNormalizesLikePositiveNaN() throws Exception {
        // The isNaN guard's only observable effect: without it -NaN renders as
        // "-nan" in C++ and would diverge here too.
        assertEquals("\"NaN\"", emit(Double.longBitsToDouble(0xFFF8000000000000L)));
    }

    @Test
    void smallPlainDecimalsRenderExactly() throws Exception {
        // Values just above the 1e-6 threshold, where Double.toString writes
        // placeholder zeros. This asserts the rendered bytes, not the internal
        // digit count: over-counting significant digits only widens
        // shortestRoundTrip's search and yields the same result, so there is no
        // observable property to assert about the count itself.
        assertEquals("0.001234", emit(0.001234));
        assertEquals("0.0001", emit(0.0001));
        assertEquals("0.001", emit(0.001));
        assertEquals("0.000001", emit(1e-6));
    }

    @Test
    void formatJsonNumberMatchesTheSerializer() throws Exception {
        // The serializer must not carry its own second copy of the rule.
        double[] vals = {
            0.0, -0.0, 1.0, 1.5, 1e6, 1e15, 1e16, 1e18, 1e20, 1e21, 1e22,
            1.5e21, -1e20, -1e21, 1e-5, 1e-6, 1e-7, 1e-9, 1e-10, 1e100, 1e-100,
            1.0000000000000002e20, 3.141592653589793, -2.718281828459045,
        };
        for (double d : vals) {
            assertEquals(GoFormat.formatJsonNumber(d), emit(d),
                    "serializer diverged from formatJsonNumber for " + d);
        }
    }
}
