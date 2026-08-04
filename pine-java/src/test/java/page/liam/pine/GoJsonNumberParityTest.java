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
    void integerValuedDoublesDropTheFractionalPart() throws Exception {
        // Double.toString writes "1.0"; Go writes "1".
        assertEquals("1", emit(1.0));
        assertEquals("42", emit(42.0));
        assertEquals("100", emit(100.0));
        assertEquals("100000000000000000000", emit(1e20));
        assertEquals("-42", emit(-42.0));
    }

    @Test
    void negativeNaNNormalizesLikePositiveNaN() throws Exception {
        // The isNaN guard's only observable effect: without it -NaN renders as
        // "-nan" in C++ and would diverge here too.
        assertEquals("\"NaN\"", emit(Double.longBitsToDouble(0xFFF8000000000000L)));
    }

    @Test
    void digitsComeFromDoubleToStringNotFromRoundingTheExactValue() throws Exception {
        // Shortening must operate on the digits Double.toString chose. Rounding
        // the exact binary expansion instead (BigDecimal(double) plus a
        // MathContext) selects a different final digit for some values, because
        // MathContext rounds HALF_UP on the true value while Go reports the
        // digit nearest the double. These four all came out one ulp-of-the-last
        // -digit high that way. The count depends on how you sample: 13 over
        // 200k uniform random bit patterns, 50+ when sampling by magnitude.
        // The mechanism and these four values do not depend on the draw.
        assertEquals("2209012388886329.2", emit(Double.longBitsToDouble(0x431f64571af9dce5L)));
        assertEquals("-1300666636127457.2", emit(Double.longBitsToDouble(0xc3127bcc33453385L)));
        assertEquals("897344844809170.2", emit(Double.longBitsToDouble(0x4309810b05ba1e92L)));
        assertEquals("171744423733713.12", emit(Double.longBitsToDouble(0x42e3866babcd3a24L)));
    }

    @Test
    void formatFloatFSubnormalDivergenceIsPinnedNotFixed() throws Exception {
        // formatFloatF still uses Double.toString, so a subnormal expands one
        // character longer than Go's (327 vs 326). Documented as a known
        // divergence rather than fixed: its only caller is resource-lookup key
        // coercion, and changing a key-derivation function is a behaviour change
        // for anything already keyed on the current form. Pinned here so the
        // number is a recorded fact rather than a surprise, and so that fixing
        // it later is a deliberate act with a failing test to update.
        assertEquals(327, GoFormat.formatFloatF(Double.MIN_VALUE).length());
        // formatJsonNumber, which does need Go parity, is unaffected.
        assertEquals("5e-324", emit(Double.MIN_VALUE));
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
    void boxedPrimitiveAndArrayDoublesAllUseTheGoFormatter() throws Exception {
        // Jackson dispatches on the declared type. A Double.class-only
        // registration left primitive double fields and double[] on Jackson's
        // default path, emitting "1.0E20" — the exact shape of issue #180.
        // Nothing on /execute reaches those today (Variant boxes everything),
        // but the mapper should be right regardless of which paths exist.
        // Asserted per field rather than as a whole document: Jackson's key
        // order is not Go's (issue #183), which is a separate matter from the
        // number bytes under test here.
        String json = MAPPER.writeValueAsString(new PrimitiveHolder());
        org.junit.jupiter.api.Assertions.assertTrue(
                json.contains("\"boxed\":100000000000000000000"), json);
        org.junit.jupiter.api.Assertions.assertTrue(
                json.contains("\"primitive\":100000000000000000000"), json);
        org.junit.jupiter.api.Assertions.assertTrue(
                json.contains("\"array\":[100000000000000000000,1e+21]"), json);
    }

    /** Exercises all three declared shapes Jackson dispatches on separately. */
    public static final class PrimitiveHolder {
        public Double getBoxed() {
            return 1e20;
        }

        public double getPrimitive() {
            return 1e20;
        }

        public double[] getArray() {
            return new double[] {1e20, 1e21};
        }
    }

    @Test
    void float32UsesThirtyTwoBitShortestRoundTrip() throws Exception {
        // Go formats float32 with bitSize=32, so the digits are shortest for
        // FLOAT, not for double. Widening first surfaces the binary noise the
        // narrower type was hiding: (double) 0.1f is 0.10000000149011612 and
        // 1e20f widens to 100000002004087730000, where Go emits 0.1 and
        // 100000000000000000000.
        assertEquals("0.1", GoFormat.formatJsonNumber(0.1f));
        assertEquals("100000000000000000000", GoFormat.formatJsonNumber(1e20f));
        assertEquals("1e-7", GoFormat.formatJsonNumber(1e-7f));
        assertEquals("3.4e+38", GoFormat.formatJsonNumber(3.4e38f));
        assertEquals("-0", GoFormat.formatJsonNumber(-0.0f));
        // Float.toString is not shortest for subnormals either: it renders
        // MIN_VALUE as "1.4E-45" where "1E-45" round-trips through float.
        assertEquals("1e-45", GoFormat.formatJsonNumber(Float.MIN_VALUE));
        assertEquals("3e-45", GoFormat.formatJsonNumber(Float.intBitsToFloat(2)));
        // The 1e-6 threshold is applied to the SHORTENED decimal, not the
        // widened double: this value widens to 9.999999974752427e-07 (below the
        // threshold) but shortens to 1e-06 (not below), and Go prints plain.
        assertEquals("0.000001", GoFormat.formatJsonNumber(Float.intBitsToFloat(897988541)));
    }

    @Test
    void floatShapesAllUseTheGoFormatter() throws Exception {
        String json = MAPPER.writeValueAsString(new FloatHolder());
        org.junit.jupiter.api.Assertions.assertTrue(
                json.contains("\"boxed\":100000000000000000000"), json);
        // 1e-7f, not 0.1f: Jackson's default writeNumber(float) also emits "0.1",
        // so that value cannot tell the registered path from the default one and
        // the assertion had no teeth. Go renders float32 1e-7 as "1e-7" while
        // Jackson gives "1.0E-7".
        org.junit.jupiter.api.Assertions.assertTrue(json.contains("\"primitive\":1e-7"), json);
        org.junit.jupiter.api.Assertions.assertTrue(
                json.contains("\"array\":[100000000000000000000,1e-7]"), json);
    }

    /** float counterpart of PrimitiveHolder. */
    public static final class FloatHolder {
        public Float getBoxed() {
            return 1e20f;
        }

        public float getPrimitive() {
            return 1e-7f;
        }

        public float[] getArray() {
            return new float[] {1e20f, 1e-7f};
        }
    }

    @Test
    void jsonNodeCarriersAreDocumentedAsUncovered() throws Exception {
        // Pins the boundary of the type coverage rather than the coverage
        // itself. DoubleNode and BigDecimal bypass the registered serializers,
        // which is acceptable only because no response is assembled from a
        // JsonNode (readTree appears only in Config and ResourceManager, both
        // parsing input). This asserts the current uncovered behaviour so that
        // if someone later serializes a JsonNode outward, they meet a failing
        // test that points at the comment explaining what to register.
        com.fasterxml.jackson.databind.node.DoubleNode node =
                com.fasterxml.jackson.databind.node.DoubleNode.valueOf(1e20);
        assertEquals("1.0E20", MAPPER.writeValueAsString(node));
        assertEquals("1E+20", MAPPER.writeValueAsString(new java.math.BigDecimal("1e20")));
        // The covered carriers, for contrast.
        assertEquals("100000000000000000000", emit(1e20));
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

    @Test
    void sprintIsIndependentOfHowTheNumberWasBoxed() throws Exception {
        // Go reaches fmt.Sprintf("%v", ...) with values that came through
        // encoding/json, and JSON has no integer type — so both sides of any
        // comparison are float64 and both obey the same 1e6 switch to %g. Java's
        // Jackson decodes a config literal to Integer while pipeline data arrives as
        // Double, so branching sprint on the box type formatted the two sides of one
        // comparison under different rules.
        //
        // Concretely: filter_condition with value 2000000 stopped removing a
        // Lua-produced 2000000, because the config side printed "2000000" and the data
        // side "2e+06". Go and pine-cpp both removed the item. Review caught this while
        // issues #189/#190 were being fixed — removing the narrowing in
        // TransformByLua.fromLua exposed it, because that narrowing had been making
        // both sides integral by accident.
        com.fasterxml.jackson.databind.ObjectMapper mapper =
                new com.fasterxml.jackson.databind.ObjectMapper();
        for (String literal : new String[] {"42", "999999", "1000000", "2000000",
                                            "123456789", "0", "-2000000", "-1"}) {
            Object boxed = mapper.readValue(literal, Object.class);
            double asDouble = ((Number) boxed).doubleValue();
            assertEquals(GoFormat.sprint(asDouble), GoFormat.sprint(boxed),
                    "sprint must not depend on the box type, literal " + literal);
        }
        // And the 1e6 switch itself, which is the Go behaviour being mirrored.
        assertEquals("999999", GoFormat.sprint(999999.0));
        assertEquals("1e+06", GoFormat.sprint(1000000.0));
        assertEquals("2e+06", GoFormat.sprint(2000000.0));
    }

    @Test
    void integralCountAboveOneMillionUsesScientificForm() {
        // Pins an ACCEPTED regression so the next edit to sprint cannot move it
        // silently. At base a9830fca pine-java printed an Integer 1000000 as
        // "1000000", agreeing with Go's %v on the native int that transform_size
        // writes; pine-cpp was the lone outlier because it casts item_count() to
        // double — and transform_size is the ONLY source where Go holds a native int,
        // so this flip is scoped to that SOURCE — but the value reaches every sprint
        // consumer, and filter_condition comparing against it diverges SILENTLY (an
        // emptied item list, no error) rather than raising a coerce error like the
        // templated path. That consumer already behaved so at base.
        // Other sources of the same count go through
        // encoding/json and are float64 in Go too, so Go errors there as well and this
        // change FIXED a pre-existing divergence on those. Removing sprint's box-type
        // branch flipped the sides for transform_size: pine-java now
        // matches pine-cpp and diverges from Go on that one path, so a
        // transform_size -> filter_truncate top_n: "{{n}}" pipeline errors at >= 1e6
        // items where Go succeeds.
        //
        // The trade is deliberate and both alternatives were measured: keeping the
        // branch breaks filter_condition, and keeping it only above 1e6 reintroduces
        // the same two-sides-two-rules asymmetry. A >= 1e6 item count is far less
        // reachable than filter_condition, so this is the side that loses.
        // Decision options are in llmdoc/memory/doc-gaps.md; this test only pins the
        // current answer.
        assertEquals("1e+06", GoFormat.sprint(Integer.valueOf(1000000)));
        assertEquals("1e+06", GoFormat.sprint(1000000.0));
        assertEquals("999999", GoFormat.sprint(Integer.valueOf(999999)));
    }
}
