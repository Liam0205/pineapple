# Operator Reference

> Auto-generated from Go operator source code. Do not edit manually.


## Filter

| Operator | Description |
|----------|-------------|
| [filter_condition](filter_condition.md) | Removes items where a specified field equals a given value. |
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
| [recall_static](recall_static.md) | Emits a configurable static set of items for testing and validation. |

## Reorder

| Operator | Description |
|----------|-------------|
| [reorder_sort](reorder_sort.md) | Sorts items by a numeric field in ascending or descending order. |

## Transform

| Operator | Description |
|----------|-------------|
| [transform_by_lua](transform_by_lua.md) | Executes a Lua script for per-item or per-common computation. |
| [transform_dispatch](transform_dispatch.md) | Copies a common-side field value to every item as an item-side field. |
| [transform_normalize](transform_normalize.md) | Normalizes a numeric item field using min-max scaling to [0, 1]. |

