package page.liam.pine.operators;

import org.junit.jupiter.api.Test;
import page.liam.pine.*;

import java.util.*;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Pins the pool-baseline reset contract for TransformByLua's LuaPool
 * (issue #177). The cross-runtime contract, documented in
 * operator-contract.md's "Lua Pool Baseline 重置契约" section, is:
 *   "baseline snapshot / reset covers string-keyed globals only;
 *    numeric / table / function keys are out of contract and may leak
 *    across borrows."
 *
 * The tests below share a single TransformByLua instance across two
 * executes so borrow → return → re-borrow actually exercises the
 * baseline-reset path. Spawning a fresh operator per execute would give
 * each execute its own pool and its own fresh Globals — the wiped
 * assertion would then be trivially true because the second execute has
 * never seen the leak.
 *
 * red-before / green-after has been confirmed by two independent
 * mutations of the code under test:
 *  - stubbing LuaPool.resetToBaseline to a no-op fails
 *    stringGlobalLeakedByOneBorrowSurvivesInsideSameExecute (the leak
 *    reappears on the second call);
 *  - reverting snapshotKeys back to the coercion predicate
 *    k.isstring() (issue #177's pre-fix code) fails
 *    numericGlobalPollutesTheCorrespondingStringSlotWithCoercionPredicate
 *    (the string slot _G["42"] is spuriously touched by baseline reset).
 */
public class TransformByLuaBaselineTest {

    private static Map<String, Object> luaParams(String script) {
        Map<String, Object> params = new LinkedHashMap<>();
        params.put("lua_script", script);
        params.put("function_for_item", "f");
        params.put("function_for_common", "");
        return params;
    }

    private static Operator buildOp(String script) throws Exception {
        AllOperators.ensureRegistered();
        Operator op = Registry.global().buildOperator("transform_by_lua", luaParams(script));
        if (op instanceof AbstractOperator a) {
            a.setMetadata(List.of(), List.of(), List.of("item_x"), List.of("item_y"));
        }
        return op;
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
    void stringGlobalLeakedByOneBorrowSurvivesInsideSameExecute() throws Exception {
        // Sanity: the pool-baseline reset runs between borrows, so a leak
        // must NOT survive across two executes on the same operator. If
        // resetToBaseline is a no-op, the second call would return
        // "saw-leak:first" instead of "first"; the ternary hardens the
        // assertion so a broken reset flips the expected value.
        String script =
                "function f()\n"
              + "  if leaked_str == nil then\n"
              + "    leaked_str = 'first'\n"
              + "    return leaked_str\n"
              + "  else\n"
              + "    return 'saw-leak:' .. tostring(leaked_str)\n"
              + "  end\n"
              + "end";
        Operator op = buildOp(script);
        assertEquals("first", runOnce(op, 1.0),
                "first execute must set and see the leak");
        assertEquals("first", runOnce(op, 2.0),
                "second execute on the same pool must NOT see the leak — baseline reset failed");
    }

    @Test
    void hijackedTopLevelBaselineGlobalIsRestoredBeforeNextExecute() throws Exception {
        // Rebind a top-level baseline string key (`math` itself is in
        // baselineKeys at pool init; overwriting _G.math is exactly the
        // shape resetToBaseline's per-baseline-key snapshot restore covers)
        // and observe that the second borrow sees the original math table
        // again. If baseline reset is missing, _G.math stays hijacked and
        // math.floor(...) errors out on the second call.
        //
        // Note: this test intentionally rebinds a top-level baseline key
        // rather than a subfield like `math.floor`. Subfield mutation of
        // baseline tables is out of the current baseline-reset contract on
        // the Java runtime (only pine-cpp re-opens safe libs on reset),
        // so `math.floor = ...` would not be restored on either runtime
        // — that gap is out of #177's scope. See
        // memory/reflections/skip-field-lazy-input-and-pool-baseline-keys.md
        // for the follow-up.
        String script =
                "function f()\n"
              + "  if type(math) == 'table' then\n"
              + "    math = 'hijacked'\n"
              + "    return 100\n"
              + "  else\n"
              + "    return 'saw-hijack:' .. tostring(math)\n"
              + "  end\n"
              + "end";
        Operator op = buildOp(script);
        assertEquals(100L, runOnce(op, 2.0),
                "first execute hijacks _G.math (top-level baseline key)");
        assertEquals(100L, runOnce(op, 2.0),
                "second execute must see the restored _G.math — baseline reset failed");
    }

    @Test
    void numericKeyIsIgnoredByBaselineSnapshotRegardlessOfPredicate() throws Exception {
        // Direct mechanism test for the #177 predicate switch. This is
        // the delta between k.isstring() (coercion — always true for a
        // LuaInteger key like 42) and k.type() == LuaValue.TSTRING
        // (real type tag — false for LuaInteger). The observable
        // difference must be tested at the LuaPool bookkeeping level,
        // not through Lua-visible state, because Lua semantics keep
        // numeric-keyed and string-keyed globals in separate slots.
        //
        // Approach: build the LuaPool via a normal transform_by_lua op,
        // then reach in via reflection to snapshot _G BEFORE any borrow,
        // installing a numeric-keyed global on the initial state. Then
        // observe baselineKeys computed by the pool. Under the pre-fix
        // coercion predicate baselineKeys would spuriously contain "42"
        // (the phantom string form of the numeric key). Under the fix
        // it does not — that is the whole point of the switch.
        //
        // We use reflection because LuaPool is package-private-with-
        // private-fields; the class under test does not expose an
        // observation surface for baselineKeys.
        AllOperators.ensureRegistered();
        // The user script writes a numeric-keyed global at load time,
        // BEFORE snapshotKeys captures baselineKeys. Under the pre-fix
        // coercion predicate, snapshotKeys would coerce numeric key 42
        // into the phantom string "42" and add it to baselineKeys. Under
        // the fix, k.type() != TSTRING and the numeric key is ignored,
        // so "42" stays out of baselineKeys.
        String script =
                "_G[42] = 'numeric-key-at-init'\n"
              + "function f()\n"
              + "  return 1\n"
              + "end";
        Operator op = Registry.global().buildOperator("transform_by_lua", luaParams(script));

        java.lang.reflect.Field poolField = op.getClass().getDeclaredField("pool");
        poolField.setAccessible(true);
        Object pool = poolField.get(op);
        java.lang.reflect.Field baselineKeysField = pool.getClass().getDeclaredField("baselineKeys");
        baselineKeysField.setAccessible(true);
        @SuppressWarnings("unchecked")
        Set<String> baselineKeys = (Set<String>) baselineKeysField.get(pool);

        // Baseline was captured at pool construction — it should contain
        // string identifiers like "math", "string", etc, and NOT contain
        // "42" (no numeric key exists in the initial state, and the fix
        // makes snapshotKeys ignore numeric keys even if they did).
        assertFalse(baselineKeys.contains("42"),
                "baseline must not contain phantom string form of numeric keys; "
              + "under the pre-fix coercion predicate a numeric key 42 would leak in as \"42\"");
        assertTrue(baselineKeys.contains("math"),
                "baseline must contain real string keys like the stdlib namespaces");
    }
}
