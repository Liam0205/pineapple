package page.liam.pine.operators;

import page.liam.pine.*;
import page.liam.pine.metrics.Counter;
import page.liam.pine.metrics.Gauge;
import org.luaj.vm2.*;
import org.luaj.vm2.lib.*;
import org.luaj.vm2.lib.jse.JsePlatform;
import org.luaj.vm2.compiler.LuaC;

import java.util.Map;
import java.util.concurrent.ConcurrentLinkedQueue;
import java.util.concurrent.atomic.AtomicLong;

public class TransformByLua extends AbstractOperator implements ConcurrentSafe, StatsProvider {
    private String script;
    private String funcName;
    private boolean isItemMode;
    private LuaPool pool;

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

        // Validate script compiles and defines the function
        Globals g = createSandboxedGlobals();
        g.load(script).call();
        if (g.get(funcName).isnil()) {
            throw new IllegalArgumentException("lua: script does not define function \"" + funcName + "\"");
        }

        pool = new LuaPool(script);
    }

    @Override
    public void execute(OperatorInput input, OperatorOutput output) throws Exception {
        Globals globals = pool.borrow();
        java.util.Set<String> usedKeys = new java.util.HashSet<>();
        try {
            if (isItemMode) {
                executeForItem(globals, input, output, usedKeys);
            } else {
                executeForCommon(globals, input, output, usedKeys);
            }
        } finally {
            pool.returnState(globals, usedKeys);
        }
    }

    @Override
    public Map<String, Long> operatorStats() {
        if (pool == null) return null;
        return Map.of(
                "borrow_count", pool.borrowCount.get(),
                "return_count", pool.returnCount.get(),
                "create_count", pool.createCount.get(),
                "active_count", pool.activeCount.get()
        );
    }

    private void executeForItem(Globals globals, OperatorInput input, OperatorOutput output, java.util.Set<String> usedKeys) throws Exception {
        for (String field : commonInput) {
            globals.set(field, toLua(input.common(field)));
            usedKeys.add(field);
        }

        LuaValue fn = globals.get(funcName);
        if (fn.isnil()) {
            throw new Exception("lua: function \"" + funcName + "\" not found");
        }

        int nret = itemOutput.size();
        int n = input.itemCount();

        for (int i = 0; i < n; i++) {
            for (String field : itemInput) {
                globals.set(field, toLua(input.item(i, field)));
                usedKeys.add(field);
            }
            Varargs results = fn.invoke(LuaValue.NONE);
            for (int j = 0; j < nret; j++) {
                Object val = toJava(results.arg(j + 1));
                output.setItem(i, itemOutput.get(j), val);
            }
        }
    }

    private void executeForCommon(Globals globals, OperatorInput input, OperatorOutput output, java.util.Set<String> usedKeys) throws Exception {
        for (String field : commonInput) {
            globals.set(field, toLua(input.common(field)));
            usedKeys.add(field);
        }

        int n = input.itemCount();
        for (String field : itemInput) {
            LuaTable tbl = new LuaTable();
            for (int i = 0; i < n; i++) {
                tbl.set(i + 1, toLua(input.item(i, field)));
            }
            globals.set(field, tbl);
            usedKeys.add(field);
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

    private static Globals createSandboxedGlobals() {
        Globals globals = new Globals();
        globals.load(new org.luaj.vm2.lib.BaseLib());
        globals.load(new org.luaj.vm2.lib.PackageLib());
        globals.load(new org.luaj.vm2.lib.TableLib());
        globals.load(new org.luaj.vm2.lib.StringLib());
        globals.load(new org.luaj.vm2.lib.MathLib());
        LuaC.install(globals);
        globals.set("dofile", LuaValue.NIL);
        globals.set("loadfile", LuaValue.NIL);
        globals.set("require", LuaValue.NIL);
        globals.set("package", LuaValue.NIL);
        return globals;
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

    static class LuaPool {
        private final ConcurrentLinkedQueue<Globals> pool = new ConcurrentLinkedQueue<>();
        private final String initScript;
        private volatile boolean closed;
        final AtomicLong borrowCount = new AtomicLong();
        final AtomicLong returnCount = new AtomicLong();
        final AtomicLong createCount = new AtomicLong();
        final AtomicLong activeCount = new AtomicLong();

        LuaPool(String script) {
            this.initScript = script;
        }

        Globals borrow() {
            if (closed) throw new IllegalStateException("lua pool is closed");
            borrowCount.incrementAndGet();
            activeCount.incrementAndGet();
            Globals g = pool.poll();
            if (g == null) {
                createCount.incrementAndGet();
                g = createSandboxedGlobals();
                g.load(initScript).call();
            }
            return g;
        }

        void returnState(Globals g, java.util.Collection<String> usedKeys) {
            returnCount.incrementAndGet();
            activeCount.decrementAndGet();
            if (closed) return;
            for (String key : usedKeys) {
                g.set(key, LuaValue.NIL);
            }
            pool.offer(g);
        }

        void close() {
            closed = true;
            pool.clear();
        }
    }
}
