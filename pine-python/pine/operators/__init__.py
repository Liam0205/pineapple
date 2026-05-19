"""Operator registration hub for pine-python.

Call ``ensure_registered()`` once at startup to register all built-in operators
with the global registry.
"""
from __future__ import annotations

import threading

from pine.operator import OperatorSchema, OperatorType, ParamSpec
from pine.registry import Registry

_registered = False
_register_lock = threading.Lock()


def ensure_registered():
    """Register all built-in operators with the global registry (idempotent)."""
    global _registered
    if _registered:
        return
    with _register_lock:
        if _registered:
            return
        _register_all()
        _registered = True


def _register_all():
    from pine.operators.filter_condition import FilterCondition
    from pine.operators.filter_paginate import FilterPaginate
    from pine.operators.filter_truncate import FilterTruncate
    from pine.operators.merge_dedup import MergeDedup
    from pine.operators.observe_log import ObserveLog
    from pine.operators.recall_resource import RecallResource
    from pine.operators.recall_static import RecallStatic
    from pine.operators.reorder_shuffle import ReorderShuffle
    from pine.operators.reorder_sort import ReorderSort
    from pine.operators.transform_by_lua import TransformByLua
    from pine.operators.transform_copy import TransformCopy
    from pine.operators.transform_dispatch import TransformDispatch
    from pine.operators.transform_normalize import TransformNormalize
    from pine.operators.transform_redis_get import TransformRedisGet
    from pine.operators.transform_redis_set import TransformRedisSet
    from pine.operators.transform_remote_pineapple import TransformRemotePineapple
    from pine.operators.transform_resource_lookup import TransformResourceLookup
    from pine.operators.transform_size import TransformSize

    # 1. transform_copy
    Registry.register_global(
        OperatorSchema(
            name="transform_copy",
            type=OperatorType.TRANSFORM,
            description="Copies field values between common and item dimensions.",
            params={
                "direction": ParamSpec.required_param(
                    "string",
                    "Copy direction: common_to_item, item_to_common,"
                    " common_to_common, or item_to_item.",
                ),
            },
        ),
        TransformCopy,
    )

    # 2. transform_dispatch
    Registry.register_global(
        OperatorSchema(
            name="transform_dispatch",
            type=OperatorType.TRANSFORM,
            description="Copies a common-side field value to every item as an item-side field.",
            params={},
        ),
        TransformDispatch,
    )

    # 3. transform_normalize
    Registry.register_global(
        OperatorSchema(
            name="transform_normalize",
            type=OperatorType.TRANSFORM,
            description="Normalizes a numeric item field using min-max scaling to [0, 1].",
            params={
                "method": ParamSpec.optional(
                    "string", "min_max", "Normalization method."
                ),
            },
        ),
        TransformNormalize,
    )

    # 4. transform_size
    Registry.register_global(
        OperatorSchema(
            name="transform_size",
            type=OperatorType.TRANSFORM,
            description="Outputs the current item count to a common field.",
            params={},
        ),
        TransformSize,
    )

    # 5. transform_by_lua
    Registry.register_global(
        OperatorSchema(
            name="transform_by_lua",
            type=OperatorType.TRANSFORM,
            description="Executes a Lua script for per-item or per-common computation.",
            params={
                "lua_script": ParamSpec.required_param(
                    "string", "Lua source code defining the function to call."
                ),
                "function_for_item": ParamSpec.optional(
                    "string", "", "Function name to call per item."
                ),
                "function_for_common": ParamSpec.optional(
                    "string", "", "Function name to call once for all items."
                ),
            },
        ),
        TransformByLua,
    )

    # 6. transform_resource_lookup
    Registry.register_global(
        OperatorSchema(
            name="transform_resource_lookup",
            type=OperatorType.TRANSFORM,
            description="Enriches items by looking up values from a named resource.",
            params={
                "resource_name": ParamSpec.required_param(
                    "string", "Name of the resource to read."
                ),
                "lookup_key": ParamSpec.required_param(
                    "string", "Item field whose value is used as the lookup key."
                ),
                "output_field": ParamSpec.required_param(
                    "string", "Item field to write the looked-up value to."
                ),
                "default_value": ParamSpec.optional(
                    "any",
                    None,
                    "Value to use when the key is not found. Missing keys are skipped if unset.",
                ),
            },
        ),
        TransformResourceLookup,
    )

    # 7. transform_redis_get
    Registry.register_global(
        OperatorSchema(
            name="transform_redis_get",
            type=OperatorType.TRANSFORM,
            description=(
                "Generic Redis read operator. Reads a value by key"
                " and outputs the result and a cache-hit flag."
            ),
            params={
                "redis_addr": ParamSpec.required_param(
                    "string", "Redis server address (host:port)."
                ),
                "redis_password": ParamSpec.optional(
                    "string", "", "Redis password."
                ),
                "redis_db": ParamSpec.optional("int", 0, "Redis DB number."),
                "key_prefix": ParamSpec.required_param(
                    "string",
                    "Key prefix prepended to the suffix built from common_input fields.",
                ),
                "data_type": ParamSpec.optional(
                    "string", "string", "Redis data type: set, string, or list."
                ),
                "fail_on_error": ParamSpec.optional(
                    "bool",
                    False,
                    "Return fatal error on Redis infrastructure failure"
                    " instead of treating as cache miss.",
                ),
            },
        ),
        TransformRedisGet,
    )

    # 8. transform_redis_set
    Registry.register_global(
        OperatorSchema(
            name="transform_redis_set",
            type=OperatorType.TRANSFORM,
            description="Generic Redis write operator. Writes a value by key with optional TTL.",
            params={
                "redis_addr": ParamSpec.required_param(
                    "string", "Redis server address (host:port)."
                ),
                "redis_password": ParamSpec.optional(
                    "string", "", "Redis password."
                ),
                "redis_db": ParamSpec.optional("int", 0, "Redis DB number."),
                "key_prefix": ParamSpec.required_param(
                    "string",
                    "Key prefix prepended to the suffix built from common_input fields.",
                ),
                "data_type": ParamSpec.optional(
                    "string", "string", "Redis data type: set, string, or list."
                ),
                "ttl": ParamSpec.optional(
                    "int", 0, "TTL in seconds. 0 means no expiry."
                ),
                "fail_on_error": ParamSpec.optional(
                    "bool",
                    False,
                    "Return fatal error on Redis failure instead of logging to stderr.",
                ),
            },
        ),
        TransformRedisSet,
    )

    # 9. transform_by_remote_pineapple
    Registry.register_global(
        OperatorSchema(
            name="transform_by_remote_pineapple",
            type=OperatorType.TRANSFORM,
            description=(
                "Calls a downstream Pineapple service and maps"
                " response fields back to the local frame."
            ),
            params={
                "host": ParamSpec.required_param(
                    "string", "Downstream service host."
                ),
                "port": ParamSpec.required_param(
                    "int64", "Downstream service port."
                ),
                "endpoint": ParamSpec.optional(
                    "string", "/execute", "Downstream endpoint path."
                ),
                "timeout": ParamSpec.optional(
                    "float64", 5.0, "Request timeout in seconds."
                ),
                "fail_on_error": ParamSpec.optional(
                    "bool",
                    True,
                    "true=fatal on downstream error; false=warning and skip.",
                ),
                "max_response_size": ParamSpec.optional(
                    "int64",
                    10485760,
                    "Maximum response body size in bytes (default 10 MB).",
                ),
                "allow_private": ParamSpec.optional(
                    "bool",
                    False,
                    "Allow connections to private/loopback addresses (dev/internal use).",
                ),
                "common_request": ParamSpec.optional(
                    "any",
                    None,
                    "Downstream common field names, positionally mapped to common_input.",
                ),
                "item_request": ParamSpec.optional(
                    "any",
                    None,
                    "Downstream item field names, positionally mapped to item_input.",
                ),
                "common_response": ParamSpec.optional(
                    "any",
                    None,
                    "Downstream common response field names, positionally mapped to common_output.",
                ),
                "item_response": ParamSpec.optional(
                    "any",
                    None,
                    "Downstream item response field names, positionally mapped to item_output.",
                ),
            },
        ),
        TransformRemotePineapple,
    )

    # 10. recall_static
    Registry.register_global(
        OperatorSchema(
            name="recall_static",
            type=OperatorType.RECALL,
            description="Emits a configurable static set of items for testing and validation.",
            params={
                "items": ParamSpec.required_param(
                    "any", "JSON array of item maps to emit as candidates."
                ),
            },
        ),
        RecallStatic,
    )

    # 11. recall_resource
    Registry.register_global(
        OperatorSchema(
            name="recall_resource",
            type=OperatorType.RECALL,
            description="Recalls items from a named resource.",
            params={
                "resource_name": ParamSpec.required_param(
                    "string", "Name of the resource to read."
                ),
            },
        ),
        RecallResource,
    )

    # 12. filter_condition
    Registry.register_global(
        OperatorSchema(
            name="filter_condition",
            type=OperatorType.FILTER,
            description="Removes items where a specified field equals a given value.",
            params={
                "value": ParamSpec.required_param(
                    "any", "Items where field == value are removed."
                ),
            },
        ),
        FilterCondition,
    )

    # 13. filter_truncate
    Registry.register_global(
        OperatorSchema(
            name="filter_truncate",
            type=OperatorType.FILTER,
            description="Keeps only the first N items, removing the rest.",
            params={
                "top_n": ParamSpec.required_param(
                    "int64", "Number of items to keep."
                ),
            },
        ),
        FilterTruncate,
    )

    # 14. filter_paginate
    Registry.register_global(
        OperatorSchema(
            name="filter_paginate",
            type=OperatorType.FILTER,
            description=(
                "Keeps only items in the [page*size, page*size+size)"
                " range, removes the rest."
            ),
            params={},
        ),
        FilterPaginate,
    )

    # 15. merge_dedup
    Registry.register_global(
        OperatorSchema(
            name="merge_dedup",
            type=OperatorType.MERGE,
            description="Deduplicates items by a key field, keeping the first occurrence.",
            params={
                "strategy": ParamSpec.optional(
                    "string", "first", "Dedup strategy -- first keeps first occurrence."
                ),
            },
        ),
        MergeDedup,
    )

    # 16. reorder_sort
    Registry.register_global(
        OperatorSchema(
            name="reorder_sort",
            type=OperatorType.REORDER,
            description="Sorts items by a numeric field in ascending or descending order.",
            params={
                "order": ParamSpec.optional(
                    "string", "desc", "Sort direction -- asc or desc."
                ),
            },
        ),
        ReorderSort,
    )

    # 17. reorder_shuffle_by_salt
    Registry.register_global(
        OperatorSchema(
            name="reorder_shuffle_by_salt",
            type=OperatorType.REORDER,
            description="Deterministic hash-based shuffle using a caller-provided salt.",
            params={},
        ),
        ReorderShuffle,
    )

    # 18. observe_log
    Registry.register_global(
        OperatorSchema(
            name="observe_log",
            type=OperatorType.OBSERVE,
            description=(
                "Reads declared input fields and writes them to"
                " standard log. Read-only operator."
            ),
            params={
                "log_prefix": ParamSpec.optional(
                    "string", "", "Prefix prepended to each log line."
                ),
            },
        ),
        ObserveLog,
    )
