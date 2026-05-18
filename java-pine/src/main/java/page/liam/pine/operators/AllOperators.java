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
        Registry.register("transform_by_lua", OperatorType.TRANSFORM, TransformByLua::new);
    }

    public static void ensureRegistered() {
        // Force class loading triggers static initializer
    }
}
