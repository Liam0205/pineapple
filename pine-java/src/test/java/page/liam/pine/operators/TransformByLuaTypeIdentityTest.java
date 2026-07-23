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
        AllOperators.ensureRegistered();
        Operator op = Registry.global().buildOperator("transform_by_lua", luaParams(script));
        if (op instanceof AbstractOperator a) {
            a.setMetadata(List.of(), List.of(), List.of("item_x"), List.of("item_y"));
        }
        List<Map<String, Object>> items = new ArrayList<>();
        Map<String, Object> row = new LinkedHashMap<>();
        row.put("item_x", itemValue);
        items.add(row);
        OperatorInput input = new OperatorInput(new LinkedHashMap<>(), items);
        OperatorOutput output = new OperatorOutput();
        op.execute(CancellationToken.create(), input, output);
        return output.getItemWrites().get(0).get("item_y");
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
        Object intOut = runOnce("function f() return 42 end", 1.0);
        assertInstanceOf(Long.class, intOut);
        assertEquals(42L, intOut);

        Object floatOut = runOnce("function f() return 2.5 end", 1.0);
        assertInstanceOf(Double.class, floatOut);
        assertEquals(2.5, floatOut);
    }

    @Test
    void luaArithmeticCoercionProducesRealNumber() throws Exception {
        // "42" + 0 coerces to a Lua number inside the script — that value
        // genuinely IS a number and must keep taking the number branch.
        Object out = runOnce("function f() return \"42\" + 0 end", 1.0);
        assertInstanceOf(Long.class, out);
        assertEquals(42L, out);
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
    void tableMixesStringsAndNumbersWithoutCrossContamination() throws Exception {
        Object out = runOnce(
                "function f() return {\"1777288596209286259\", 42, \"007\"} end", 1.0);
        assertInstanceOf(List.class, out);
        List<?> arr = (List<?>) out;
        assertEquals(3, arr.size());
        assertEquals("1777288596209286259", arr.get(0));
        assertInstanceOf(String.class, arr.get(0));
        assertEquals(42L, arr.get(1));
        assertInstanceOf(Long.class, arr.get(1));
        assertEquals("007", arr.get(2));
        assertInstanceOf(String.class, arr.get(2));
    }
}
