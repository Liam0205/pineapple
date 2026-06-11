package page.liam.pine.operators;

import org.junit.jupiter.api.Test;
import page.liam.pine.*;

import java.util.*;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Pins the luajc-vs-luac compiler backend selection in TransformByLua:
 * the default is luajc (Lua → JVM bytecode via BCEL), scripts that luajc
 * cannot compile fall back to luac transparently, and the system property
 * pine.lua.compiler=luac forces the interpreter path. All three paths must
 * produce identical operator output — the backend is an execution-strategy
 * detail, never an observable semantic.
 */
public class TransformByLuaCompilerBackendTest {

    private static Map<String, Object> luaParams(String script) {
        Map<String, Object> params = new LinkedHashMap<>();
        params.put("lua_script", script);
        params.put("function_for_item", "f");
        params.put("function_for_common", "");
        return params;
    }

    private static Object runOnce(String script, double itemValue) throws Exception {
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
    void luajcDefaultProducesSameResultAsLuacForced() throws Exception {
        String script = "function f()\n  local acc = 1.0\n  for i = 1, 5 do acc = acc * item_x + i end\n  return acc\nend";

        String saved = System.getProperty(TransformByLua.COMPILER_PROP);
        try {
            System.clearProperty(TransformByLua.COMPILER_PROP);  // default = luajc
            Object viaLuajc = runOnce(script, 2.0);

            System.setProperty(TransformByLua.COMPILER_PROP, "luac");
            Object viaLuac = runOnce(script, 2.0);

            assertEquals(viaLuac, viaLuajc, "luajc and luac must produce identical results");
        } finally {
            if (saved == null) System.clearProperty(TransformByLua.COMPILER_PROP);
            else System.setProperty(TransformByLua.COMPILER_PROP, saved);
        }
    }

    @Test
    void invalidScriptStillFailsCleanlyUnderLuajc() {
        String saved = System.getProperty(TransformByLua.COMPILER_PROP);
        try {
            System.clearProperty(TransformByLua.COMPILER_PROP);
            AllOperators.ensureRegistered();
            // Missing function: semantic validation error must surface with the
            // same message regardless of backend (luajc probe throws
            // IllegalArgumentException which must NOT be swallowed by fallback).
            Exception e = assertThrows(Exception.class,
                () -> Registry.global().buildOperator("transform_by_lua",
                        luaParams("function not_f() return 1 end")));
            assertTrue(e.getMessage().contains("does not define function"),
                "unexpected message: " + e.getMessage());
        } finally {
            if (saved == null) System.clearProperty(TransformByLua.COMPILER_PROP);
            else System.setProperty(TransformByLua.COMPILER_PROP, saved);
        }
    }

    @Test
    void luacForcedSwitchWorks() throws Exception {
        String saved = System.getProperty(TransformByLua.COMPILER_PROP);
        try {
            System.setProperty(TransformByLua.COMPILER_PROP, "luac");
            Object result = runOnce("function f() return item_x * 2 end", 21.0);
            assertEquals(42L, result);
        } finally {
            if (saved == null) System.clearProperty(TransformByLua.COMPILER_PROP);
            else System.setProperty(TransformByLua.COMPILER_PROP, saved);
        }
    }
}
