package page.liam.pine.operators;

import page.liam.pine.Codegen;
import page.liam.pine.OperatorSchema;
import page.liam.pine.OperatorType;
import page.liam.pine.ParamSpec;
import page.liam.pine.Registry;
import page.liam.pine.ResourceManager;
import page.liam.pine.ResourceRegistry;
import redis.clients.jedis.JedisPool;
import redis.clients.jedis.JedisPoolConfig;

import java.util.Collections;
import java.util.Map;

public class AllOperators {
    private static volatile boolean registered = false;

    public static void ensureRegistered() {
        if (!registered) {
            synchronized (AllOperators.class) {
                if (!registered) {
                    registerAll();
                    registered = true;
                }
            }
        }
    }

    private static void registerAll() {
        // 1. transform_copy
        Registry.registerGlobal(
                new OperatorSchema(
                        "transform_copy",
                        OperatorType.TRANSFORM,
                        "Copies field values between common and item dimensions.",
                        Map.of(
                                "direction", ParamSpec.required("string",
                                        "Copy direction: common_to_item, item_to_common, common_to_common, or item_to_item.")
                        )),
                TransformCopy::new);

        // 2. transform_dispatch
        Registry.registerGlobal(
                new OperatorSchema(
                        "transform_dispatch",
                        OperatorType.TRANSFORM,
                        "Copies a common-side field value to every item as an item-side field.",
                        Collections.emptyMap()),
                TransformDispatch::new);

        // 3. transform_normalize
        Registry.registerGlobal(
                new OperatorSchema(
                        "transform_normalize",
                        OperatorType.TRANSFORM,
                        "Normalizes a numeric item field using min-max scaling to [0, 1].",
                        Map.of(
                                "method", ParamSpec.optional("string", "min_max",
                                        "Normalization method.")
                        )),
                TransformNormalize::new);

        // 4. transform_size
        Registry.registerGlobal(
                new OperatorSchema(
                        "transform_size",
                        OperatorType.TRANSFORM,
                        "Outputs the current item count to a common field.",
                        Collections.emptyMap()),
                TransformSize::new);

        // 5. transform_by_lua
        Registry.registerGlobal(
                new OperatorSchema(
                        "transform_by_lua",
                        OperatorType.TRANSFORM,
                        "Executes a Lua script for per-item or per-common computation.",
                        Map.of(
                                "lua_script", ParamSpec.required("string",
                                        "Lua source code defining the function to call."),
                                "function_for_item", ParamSpec.optional("string", "",
                                        "Function name to call per item."),
                                "function_for_common", ParamSpec.optional("string", "",
                                        "Function name to call once for all items.")
                        )),
                TransformByLua::new);

        // 6. transform_resource_lookup
        Registry.registerGlobal(
                new OperatorSchema(
                        "transform_resource_lookup",
                        OperatorType.TRANSFORM,
                        "Enriches items by looking up values from a named resource.",
                        Map.of(
                                "resource_name", ParamSpec.required("string",
                                        "Name of the resource to read."),
                                "lookup_key", ParamSpec.required("string",
                                        "Item field whose value is used as the lookup key."),
                                "output_field", ParamSpec.required("string",
                                        "Item field to write the looked-up value to."),
                                "default_value", ParamSpec.optional("any", null,
                                        "Value to use when the key is not found. Missing keys are skipped if unset.")
                        )),
                TransformResourceLookup::new);

        // 7. transform_redis_get
        Registry.registerGlobal(
                new OperatorSchema(
                        "transform_redis_get",
                        OperatorType.TRANSFORM,
                        "Generic Redis read operator. Reads a value by key and outputs the result and a cache-hit flag.",
                        Map.of(
                                "resource_name", ParamSpec.required("string",
                                        "Name of a redis_connection resource to borrow the client from."),
                                "key_prefix", ParamSpec.required("string",
                                        "Key prefix prepended to the suffix built from common_input fields."),
                                "data_type", ParamSpec.optional("string", "string",
                                        "Redis data type: set, string, or list."),
                                "fail_on_error", ParamSpec.optional("bool", false,
                                        "Return fatal error on Redis infrastructure failure instead of treating as cache miss.")
                        )),
                TransformRedisGet::new);

        // 8. transform_redis_set
        Registry.registerGlobal(
                new OperatorSchema(
                        "transform_redis_set",
                        OperatorType.TRANSFORM,
                        "Generic Redis write operator. Writes a value by key with optional TTL.",
                        Map.of(
                                "resource_name", ParamSpec.required("string",
                                        "Name of a redis_connection resource to borrow the client from."),
                                "key_prefix", ParamSpec.required("string",
                                        "Key prefix prepended to the suffix built from common_input fields."),
                                "data_type", ParamSpec.optional("string", "string",
                                        "Redis data type: set, string, or list."),
                                "ttl", ParamSpec.optional("int", 0L,
                                        "TTL in seconds. 0 means no expiry."),
                                "fail_on_error", ParamSpec.optional("bool", false,
                                        "Return fatal error on Redis failure instead of logging to stderr.")
                        )),
                TransformRedisSet::new);

        // 9. transform_by_remote_pineapple (>10 params, use Map.ofEntries)
        Registry.registerGlobal(
                new OperatorSchema(
                        "transform_by_remote_pineapple",
                        OperatorType.TRANSFORM,
                        "Calls a downstream Pineapple service and maps response fields back to the local frame.",
                        Map.ofEntries(
                                Map.entry("host", ParamSpec.required("string",
                                        "Downstream service host.")),
                                Map.entry("port", ParamSpec.required("int64",
                                        "Downstream service port.")),
                                Map.entry("endpoint", ParamSpec.optional("string", "/execute",
                                        "Downstream endpoint path.")),
                                Map.entry("timeout", ParamSpec.optional("float64", 5.0,
                                        "Request timeout in seconds.")),
                                Map.entry("fail_on_error", ParamSpec.optional("bool", true,
                                        "true=fatal on downstream error; false=warning and skip.")),
                                Map.entry("max_response_size", ParamSpec.optional("int64", 10485760L,
                                        "Maximum response body size in bytes (default 10 MB).")),
                                Map.entry("allow_private", ParamSpec.optional("bool", false,
                                        "Allow connections to private/loopback addresses (dev/internal use).")),
                                Map.entry("common_request", ParamSpec.optional("any", null,
                                        "Downstream common field names, positionally mapped to common_input.")),
                                Map.entry("item_request", ParamSpec.optional("any", null,
                                        "Downstream item field names, positionally mapped to item_input.")),
                                Map.entry("common_response", ParamSpec.optional("any", null,
                                        "Downstream common response field names, positionally mapped to common_output.")),
                                Map.entry("item_response", ParamSpec.optional("any", null,
                                        "Downstream item response field names, positionally mapped to item_output."))
                        )),
                TransformRemotePineapple::new);

        // 10. recall_static
        Registry.registerGlobal(
                new OperatorSchema(
                        "recall_static",
                        OperatorType.RECALL,
                        "Emits a configurable static set of items for testing and validation.",
                        Map.of(
                                "items", ParamSpec.required("any",
                                        "JSON array of item maps to emit as candidates.")
                        )),
                RecallStatic::new);

        // 11. recall_resource
        Registry.registerGlobal(
                new OperatorSchema(
                        "recall_resource",
                        OperatorType.RECALL,
                        "Recalls items from a named resource.",
                        Map.of(
                                "resource_name", ParamSpec.required("string",
                                        "Name of the resource to read.")
                        )),
                RecallResource::new);

        // 12. filter_condition
        Registry.registerGlobal(
                new OperatorSchema(
                        "filter_condition",
                        OperatorType.FILTER,
                        "Removes items where a specified field equals a given value.",
                        Map.of(
                                "value", ParamSpec.required("any",
                                        "Items where field == value are removed.")
                        )),
                FilterCondition::new);

        // 13. filter_truncate
        Registry.registerGlobal(
                new OperatorSchema(
                        "filter_truncate",
                        OperatorType.FILTER,
                        "Keeps only the first N items, removing the rest.",
                        Map.of(
                                "top_n", ParamSpec.required("int64",
                                        "Number of items to keep.")
                        )),
                FilterTruncate::new);

        // 14. filter_paginate
        Registry.registerGlobal(
                new OperatorSchema(
                        "filter_paginate",
                        OperatorType.FILTER,
                        "Keeps only items in the [page*size, page*size+size) range, removes the rest.",
                        Collections.emptyMap()),
                FilterPaginate::new);

        // 15. merge_dedup
        Registry.registerGlobal(
                new OperatorSchema(
                        "merge_dedup",
                        OperatorType.MERGE,
                        "Deduplicates items by a key field, keeping the first occurrence.",
                        Map.of(
                                "strategy", ParamSpec.optional("string", "first",
                                        "Dedup strategy — first keeps first occurrence.")
                        )),
                MergeDedup::new);

        // 16. reorder_sort
        Registry.registerGlobal(
                new OperatorSchema(
                        "reorder_sort",
                        OperatorType.REORDER,
                        "Sorts items by a numeric field in ascending or descending order.",
                        Map.of(
                                "order", ParamSpec.optional("string", "desc",
                                        "Sort direction — asc or desc.")
                        )),
                ReorderSort::new);

        // 17. reorder_shuffle_by_salt
        Registry.registerGlobal(
                new OperatorSchema(
                        "reorder_shuffle_by_salt",
                        OperatorType.REORDER,
                        "Deterministic hash-based shuffle using a caller-provided salt.",
                        Collections.emptyMap()),
                ReorderShuffle::new);

        // 18. observe_log
        Registry.registerGlobal(
                new OperatorSchema(
                        "observe_log",
                        OperatorType.OBSERVE,
                        "Reads declared input fields and writes them to standard log. Read-only operator.",
                        Map.of(
                                "log_prefix", ParamSpec.optional("string", "",
                                        "Prefix prepended to each log line.")
                        )),
                ObserveLog::new);

        // 19. transform_bench_cpu
        Registry.registerGlobal(
                new OperatorSchema(
                        "transform_bench_cpu",
                        OperatorType.TRANSFORM,
                        "Benchmark-only CPU-bound operator. Computes iterative fib per item. Not available in pine-python.",
                        Map.of(
                                "iterations", ParamSpec.optional("int", 100,
                                        "Number of fib(32) computations per item.")
                        )),
                TransformBenchCpu::new);

        // 20. transform_bench_sleep
        Registry.registerGlobal(
                new OperatorSchema(
                        "transform_bench_sleep",
                        OperatorType.TRANSFORM,
                        "Benchmark-only I/O-simulating operator. Sleeps for delay_ms per invocation. Not available in pine-python.",
                        Map.of(
                                "delay_ms", ParamSpec.optional("int", 5,
                                        "Milliseconds to sleep per operator invocation.")
                        )),
                TransformBenchSleep::new);

        registerRedisConnectionResource();

        if (Boolean.getBoolean("pine.bench")) {
            page.liam.pine.operators.bench.BenchStubs.ensureRegistered();
        }
    }

