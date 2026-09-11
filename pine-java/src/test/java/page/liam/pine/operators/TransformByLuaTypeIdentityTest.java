package page.liam.pine.operators;

import org.junit.jupiter.api.Test;
import page.liam.pine.*;

import java.util.*;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Pins fromLua's type dispatch on the actual Lua type tag (issue #175).
 *
 * luaj's isnumber()/isstring() implement Lua coercion semantics, not type
 * identity: LuaString.isnumber() is true for any numeric-looking string and
 * LuaNumber.isstring() is true for every number. Dispatching on them routed
 * Lua strings through the number branch, losing type identity for every
 * numeric string and corrupting the value itself past 2^53 (todouble
 * round-trip). These tests assert on the returned Java class, which the
 * shared fixtures cannot do (their comparators stringify).
 */
public class TransformByLuaTypeIdentityTest {

    private static Map<String, Object> luaParams(String script) {
        Map<String, Object> params = new LinkedHashMap<>();
        params.put("lua_script", script);
        params.put("function_for_item", "f");
        params.put("function_for_common", "");
        return params;
    }

    private static Object runOnce(String script, Object itemValue) throws Exception {
        return runItems(script, List.of(itemValue)).get(0);
    }

    private static List<Object> runItems(String script, List<Object> itemValues) throws Exception {
        AllOperators.ensureRegistered();
        Operator op = Registry.global().buildOperator("transform_by_lua", luaParams(script));
        return runItems(op, itemValues);
    }

    private static List<Object> runItems(Operator op, List<Object> itemValues) throws Exception {
        if (op instanceof AbstractOperator a) {
            a.setMetadata(List.of(), List.of(), List.of("item_x"), List.of("item_y"));
        }
        List<Map<String, Object>> items = new ArrayList<>();
        for (Object v : itemValues) {
            Map<String, Object> row = new LinkedHashMap<>();
            row.put("item_x", v);
            items.add(row);
        }
        OperatorInput input = new OperatorInput(new LinkedHashMap<>(), items);
        OperatorOutput output = new OperatorOutput();
        op.execute(CancellationToken.create(), input, output);
        List<Object> out = new ArrayList<>();
        for (int i = 0; i < itemValues.size(); i++) {
            out.add(output.getItemWrites().get(i).get("item_y"));
        }
        return out;
    }

    @Test
    void nineteenDigitIdStringSurvivesByteExact() throws Exception {
        Object out = runOnce("function f() return \"1777288596209286259\" end", 1.0);
        assertInstanceOf(String.class, out);
        assertEquals("1777288596209286259", out);
    }

    @Test
    void stringJustPastDoubleMantissaKeepsExactValue() throws Exception {
        // 2^53 + 1 — the first integer a double cannot represent.
        Object out = runOnce("function f() return \"9007199254740993\" end", 1.0);
        assertInstanceOf(String.class, out);
        assertEquals("9007199254740993", out);
    }

    @Test
    void leadingZeroStringKeepsStringIdentity() throws Exception {
        Object out = runOnce("function f() return \"007\" end", 1.0);
        assertInstanceOf(String.class, out);
        assertEquals("007", out);
    }

    @Test
    void smallNumericStringStaysString() throws Exception {
        // Well inside double range — pure type-identity check, no precision
        // component. This is the case fixture comparators cannot see.
        Object out = runOnce("function f() return \"42\" end", 1.0);
        assertInstanceOf(String.class, out);
        assertEquals("42", out);
    }

    @Test
    void realNumbersStillTakeNumberBranch() throws Exception {
        // Lua numbers come back as Double, INCLUDING integral ones. This changed in
        // issues #189/#190: fromLua used to narrow an integral double to long, which
        // made pine-java the only runtime able to print a different SPELLING of the
        // same float64 (2^62 as 4611686018427387904 rather than Go's
        // 4611686018427388000). Go's pool_gopher_lua returns `float64(x)` for every
        // Lua number and has no integer branch at all, and pine-cpp uses
        // lua_tonumber, so Long was never a cross-runtime contract — it was an
        // internal detail these assertions had frozen.
        //
        // What IS the contract is the serialized form, and it is unchanged below
        // 2^53: GoFormat.formatJsonNumber prints an integral double with no decimal
        // point, so 42.0 still serializes as `42`. Asserted here on the value rather
        // than the box type.
        Object intOut = runOnce("function f() return 42 end", 1.0);
        assertInstanceOf(Double.class, intOut);
        assertEquals(42.0, intOut);
        assertEquals("42", page.liam.pine.GoFormat.formatJsonNumber((Double) intOut));

        Object floatOut = runOnce("function f() return 2.5 end", 1.0);
        assertInstanceOf(Double.class, floatOut);
        assertEquals(2.5, floatOut);
    }

