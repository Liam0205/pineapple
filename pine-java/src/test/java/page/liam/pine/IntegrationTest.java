package page.liam.pine;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;

import java.util.*;

import static org.junit.jupiter.api.Assertions.*;

public class IntegrationTest {
    private static final ObjectMapper mapper = new ObjectMapper();

    private byte[] buildConfig(Map<String, Object> operators, List<String> sequence,
                               Map<String, Object> flowContract) throws Exception {
        return buildConfig(operators, sequence, flowContract, false);
    }

    private byte[] buildConfigWithDebug(Map<String, Object> operators, List<String> sequence,
                                        Map<String, Object> flowContract) throws Exception {
        return buildConfig(operators, sequence, flowContract, true);
    }

    private byte[] buildConfig(Map<String, Object> operators, List<String> sequence,
                               Map<String, Object> flowContract, boolean debug) throws Exception {
        Map<String, Object> pipelineConfig = new LinkedHashMap<>();
        pipelineConfig.put("operators", operators);

        Map<String, Object> mainGroup = new LinkedHashMap<>();
        mainGroup.put("pipeline", sequence);

        Map<String, Object> pipelineGroup = new LinkedHashMap<>();
        pipelineGroup.put("main", mainGroup);

        Map<String, Object> config = new LinkedHashMap<>();
        config.put("pipeline_config", pipelineConfig);
        config.put("pipeline_group", pipelineGroup);
        config.put("flow_contract", flowContract);
        if (debug) {
            config.put("debug", true);
        }
        return mapper.writeValueAsBytes(config);
    }

