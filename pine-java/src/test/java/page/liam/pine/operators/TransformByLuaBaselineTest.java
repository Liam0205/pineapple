package page.liam.pine.operators;

import org.junit.jupiter.api.Test;
import page.liam.pine.*;

import java.util.*;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Pins the pool-baseline reset contract for TransformByLua's Lua pool
 * (issue #177). The cross-runtime contract, documented in
 * operator-contract.md's Lua Bridge section, is:
 *   "baseline snapshot / reset covers string-keyed globals only;
 *    numeric / table / function keys are out of contract and may leak
 *    across borrows."
 *
 * This test ensures the Java implementation actually matches that
 * contract: string-keyed globals written by one borrow are wiped, and
 * script-visible state is otherwise clean at the next borrow — under a
 * pool that recycles the same state (borrow twice, same script, no
 * concurrency, ordered returns force the second borrow to reuse the
 * first state).
 *
 * The narrow point of the #177 fix (snapshotKeys switching from
 * k.isstring() to k.type() == TSTRING) is behavior-equivalent on the
 * happy path because normal operator paths never write numeric-keyed
 * globals; this test pins the happy path so a future regression that
 * flips the predicate back would still show up if it broke reuse.
 */
public class TransformByLuaBaselineTest {

    private static Map<String, Object> luaParams(String script) {
        Map<String, Object> params = new LinkedHashMap<>();
        params.put("lua_script", script);
        params.put("function_for_item", "f");
        params.put("function_for_common", "");
        return params;
    }

    private static Object runOnce(Operator op, Object itemValue) throws Exception {
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
    void stringGlobalLeakedByOneBorrowIsWipedBeforeNextBorrow() throws Exception {
        AllOperators.ensureRegistered();
        // First borrow leaks a string global; second borrow reads it —
        // if the baseline reset misses the leak, the second call would
        // return the leaked value instead of nil (Lua sees undeclared
        // globals as nil).
        String leakScript =
                "function f()\n"
              + "  leaked_str = 'from-first-borrow'\n"
              + "  return leaked_str\n"
              + "end";
        Operator leaker = Registry.global().buildOperator("transform_by_lua", luaParams(leakScript));
        if (leaker instanceof AbstractOperator a) {
            a.setMetadata(List.of(), List.of(), List.of("item_x"), List.of("item_y"));
        }
        Object leakedOut = runOnce(leaker, 1.0);
        assertEquals("from-first-borrow", leakedOut);

        String readerScript =
                "function f()\n"
              + "  if leaked_str == nil then return 'wiped' else return leaked_str end\n"
              + "end";
        Operator reader = Registry.global().buildOperator("transform_by_lua", luaParams(readerScript));
        if (reader instanceof AbstractOperator a) {
            a.setMetadata(List.of(), List.of(), List.of("item_x"), List.of("item_y"));
        }
        Object seen = runOnce(reader, 1.0);
        assertEquals("wiped", seen,
                "string-keyed globals leaked by one borrow must be reset before the next borrow");
    }

    @Test
    void baselineStringsSurviveReset() throws Exception {
        // Standard library globals (math, string, table, ...) are string
        // keys captured into the baseline at pool init; they must remain
        // available after the reset that runs between borrows.
        AllOperators.ensureRegistered();
        String script =
                "function f()\n"
              + "  return math.floor(item_x * 3.5)\n"
              + "end";
        Operator op = Registry.global().buildOperator("transform_by_lua", luaParams(script));
        if (op instanceof AbstractOperator a) {
            a.setMetadata(List.of(), List.of(), List.of("item_x"), List.of("item_y"));
        }
        // Two borrows in sequence — reuse path must keep math.floor
        // reachable both times.
        Object first = runOnce(op, 2.0);
        Object second = runOnce(op, 4.0);
        assertEquals(7L, first);
        assertEquals(14L, second);
    }
}
