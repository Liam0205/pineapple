# Operator Reference

> Auto-generated from Go operator source code. Do not edit manually.


## Filter

| Operator | Description |
|----------|-------------|
| [filter_condition](filter_condition.md) | Removes items where a specified field equals a given value. |
| [filter_paginate](filter_paginate.md) | Keeps only items in the [page*size, page*size+size) range, removes the rest. |
| [filter_truncate](filter_truncate.md) | Keeps only the first N items, removing the rest. |

## Merge

| Operator | Description |
|----------|-------------|
| [merge_dedup](merge_dedup.md) | Deduplicates items by a key field, keeping the first occurrence. |

## Observe

| Operator | Description |
|----------|-------------|
| [observe_log](observe_log.md) | Reads declared input fields and writes them to Go standard log. This is a read-only operator: it produces no output fields and does not modify the DataFrame. It is exempt from dead-code detection. |

## Recall

| Operator | Description |
|----------|-------------|
| [recall_resource](recall_resource.md) | Recalls items from a named resource. |
| [recall_static](recall_static.md) | Emits a configurable static set of items for testing and validation. |

## Reorder

| Operator | Description |
|----------|-------------|
| [reorder_shuffle_by_salt](reorder_shuffle_by_salt.md) | Deterministic hash-based shuffle using a caller-provided salt. |
| [reorder_sort](reorder_sort.md) | Sorts items by a numeric field in ascending or descending order. |

## Transform

| Operator | Description |
|----------|-------------|
| [transform_bench_cpu](transform_bench_cpu.md) | Benchmark-only CPU-bound operator. Computes iterative fib per item. |
| [transform_bench_sleep](transform_bench_sleep.md) | Benchmark-only I/O-simulating operator. Sleeps for delay_ms per invocation. |
| [transform_by_lua](transform_by_lua.md) | Executes a Lua script for per-item or per-common computation. |
| [transform_by_remote_pineapple](transform_by_remote_pineapple.md) | Calls a downstream Pineapple service and maps response fields back to the local frame. |
| [transform_copy](transform_copy.md) | Copies field values between common and item dimensions. |
| [transform_dispatch](transform_dispatch.md) | Copies a common-side field value to every item as an item-side field. |
| [transform_normalize](transform_normalize.md) | Normalizes a numeric item field using min-max scaling to [0, 1]. |
| [transform_redis_get](transform_redis_get.md) | Generic Redis read operator. Reads a value by key and outputs the result and a cache-hit flag. |
| [transform_redis_set](transform_redis_set.md) | Generic Redis write operator. Writes a value by key with optional TTL. |
| [transform_resource_lookup](transform_resource_lookup.md) | Enriches items by looking up values from a named resource. |
| [transform_size](transform_size.md) | Outputs the current item count to a common field. |