    /**
     * Registers the built-in redis_connection resource type: a shared JedisPool
     * borrowed by transform_redis_get/set via resource_name, mirroring Go's
     * operators/transform/redis_connection.go. DefaultInterval is -1 (never
     * refresh): a connection pool has no meaningful refresh and is held until the
     * ResourceManager retires, at which point stop() closes the pool.
     */
    private static void registerRedisConnectionResource() {
        ResourceManager.registerFactory("redis_connection", params -> {
            Object addrObj = params.get("addr");
            String addr = addrObj instanceof String ? (String) addrObj : "";
            if (addr.isEmpty()) {
                throw new IllegalArgumentException("redis_connection: addr is required");
            }
            Object pwObj = params.get("password");
            String password = pwObj instanceof String ? (String) pwObj : "";
            Object dbObj = params.get("db");
            int db = dbObj instanceof Number ? ((Number) dbObj).intValue() : 0;
            return () -> {
                String host = addr.contains(":") ? addr.substring(0, addr.indexOf(':')) : addr;
                int port = addr.contains(":") ? Integer.parseInt(addr.substring(addr.indexOf(':') + 1)) : 6379;
                JedisPoolConfig cfg = new JedisPoolConfig();
                // JedisPool construction is lazy (no connect here), so a never-refresh
                // pool can be created at start even when Redis is unavailable; the
                // failure surfaces at borrow time and degrades per fail_on_error.
                return password.isEmpty()
                        ? new JedisPool(cfg, host, port, 2000, null, db)
                        : new JedisPool(cfg, host, port, 2000, password, db);
            };
        });

        Codegen.ResourceSchema schema = new Codegen.ResourceSchema();
        schema.name = "redis_connection";
        schema.description = "Shared Redis connection pool borrowed by Redis operators via resource_name.";
        schema.defaultInterval = -1;
        schema.params = Map.of(
                "addr", codegenParam("string", true, null, "Redis server address (host:port)."),
                "password", codegenParam("string", false, "", "Redis password."),
                "db", codegenParam("int", false, 0L, "Redis DB number.")
        );
        ResourceRegistry.register(schema);
    }

    private static Codegen.ParamSpec codegenParam(String type, boolean required, Object defaultValue, String description) {
        Codegen.ParamSpec ps = new Codegen.ParamSpec();
        ps.type = type;
        ps.required = required;
        ps.defaultValue = defaultValue;
        ps.description = description;
        return ps;
    }
}
