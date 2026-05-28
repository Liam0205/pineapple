package page.liam.pine.operators;

import page.liam.pine.*;
import page.liam.pine.metrics.Counter;
import page.liam.pine.metrics.Gauge;
import org.luaj.vm2.*;
import org.luaj.vm2.lib.*;
import org.luaj.vm2.lib.jse.JsePlatform;
import org.luaj.vm2.compiler.LuaC;

import java.util.*;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ConcurrentLinkedQueue;
import java.util.concurrent.atomic.AtomicLong;

/**
 * Operator: transform_by_lua
 * Metadata contract
 *   CommonInput:  [<common fields read as scalar globals>]
 *   CommonOutput: [<return values from function_for_common>]
 *   ItemInput:    [<item fields — scalars in item mode, lists in common mode>]
 *   ItemOutput:   [<return values from function_for_item>]
 */
public class TransformByLua extends AbstractOperator implements ConcurrentSafe, StatsProvider, DebugAware, MetricsAware {
    private String script;
    private String funcName;
    private boolean isItemMode;
    private LuaPool pool;
    private String operatorName;
    private boolean debug;

    @Override
    public void init(OperatorParams params) {
        script = params.getString("lua_script");
        String funcForItem = params.getString("function_for_item", "");
        String funcForCommon = params.getString("function_for_common", "");

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
    public void setDebug(String operatorName, boolean debug) {
        this.operatorName = operatorName;
        this.debug = debug;
    }

    @Override
    public boolean isDebug() {
        return debug;
    }

    @Override
    public void setMetricsProvider(page.liam.pine.metrics.Provider provider) {
        String name = this.operatorName != null ? this.operatorName : "unknown";
        pool.setMetrics(
            provider.newCounter(new page.liam.pine.metrics.MetricOpts(
                "pine_lua_pool_borrow_total", "Total Lua state borrows.", "operator")).with(name),
            provider.newCounter(new page.liam.pine.metrics.MetricOpts(
                "pine_lua_pool_return_total", "Total Lua state returns.", "operator")).with(name),
            provider.newCounter(new page.liam.pine.metrics.MetricOpts(
                "pine_lua_pool_create_total", "Total Lua states created.", "operator")).with(name),
            provider.newGauge(new page.liam.pine.metrics.MetricOpts(
                "pine_lua_pool_active", "Lua states currently borrowed.", "operator")).with(name)
        );
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) throws PineErrors.OperatorException {
        if (debug) {
            int fields = commonInput().size();
            int nonNil = 0;
            for (String f : commonInput()) {
                if (input.common(f) != null) nonNil++;
            }
            int itemCount = input.itemCount();
            String mode = isItemMode ? "item" : "common";
            System.err.printf("[pine:debug] operator=\"%s\" common_input fields=%d non_nil=%d items=%d mode=%s func=%s%n",
                operatorName, fields, nonNil, itemCount, mode, funcName);
        }
        Globals globals = pool.borrow();
        if (globals == null) {
            throw new PineErrors.OperatorException("lua: pool is closed");
        }
        try {
            if (isItemMode) {
                executeForItem(token, globals, input, output);
            } else {
                executeForCommon(token, globals, input, output);
            }
        } catch (LuaError e) {
            throw new PineErrors.OperatorException("lua: " + e.getMessage(), e);
        } catch (PineErrors.OperatorException e) {
            throw e;
        } catch (Exception e) {
            throw new PineErrors.OperatorException(e.getMessage(), e);
        } finally {
            pool.returnState(globals);
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

    private void executeForItem(CancellationToken token, Globals globals, OperatorInput input, OperatorOutput output) throws Exception {
        for (String field : commonInput()) {
            globals.set(field, toLua(input.common(field)));
        }

        LuaValue fn = globals.get(funcName);
        if (fn.isnil()) {
            throw new Exception("lua: function \"" + funcName + "\" not found");
        }

        int nret = itemOutput().size();
        int n = input.itemCount();

        for (int i = 0; i < n; i++) {
            if (token.isCancelled()) break;
            for (String field : itemInput()) {
                globals.set(field, toLua(input.item(i, field)));
            }
            Varargs results;
            try {
                results = fn.invoke(LuaValue.NONE);
            } catch (LuaError e) {
                throw new Exception("lua: item[" + i + "]: " + e.getMessage(), e);
            }
            for (int j = 0; j < nret; j++) {
                Object val = fromLua(results.arg(j + 1));
                output.setItem(i, itemOutput().get(j), val);
            }
        }
    }

    private void executeForCommon(CancellationToken token, Globals globals, OperatorInput input, OperatorOutput output) throws Exception {
        if (token.isCancelled()) return;

        for (String field : commonInput()) {
            globals.set(field, toLua(input.common(field)));
        }

        int n = input.itemCount();
        for (String field : itemInput()) {
            LuaTable tbl = new LuaTable();
            for (int i = 0; i < n; i++) {
                tbl.set(i + 1, toLua(input.item(i, field)));
            }
            globals.set(field, tbl);
        }

        if (token.isCancelled()) return;

        LuaValue fn = globals.get(funcName);
        if (fn.isnil()) {
            throw new Exception("lua: function \"" + funcName + "\" not found");
        }

        int nret = commonOutput().size();
        Varargs results = fn.invoke(LuaValue.NONE);
        for (int j = 0; j < nret; j++) {
            Object val = fromLua(results.arg(j + 1));
            output.setCommon(commonOutput().get(j), val);
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
        if (v instanceof List<?> list) {
            LuaTable tbl = new LuaTable();
            for (int i = 0; i < list.size(); i++) {
                tbl.set(i + 1, toLua(list.get(i)));
            }
            return tbl;
        }
        if (v instanceof Map<?, ?> map) {
            LuaTable tbl = new LuaTable();
            for (Map.Entry<?, ?> e : map.entrySet()) {
                tbl.set(String.valueOf(e.getKey()), toLua(e.getValue()));
            }
            return tbl;
        }
        return LuaValue.valueOf(String.valueOf(v));
    }

    private static Object fromLua(LuaValue v) throws PineErrors.OperatorException {
        if (v.isnil()) return null;
        if (v.isboolean()) return v.toboolean();
        if (v.isnumber()) {
            double d = v.todouble();
            if (d == Math.floor(d) && !Double.isInfinite(d) && d >= Long.MIN_VALUE && d <= Long.MAX_VALUE) {
                return (long) d;
            }
            return d;
        }
        if (v.isstring()) return v.tojstring();
        if (v.istable()) {
            LuaTable tbl = v.checktable();
            int len = tbl.length();
            if (len > 0) {
                List<Object> arr = new ArrayList<>(len);
                for (int i = 1; i <= len; i++) {
                    arr.add(fromLua(tbl.get(i)));
                }
                return arr;
            }
            Map<String, Object> map = new LinkedHashMap<>();
            LuaValue k = LuaValue.NIL;
            while (true) {
                Varargs n = tbl.next(k);
                if ((k = n.arg1()).isnil()) break;
                if (k.type() != LuaValue.TSTRING) {
                    throw new PineErrors.OperatorException(
                            "lua: table has non-string key of type \"" + k.typename() + "\"");
                }
                map.put(k.tojstring(), fromLua(n.arg(2)));
            }
            // Lua empty table → empty array (cross-runtime convention)
            if (map.isEmpty()) return new ArrayList<>();
            return map;
        }
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

        private Counter mBorrow, mReturn, mCreate;
        private Gauge mActive;

        private final Set<String> baselineKeys;
        private final ConcurrentHashMap<Globals, Map<String, LuaValue>> snapshots = new ConcurrentHashMap<>();

        LuaPool(String script) {
            this.initScript = script;
            // Build baseline key set from a fresh sandboxed globals after loading script
            Globals g = createSandboxedGlobals();
            g.load(initScript).call();
            baselineKeys = snapshotKeys(g);
            pool.offer(g);
            createCount.incrementAndGet();
        }

        void setMetrics(Counter borrow, Counter ret, Counter create, Gauge active) {
            this.mBorrow = borrow;
            this.mReturn = ret;
            this.mCreate = create;
            this.mActive = active;
        }

        Globals borrow() {
            if (closed) return null;
            borrowCount.incrementAndGet();
            activeCount.incrementAndGet();
            if (mBorrow != null) mBorrow.inc();
            if (mActive != null) mActive.add(1);
            Globals g = pool.poll();
            if (g == null) {
                createCount.incrementAndGet();
                if (mCreate != null) mCreate.inc();
                g = createSandboxedGlobals();
                g.load(initScript).call();
            }
            snapshots.put(g, snapshotBaselineValues(g));
            return g;
        }

        void returnState(Globals g) {
            returnCount.incrementAndGet();
            activeCount.decrementAndGet();
            if (mReturn != null) mReturn.inc();
            if (mActive != null) mActive.add(-1);
            if (closed) return;
            Map<String, LuaValue> snap = snapshots.remove(g);
            resetToBaseline(g, snap);
            pool.offer(g);
        }

        void close() {
            closed = true;
            pool.clear();
        }

        private Set<String> snapshotKeys(Globals g) {
            Set<String> keys = new HashSet<>();
            LuaValue k = LuaValue.NIL;
            while (true) {
                Varargs n = g.next(k);
                if ((k = n.arg1()).isnil()) break;
                if (k.isstring()) keys.add(k.tojstring());
            }
            return keys;
        }

        private Map<String, LuaValue> snapshotBaselineValues(Globals g) {
            Map<String, LuaValue> snap = new HashMap<>();
            for (String k : baselineKeys) {
                snap.put(k, g.get(k));
            }
            return snap;
        }

        private void resetToBaseline(Globals g, Map<String, LuaValue> snap) {
            if (snap == null) return;
            // Remove non-baseline keys
            Set<String> currentKeys = snapshotKeys(g);
            for (String k : currentKeys) {
                if (!baselineKeys.contains(k)) {
                    g.set(k, LuaValue.NIL);
                }
            }
            // Restore modified baseline keys
            for (Map.Entry<String, LuaValue> e : snap.entrySet()) {
                g.set(e.getKey(), e.getValue());
            }
        }
    }
}