    @Test
    void integralDoubleAbove2Pow53KeepsGoSpelling() throws Exception {
        // The regression from issues #189 (nightly fuzz, seed 1655185644 round 7005)
        // and #190. (-2147483648)^2 is exactly 2^62, so Lua produces an integral
        // value past the point where double<->long round-trips losslessly.
        Object out = runOnce("function f() return item_x * item_x end", -2147483648.0);
        assertInstanceOf(Double.class, out);
        assertEquals(Math.pow(2, 62), out);
        // The spelling, which is the part that actually diverged: Go's strconv
        // shortest round-trip, NOT the exact integer 4611686018427387904.
        assertEquals("4611686018427388000",
                page.liam.pine.GoFormat.formatJsonNumber((Double) out));
    }

    @Test
    void luaArithmeticCoercionProducesRealNumber() throws Exception {
        // "42" + 0 coerces to a Lua number inside the script — that value
        // genuinely IS a number and must keep taking the number branch.
        Object out = runOnce("function f() return \"42\" + 0 end", 1.0);
        assertInstanceOf(Double.class, out);
        assertEquals(42.0, out);
    }

    @Test
    void inputStringRoundTripsThroughLuaUnchanged() throws Exception {
        // toLua(String) -> LuaString -> fromLua must be the identity, the
        // "IDs collected in Lua and passed downstream" shape from #175.
        Object out = runOnce("function f() return item_x end", "1777288596209286259");
        assertInstanceOf(String.class, out);
        assertEquals("1777288596209286259", out);
    }

    @Test
    void numericStringAfterNumberInSameGlobalStaysString() throws Exception {
        // Issue #200 (nightly fuzz, seed 2262930939 round 362). The test above
        // passes with a single item and always did: the bug needs the global's
        // slot to already hold a NUMBER when the string is written. luaj 3.0.1's
        // LuaTable.NumberValueEntry.set reuses the slot via tonumber() — Lua
        // coercion — so "1777288596209286259" written over 7.0 was read back by
        // the script as the double 1777288596209286144. #175 audited this same
        // function for its dispatch predicates and could not see this: it is a
        // different dimension (slot state across items, not per-value dispatch).
        List<Object> out = runItems("function f() return item_x end",
                Arrays.asList(7.0, "1777288596209286259", "123", "1e5", "a", "456"));
        assertInstanceOf(Double.class, out.get(0));
        assertEquals(7.0, out.get(0));
        // Every numeric-looking string after the number must survive as a string.
        assertInstanceOf(String.class, out.get(1));
        assertEquals("1777288596209286259", out.get(1));
        assertInstanceOf(String.class, out.get(2));
        assertEquals("123", out.get(2));
        assertInstanceOf(String.class, out.get(3));
        assertEquals("1e5", out.get(3));
        assertInstanceOf(String.class, out.get(4));
        assertEquals("a", out.get(4));
        assertInstanceOf(String.class, out.get(5));
        assertEquals("456", out.get(5));
    }

    @Test
    void numberAfterStringAndStringAfterStringUnaffected() throws Exception {
        // The other transitions of the same slot: string→number must still
        // give a number (a real Lua number, not a string), and string→string
        // has no NumberValueEntry to trip on. Pins the guard to exactly the
        // number-slot → string-value edge.
        List<Object> out = runItems("function f() return item_x end",
                Arrays.asList("42", 42.0, "42", "43"));
        assertInstanceOf(String.class, out.get(0));
        assertInstanceOf(Double.class, out.get(1));
        assertEquals(42.0, out.get(1));
        assertInstanceOf(String.class, out.get(2));
        assertEquals("42", out.get(2));
        assertInstanceOf(String.class, out.get(3));
        assertEquals("43", out.get(3));
    }

    @Test
    void numericStringOverNumericBaselineGlobalAcrossPooledExecutes() throws Exception {
        // Same slot bug where the number was put there by the SCRIPT, not by a
        // previous item: a top-level `item_x = 7` makes item_x a baseline
        // global holding a number, and resetToBaseline restores that number
        // after every execute. So the very first item of every request writes a
        // string over a number slot — including on a pooled Globals that a
        // previous request already used.
        AllOperators.ensureRegistered();
        Operator op = Registry.global().buildOperator("transform_by_lua",
                luaParams("item_x = 7\nfunction f() return item_x end"));
        for (int request = 0; request < 2; request++) {
            List<Object> out = runItems(op, List.of("1777288596209286259"));
            assertInstanceOf(String.class, out.get(0), "request " + request);
            assertEquals("1777288596209286259", out.get(0), "request " + request);
        }
    }

    @Test
    void tableMixesStringsAndNumbersWithoutCrossContamination() throws Exception {
        Object out = runOnce(
                "function f() return {\"1777288596209286259\", 42, \"007\"} end", 1.0);
        assertInstanceOf(List.class, out);
        List<?> arr = (List<?>) out;
        assertEquals(3, arr.size());
        assertEquals("1777288596209286259", arr.get(0));
        assertInstanceOf(String.class, arr.get(0));
        assertEquals(42.0, arr.get(1));
        // Double, not Long: fromLua no longer narrows integral values (#189/#190).
        // The point of this case is that the numeric STRINGS either side keep string
        // identity while the real number keeps number identity — the box type of the
        // number was never the contract.
        assertInstanceOf(Double.class, arr.get(1));
        assertEquals("007", arr.get(2));
        assertInstanceOf(String.class, arr.get(2));
    }
}