    private Map<String, Object> metadata(List<String> commonIn, List<String> commonOut,
                                         List<String> itemIn, List<String> itemOut) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("common_input", commonIn);
        m.put("common_output", commonOut);
        m.put("item_input", itemIn);
        m.put("item_output", itemOut);
        return m;
    }

    private Map<String, Object> operator(String typeName, Map<String, Object> params,
                                         Map<String, Object> meta) {
        Map<String, Object> op = new LinkedHashMap<>();
        op.put("type_name", typeName);
        op.put("$metadata", meta);
        for (Map.Entry<String, Object> e : params.entrySet()) {
            op.put(e.getKey(), e.getValue());
        }
        return op;
    }

    @Test
    void testSimplePipeline() throws Exception {
        List<Map<String, Object>> staticItems = new ArrayList<>();
        Map<String, Object> item1 = new LinkedHashMap<>();
        item1.put("item_id", "a");
        item1.put("score", 1.0);
        staticItems.add(item1);
        Map<String, Object> item2 = new LinkedHashMap<>();
        item2.put("item_id", "b");
        item2.put("score", 2.0);
        staticItems.add(item2);

        Map<String, Object> operators = new LinkedHashMap<>();
        operators.put("recall", operator("recall_static",
                Collections.singletonMap("items", staticItems),
                metadata(Collections.emptyList(), Collections.emptyList(),
                        Collections.emptyList(), Arrays.asList("item_id", "score"))));
        operators.put("copy", operator("transform_copy",
                Collections.singletonMap("direction", "item_to_item"),
                metadata(Collections.emptyList(), Collections.emptyList(),
                        Collections.singletonList("score"), Collections.singletonList("final_score"))));

        Map<String, Object> flowContract = new LinkedHashMap<>();
        flowContract.put("common_input", Collections.emptyList());
        flowContract.put("common_output", Collections.emptyList());
        flowContract.put("item_output", Arrays.asList("item_id", "final_score"));

        byte[] config = buildConfig(operators, Arrays.asList("recall", "copy"), flowContract);
        Engine engine = Engine.create(config);

        Engine.Result result = engine.execute(new HashMap<>(), new ArrayList<>());

        assertNull(result.error);
        assertEquals(2, result.items.size());
        assertEquals("a", result.items.get(0).get("item_id"));
        assertEquals("b", result.items.get(1).get("item_id"));
        assertNotNull(result.items.get(0).get("final_score"));
    }

    @Test
    void testLuaTransform() throws Exception {
        List<Map<String, Object>> staticItems = new ArrayList<>();
        Map<String, Object> item1 = new LinkedHashMap<>();
        item1.put("item_id", "x");
        item1.put("score", 3.0);
        staticItems.add(item1);
        Map<String, Object> item2 = new LinkedHashMap<>();
        item2.put("item_id", "y");
        item2.put("score", 5.0);
        staticItems.add(item2);

        String luaScript = "function double_score() return score * 2 end";

        Map<String, Object> luaParams = new LinkedHashMap<>();
        luaParams.put("lua_script", luaScript);
        luaParams.put("function_for_item", "double_score");

        Map<String, Object> operators = new LinkedHashMap<>();
        operators.put("recall", operator("recall_static",
                Collections.singletonMap("items", staticItems),
                metadata(Collections.emptyList(), Collections.emptyList(),
                        Collections.emptyList(), Arrays.asList("item_id", "score"))));
        operators.put("lua", operator("transform_by_lua",
                luaParams,
                metadata(Collections.emptyList(), Collections.emptyList(),
                        Collections.singletonList("score"), Collections.singletonList("doubled"))));

        Map<String, Object> flowContract = new LinkedHashMap<>();
        flowContract.put("common_input", Collections.emptyList());
        flowContract.put("common_output", Collections.emptyList());
        flowContract.put("item_output", Arrays.asList("item_id", "score", "doubled"));

        byte[] config = buildConfig(operators, Arrays.asList("recall", "lua"), flowContract);
        Engine engine = Engine.create(config);

        Engine.Result result = engine.execute(new HashMap<>(), new ArrayList<>());

        assertNull(result.error);
        assertEquals(2, result.items.size());
        assertEquals(6.0, ((Number) result.items.get(0).get("doubled")).doubleValue(), 1e-9);
        assertEquals(10.0, ((Number) result.items.get(1).get("doubled")).doubleValue(), 1e-9);
    }

    @Test
    void testLuaPoolReuseStats() throws Exception {
        List<Map<String, Object>> staticItems = new ArrayList<>();
        Map<String, Object> item1 = new LinkedHashMap<>();
        item1.put("item_id", "x");
        item1.put("score", 3.0);
        staticItems.add(item1);

        String luaScript = "function double_score() return score * 2 end";

        Map<String, Object> luaParams = new LinkedHashMap<>();
        luaParams.put("lua_script", luaScript);
        luaParams.put("function_for_item", "double_score");

        Map<String, Object> operators = new LinkedHashMap<>();
        operators.put("recall", operator("recall_static",
                Collections.singletonMap("items", staticItems),
                metadata(Collections.emptyList(), Collections.emptyList(),
                        Collections.emptyList(), Arrays.asList("item_id", "score"))));
        operators.put("lua", operator("transform_by_lua",
                luaParams,
                metadata(Collections.emptyList(), Collections.emptyList(),
                        Collections.singletonList("score"), Collections.singletonList("doubled"))));

        Map<String, Object> flowContract = new LinkedHashMap<>();
        flowContract.put("common_input", Collections.emptyList());
        flowContract.put("common_output", Collections.emptyList());
        flowContract.put("item_output", Arrays.asList("item_id", "score", "doubled"));

        byte[] config = buildConfig(operators, Arrays.asList("recall", "lua"), flowContract);
        Engine engine = Engine.create(config);

        // Run several times so a state returned to the pool gets reused.
        for (int i = 0; i < 5; i++) {
            Engine.Result result = engine.execute(new HashMap<>(), new ArrayList<>());
            assertNull(result.error);
        }

        Map<String, Long> lua = engine.operatorCustomStats().get("lua");
        assertNotNull(lua, "lua operator should expose custom stats");
        assertTrue(lua.containsKey("reuse_count"), "stats must expose reuse_count");

        long borrow = lua.get("borrow_count");
        long reuse = lua.get("reuse_count");
        long create = lua.get("create_count");
        // Every borrow is a pool hit (reuse) or an on-borrow miss; create_count
        // also counts the single pre-warm creation, so misses = create_count - 1.
        assertEquals(borrow, reuse + (create - 1),
                "borrow_count must equal reuse_count + on-borrow misses");
        assertTrue(reuse > 0, "repeated executions must reuse pooled states");
    }

    @Test
    void testEngineCloseTearsDownLuaPool() throws Exception {
        List<Map<String, Object>> staticItems = new ArrayList<>();
        Map<String, Object> item1 = new LinkedHashMap<>();
        item1.put("item_id", "x");
        item1.put("score", 3.0);
        staticItems.add(item1);

        String luaScript = "function double_score() return score * 2 end";

        Map<String, Object> luaParams = new LinkedHashMap<>();
        luaParams.put("lua_script", luaScript);
        luaParams.put("function_for_item", "double_score");

        Map<String, Object> operators = new LinkedHashMap<>();
        operators.put("recall", operator("recall_static",
                Collections.singletonMap("items", staticItems),
                metadata(Collections.emptyList(), Collections.emptyList(),
                        Collections.emptyList(), Arrays.asList("item_id", "score"))));
        operators.put("lua", operator("transform_by_lua",
                luaParams,
                metadata(Collections.emptyList(), Collections.emptyList(),
                        Collections.singletonList("score"), Collections.singletonList("doubled"))));

        Map<String, Object> flowContract = new LinkedHashMap<>();
        flowContract.put("common_input", Collections.emptyList());
        flowContract.put("common_output", Collections.emptyList());
        flowContract.put("item_output", Arrays.asList("item_id", "score", "doubled"));

        byte[] config = buildConfig(operators, Arrays.asList("recall", "lua"), flowContract);
        Engine engine = Engine.create(config);

        // Pool works before close.
        Engine.Result before = engine.execute(new HashMap<>(), new ArrayList<>());
        assertNull(before.error);

        // Retiring the engine must close the operator's Lua state pool.
        engine.close();

        // After close, the pool refuses to hand out states and the operator errors.
        Engine.Result after = engine.execute(new HashMap<>(), new ArrayList<>());
        assertNotNull(after.error, "executing a closed engine's Lua pool must error");
        assertTrue(after.error.getMessage().contains("pool is closed"),
                "error should report the pool is closed, got: " + after.error.getMessage());
    }

    @Test
    void testFilterAndSort() throws Exception {
        List<Map<String, Object>> staticItems = new ArrayList<>();
        Map<String, Object> item1 = new LinkedHashMap<>();
        item1.put("item_id", "a");
        item1.put("score", 1.0);
        staticItems.add(item1);
        Map<String, Object> item2 = new LinkedHashMap<>();
        item2.put("item_id", "b");
        item2.put("score", 3.0);
        staticItems.add(item2);
        Map<String, Object> item3 = new LinkedHashMap<>();
        item3.put("item_id", "c");
        item3.put("score", 2.0);
        staticItems.add(item3);

        Map<String, Object> truncateParams = new LinkedHashMap<>();
        truncateParams.put("top_n", 2);

        Map<String, Object> sortParams = new LinkedHashMap<>();
        sortParams.put("order", "desc");

        Map<String, Object> operators = new LinkedHashMap<>();
        operators.put("recall", operator("recall_static",
                Collections.singletonMap("items", staticItems),
                metadata(Collections.emptyList(), Collections.emptyList(),
                        Collections.emptyList(), Arrays.asList("item_id", "score"))));
        operators.put("truncate", operator("filter_truncate",
                truncateParams,
                metadata(Collections.emptyList(), Collections.emptyList(),
                        Collections.singletonList("score"), Collections.emptyList())));
        operators.put("sort", operator("reorder_sort",
                sortParams,
                metadata(Collections.emptyList(), Collections.emptyList(),
                        Collections.singletonList("score"), Collections.emptyList())));

        Map<String, Object> flowContract = new LinkedHashMap<>();
        flowContract.put("common_input", Collections.emptyList());
        flowContract.put("common_output", Collections.emptyList());
        flowContract.put("item_output", Arrays.asList("item_id", "score"));

        byte[] config = buildConfig(operators, Arrays.asList("recall", "truncate", "sort"), flowContract);
        Engine engine = Engine.create(config);

        Engine.Result result = engine.execute(new HashMap<>(), new ArrayList<>());

        assertNull(result.error);
        assertEquals(2, result.items.size());
        double firstScore = ((Number) result.items.get(0).get("score")).doubleValue();
        double secondScore = ((Number) result.items.get(1).get("score")).doubleValue();
        assertTrue(firstScore >= secondScore, "items should be sorted desc by score");
    }

    @Test
    void testCancellation() throws Exception {
        List<Map<String, Object>> staticItems = new ArrayList<>();
        Map<String, Object> item1 = new LinkedHashMap<>();
        item1.put("item_id", "a");
        item1.put("score", 1.0);
        staticItems.add(item1);

        Map<String, Object> operators = new LinkedHashMap<>();
        operators.put("recall", operator("recall_static",
                Collections.singletonMap("items", staticItems),
                metadata(Collections.emptyList(), Collections.emptyList(),
                        Collections.emptyList(), Arrays.asList("item_id", "score"))));

        Map<String, Object> flowContract = new LinkedHashMap<>();
        flowContract.put("common_input", Collections.emptyList());
        flowContract.put("common_output", Collections.emptyList());
        flowContract.put("item_output", Arrays.asList("item_id", "score"));

        byte[] config = buildConfig(operators, Collections.singletonList("recall"), flowContract);
        Engine engine = Engine.create(config);

        CancellationToken token = CancellationToken.create();
        token.cancel();

        Engine.Result result = engine.execute(token, new HashMap<>(), new ArrayList<>());

        // With pre-cancelled token, the engine should handle gracefully:
        // either produce partial/empty results or complete without throwing
        assertNotNull(result);
        assertNotNull(result.common);
    }

    @Test
    void testValidationError() throws Exception {
        List<Map<String, Object>> staticItems = new ArrayList<>();
        Map<String, Object> item1 = new LinkedHashMap<>();
        item1.put("item_id", "a");
        item1.put("score", 1.0);
        staticItems.add(item1);

        Map<String, Object> operators = new LinkedHashMap<>();
        operators.put("recall", operator("recall_static",
                Collections.singletonMap("items", staticItems),
                metadata(Collections.emptyList(), Collections.emptyList(),
                        Collections.emptyList(), Arrays.asList("item_id", "score"))));

        Map<String, Object> flowContract = new LinkedHashMap<>();
        flowContract.put("common_input", Collections.emptyList());
        flowContract.put("common_output", Collections.emptyList());
        flowContract.put("item_output", Arrays.asList("item_id", "score"));

        byte[] config = buildConfig(operators, Collections.singletonList("recall"), flowContract);
        Engine engine = Engine.create(config);

        assertThrows(PineErrors.ValidationError.class, () -> {
            engine.execute(null, new ArrayList<>());
        });
    }

    @Test
    void testDebugTrace() throws Exception {
        List<Map<String, Object>> staticItems = new ArrayList<>();
        Map<String, Object> item1 = new LinkedHashMap<>();
        item1.put("item_id", "a");
        item1.put("score", 1.0);
        staticItems.add(item1);

        Map<String, Object> operators = new LinkedHashMap<>();
        operators.put("recall", operator("recall_static",
                Collections.singletonMap("items", staticItems),
                metadata(Collections.emptyList(), Collections.emptyList(),
                        Collections.emptyList(), Arrays.asList("item_id", "score"))));

        Map<String, Object> flowContract = new LinkedHashMap<>();
        flowContract.put("common_input", Collections.emptyList());
        flowContract.put("common_output", Collections.emptyList());
        flowContract.put("item_output", Arrays.asList("item_id", "score"));

        byte[] config = buildConfigWithDebug(operators, Collections.singletonList("recall"), flowContract);
        Engine engine = Engine.create(config);

        Engine.Result result = engine.execute(new HashMap<>(), new ArrayList<>());

        assertNull(result.error);
        assertNotNull(result.trace);
        assertFalse(result.trace.isEmpty(), "trace should be non-empty in debug mode");
    }
}
