package page.liam.pine.operators.bench;

import page.liam.pine.*;

import java.util.*;

public class BenchStubs {
    private static volatile boolean registered = false;

    public static void ensureRegistered() {
        if (!registered) {
            synchronized (BenchStubs.class) {
                if (!registered) {
                    registerAll();
                    registered = true;
                }
            }
        }
    }

    private static void registerAll() {
        Registry.registerGlobal(
                new OperatorSchema(
                        "recall_feed_data",
                        OperatorType.RECALL,
                        "Benchmark stub: generates synthetic feed items.",
                        Map.of(
                                "bench_item_count", ParamSpec.optional("int", 3000L, "Number of items to generate."),
                                "resource_name", ParamSpec.optional("string", "", "Ignored in stub.")
                        )),
                RecallFeedDataStub::new);

        Registry.registerGlobal(
                new OperatorSchema(
                        "transform_redis_zrangebyscore",
                        OperatorType.TRANSFORM,
                        "Benchmark stub: simulates Redis ZRANGEBYSCORE.",
                        Map.of(
                                "key_prefix", ParamSpec.optional("string", "", "Stub param."),
                                "window_seconds", ParamSpec.optional("int", 0L, "Stub param."),
                                "redis_addr", ParamSpec.optional("string", "", "Stub param."),
                                "redis_password", ParamSpec.optional("string", "", "Stub param.")
                        )),
                TransformRedisZrangebyscoreStub::new);
        Registry.registerGlobal(
                new OperatorSchema(
                        "transform_hydrate",
                        OperatorType.TRANSFORM,
                        "Benchmark stub: simulates MySQL hydration.",
                        Map.of(
                                "mysql_dsn", ParamSpec.optional("string", "", "Stub param.")
                        )),
                TransformHydrateStub::new);

        Registry.registerGlobal(
                new OperatorSchema(
                        "transform_query_blocked_creators",
                        OperatorType.TRANSFORM,
                        "Benchmark stub: simulates MySQL blocked-creators query.",
                        Map.of(
                                "mysql_dsn", ParamSpec.optional("string", "", "Stub param.")
                        )),
                TransformQueryBlockedCreatorsStub::new);

        Registry.registerGlobal(
                new OperatorSchema(
                        "filter_impression",
                        OperatorType.FILTER,
                        "Benchmark stub: simulates impression-based filtering.",
                        Map.of(
                                "min_remaining_ratio", ParamSpec.optional("float64", 1.5, "Stub param.")
                        )),
                FilterImpressionStub::new);

        Registry.registerGlobal(
                new OperatorSchema(
                        "filter_blocked_creator",
                        OperatorType.FILTER,
                        "Benchmark stub: simulates blocked-creator filtering.",
                        Collections.emptyMap()),
                FilterBlockedCreatorStub::new);

        Registry.registerGlobal(
                new OperatorSchema(
                        "reorder_topn_boost",
                        OperatorType.REORDER,
                        "Benchmark stub: simulates top-N boost reordering.",
                        Map.of(
                                "size", ParamSpec.optional("int", 10L, "Stub param.")
                        )),
                ReorderTopnBoostStub::new);

        Registry.registerGlobal(
                new OperatorSchema(
                        "observe_datahub",
                        OperatorType.OBSERVE,
                        "Benchmark stub: simulates DataHub MQ write.",
                        Map.of(
                                "resource_name", ParamSpec.optional("string", "", "Stub param."),
                                "mode", ParamSpec.optional("string", "", "Stub param."),
                                "key_fields", ParamSpec.optional("any", null, "Stub param.")
                        )),
                ObserveDatahubStub::new);

        Registry.registerGlobal(
                new OperatorSchema(
                        "transform_generate_request_id",
                        OperatorType.TRANSFORM,
                        "Benchmark stub: generates a fixed request ID.",
                        Map.of(
                                "prefix", ParamSpec.optional("string", "bench", "Stub param.")
                        )),
                TransformGenerateRequestIdStub::new);

        ResourceManager.registerFactory("feed_data", params -> () -> {
            List<Map<String, Object>> items = new ArrayList<>(3000);
            for (int i = 0; i < 3000; i++) {
                Map<String, Object> row = new LinkedHashMap<>();
                row.put("id", (double) (i + 1));
                row.put("item_id", String.valueOf(10000 + i));
                row.put("type", (double) (i % 3 + 1));
                row.put("score", (double) (1000 - i));
                row.put("created_at", "2026-01-01T00:00:00Z");
                items.add(row);
            }
            return items;
        });

        ResourceManager.registerFactory("datahub_producer", params -> () -> "nop");
    }
}
