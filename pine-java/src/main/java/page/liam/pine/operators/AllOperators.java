package page.liam.pine.operators;

import page.liam.pine.OperatorType;
import page.liam.pine.Registry;

public class AllOperators {
    static {
        Registry.register("filter_condition", OperatorType.FILTER, FilterCondition::new);
        Registry.register("filter_truncate", OperatorType.FILTER, FilterTruncate::new);
        Registry.register("filter_paginate", OperatorType.FILTER, FilterPaginate::new);
        Registry.register("transform_copy", OperatorType.TRANSFORM, TransformCopy::new);
        Registry.register("transform_dispatch", OperatorType.TRANSFORM, TransformDispatch::new);
        Registry.register("transform_normalize", OperatorType.TRANSFORM, TransformNormalize::new);
        Registry.register("transform_size", OperatorType.TRANSFORM, TransformSize::new);
        Registry.register("merge_dedup", OperatorType.MERGE, MergeDedup::new);
        Registry.register("reorder_sort", OperatorType.REORDER, ReorderSort::new);
        Registry.register("recall_static", OperatorType.RECALL, RecallStatic::new);
        Registry.register("recall_resource", OperatorType.RECALL, RecallResource::new);
        Registry.register("transform_by_lua", OperatorType.TRANSFORM, TransformByLua::new);
        Registry.register("transform_resource_lookup", OperatorType.TRANSFORM, TransformResourceLookup::new);
        Registry.register("observe_log", OperatorType.OBSERVE, ObserveLog::new);
        Registry.register("reorder_shuffle_by_salt", OperatorType.REORDER, ReorderShuffle::new);
        Registry.register("transform_redis_get", OperatorType.TRANSFORM, TransformRedisGet::new);
        Registry.register("transform_redis_set", OperatorType.TRANSFORM, TransformRedisSet::new);
        Registry.register("transform_by_remote_pineapple", OperatorType.TRANSFORM, TransformRemotePineapple::new);
    }

    public static void ensureRegistered() {
        // Force class loading triggers static initializer
    }
}
