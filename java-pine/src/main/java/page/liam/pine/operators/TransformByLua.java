package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.OperatorInput;
import page.liam.pine.OperatorOutput;
import org.luaj.vm2.*;
import org.luaj.vm2.lib.jse.JsePlatform;

import java.util.Map;

public class TransformByLua extends AbstractOperator {
    private String script;
    private String funcName;
    private boolean isItemMode;

    @Override
    public void init(Map<String, Object> params) throws Exception {
        script = (String) params.get("lua_script");
        String funcForItem = (String) params.getOrDefault("function_for_item", "");
        String funcForCommon = (String) params.getOrDefault("function_for_common", "");

        if (funcForItem.isEmpty() && funcForCommon.isEmpty()) {
            throw new IllegalArgumentException("lua: exactly one of function_for_item or function_for_common must be set");
        }
        if (!funcForItem.isEmpty() && !funcForCommon.isEmpty()) {
            throw new IllegalArgumentException("lua: cannot set both function_for_item and function_for_common");
        }

        if (!funcForItem.isEmpty()) {
            funcName = funcForItem;
            isItemMode = true;
        } else {
            funcName = funcForCommon;
            isItemMode = false;
        }

        // Validate script compiles
        Globals globals = JsePlatform.standardGlobals();
        globals.load(script).call();
    }

    @Override
    public void execute(OperatorInput input, OperatorOutput output) throws Exception {
        Globals globals = JsePlatform.standardGlobals();
        globals.load(script).call();

        if (isItemMode) {
            executeForItem(globals, input, output);
        } else {
            executeForCommon(globals, input, output);
        }
    }

    private void executeForItem(Globals globals, OperatorInput input, OperatorOutput output) throws Exception {
        // Set common globals once
        for (String field : commonInput) {
            globals.set(field, toLua(input.common(field)));
        }

        LuaValue fn = globals.get(funcName);
        if (fn.isnil()) {
            throw new Exception("lua: function \"" + funcName + "\" not found");
        }

        int nret = itemOutput.size();
        int n = input.itemCount();

        for (int i = 0; i < n; i++) {
            // Set item globals for this item
            for (String field : itemInput) {
                globals.set(field, toLua(input.item(i, field)));
            }

            Varargs results = fn.invoke(LuaValue.NONE);

            for (int j = 0; j < nret; j++) {
                Object val = toJava(results.arg(j + 1));
                output.setItem(i, itemOutput.get(j), val);
            }
        }
    }

    private void executeForCommon(Globals globals, OperatorInput input, OperatorOutput output) throws Exception {
        // Set common globals as scalars
        for (String field : commonInput) {
            globals.set(field, toLua(input.common(field)));
        }

        // Set item fields as 1-indexed Lua tables
        int n = input.itemCount();
        for (String field : itemInput) {
            LuaTable tbl = new LuaTable();
            for (int i = 0; i < n; i++) {
                tbl.set(i + 1, toLua(input.item(i, field)));
            }
            globals.set(field, tbl);
        }

        LuaValue fn = globals.get(funcName);
        if (fn.isnil()) {
            throw new Exception("lua: function \"" + funcName + "\" not found");
        }

        int nret = commonOutput.size();
        Varargs results = fn.invoke(LuaValue.NONE);

        for (int j = 0; j < nret; j++) {
            Object val = toJava(results.arg(j + 1));
            output.setCommon(commonOutput.get(j), val);
        }
    }

    private static LuaValue toLua(Object v) {
        if (v == null) return LuaValue.NIL;
        if (v instanceof Boolean) return LuaValue.valueOf((Boolean) v);
        if (v instanceof Number) return LuaValue.valueOf(((Number) v).doubleValue());
        if (v instanceof String) return LuaValue.valueOf((String) v);
        return LuaValue.valueOf(String.valueOf(v));
    }

    private static Object toJava(LuaValue v) {
        if (v.isnil()) return null;
        if (v.isboolean()) return v.toboolean();
        if (v.isnumber()) return v.todouble();
        if (v.isstring()) return v.tojstring();
        return v.tojstring();
    }
}
