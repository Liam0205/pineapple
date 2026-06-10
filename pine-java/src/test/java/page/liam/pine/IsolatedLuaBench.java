package page.liam.pine;

import page.liam.pine.operators.AllOperators;

import java.util.*;

/**
 * Isolated Lua-vs-native operator benchmark. Mirrors pine-go
 * benchmarks/bench_isolated_test.go: build the operator directly from the
 * registry, loop execute() on a fixed materialized OperatorInput, and report
 * ns/op — bypassing the engine/DAG/HTTP layers entirely so the LuaJ-vs-native
 * gap is not diluted by framework overhead (see
 * llmdoc/memory/reflections/bench-lua-vs-go-performance.md for why
 * end-to-end numbers understate the gap by 50-70%).
 *
 * <p>Not a JUnit test: it has a main() and is excluded from surefire runs.
 * Run manually when re-measuring:
 *
 * <pre>
 * cd pine-java && mvn -q test-compile
 * CP="target/classes:target/test-classes:$(mvn dependency:build-classpath -B -q -Dmdep.outputFile=/dev/stdout | tail -1)"
 * java -cp "$CP" page.liam.pine.IsolatedLuaBench
 * </pre>
 *
 * Latest archived results:
 * .code-review/sharedmutex-deep-dive/lua-vs-native-three-runtimes.md
 */
public class IsolatedLuaBench {
    public static void main(String[] args) throws Exception {
        AllOperators.ensureRegistered();
        CancellationToken token = CancellationToken.create();

        System.out.printf("%-16s %-7s %-14s %-14s %s%n", "case", "items", "native ns/op", "lua ns/op", "lua/native");
        System.out.println("-".repeat(64));

        for (String[] tc : CASES) {
            String name = tc[0], luaScript = tc[1], nativeType = tc[2];
            for (int n : new int[]{100, 1000}) {
                List<Map<String, Object>> items = genItems(name, n);
                OperatorInput input = new OperatorInput(new LinkedHashMap<>(), items);

                Operator nativeOp = buildNative(nativeType, name);
                Operator luaOp = buildLua(luaScript, name);

                int iters = n >= 1000 ? 2000 : 10000;
                double nativeNs = bench(nativeOp, token, input, iters);
                double luaNs = bench(luaOp, token, input, iters);

                System.out.printf("%-16s %-7d %-14.0f %-14.0f %.2fx%n", name, n, nativeNs, luaNs, luaNs / nativeNs);
            }
        }
    }

    static final String[][] CASES = {
        {"L1_identity", "function f() return item_x end", "transform_copy"},
        {"L2_arithmetic", "function f() return item_price * 0.85 + 10.0 end", "transform_normalize"},
        {"L5_iterative", "function f()\n  local x = item_price\n  local acc = 1.0\n  for i = 1, 5 do acc = acc * x + i end\n  return acc\nend", "transform_normalize"},
    };

    static List<Map<String, Object>> genItems(String caseName, int n) {
        List<Map<String, Object>> items = new ArrayList<>(n);
        for (int i = 0; i < n; i++) {
            Map<String, Object> row = new LinkedHashMap<>();
            if (caseName.equals("L1_identity")) {
                row.put("item_x", (double) i);
            } else {
                row.put("item_price", (double) (100 + i));
            }
            items.add(row);
        }
        return items;
    }

    static Operator buildNative(String type, String caseName) throws Exception {
        Map<String, Object> params = new LinkedHashMap<>();
        if (type.equals("transform_copy")) {
            params.put("direction", "item_to_item");
        } else if (type.equals("transform_normalize")) {
            params.put("method", "min_max");
        }
        Operator op = Registry.global().buildOperator(type, params);
        setMeta(op, caseName);
        return op;
    }

    static Operator buildLua(String script, String caseName) throws Exception {
        Map<String, Object> params = new LinkedHashMap<>();
        params.put("lua_script", script);
        params.put("function_for_item", "f");
        params.put("function_for_common", "");
        Operator op = Registry.global().buildOperator("transform_by_lua", params);
        setMeta(op, caseName);
        return op;
    }

    static void setMeta(Operator op, String caseName) {
        List<String> in = caseName.equals("L1_identity") ? List.of("item_x") : List.of("item_price");
        List<String> out = caseName.equals("L1_identity") ? List.of("item_y") : List.of("item_result");
        if (op instanceof AbstractOperator a) {
            a.setMetadata(List.of(), List.of(), in, out);
        }
    }

    static double bench(Operator op, CancellationToken token, OperatorInput input, int iters) throws Exception {
        // Warmup also lets C2 compile the hot path before timing starts.
        for (int i = 0; i < Math.max(200, iters / 10); i++) {
            op.execute(token, input, new OperatorOutput());
        }
        long t0 = System.nanoTime();
        for (int i = 0; i < iters; i++) {
            op.execute(token, input, new OperatorOutput());
        }
        long t1 = System.nanoTime();
        return (double) (t1 - t0) / iters;
    }
}
