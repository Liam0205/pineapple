# Operator Test Fixtures

JSON fixture files for cross-language operator validation.

## Schema

Each file is named `{operator_name}.json` and contains:

```json
{
  "operator": "string — registered operator type_name",
  "cases": [
    {
      "name": "string — human-readable test case name",
      "params": { "key": "value" },
      "metadata": {
        "common_input": ["field1"],
        "item_input": ["field2"],
        "common_output": ["field3"],
        "item_output": ["field4"]
      },
      "input": {
        "common": { "field1": "value" },
        "items": [ { "field2": "value" } ]
      },
      "expected": {
        "common": { "field3": "value" },
        "items": [ { "field4": "value" } ],
        "added_items": [ { "key": "value" } ],
        "removed_indices": [0, 2],
        "warnings": ["optional warning substring match"]
      }
    }
  ]
}
```

## Expected output semantics

- `expected.common`: fields that must appear in common writes (subset match)
- `expected.items`: for Transform — per-item writes indexed by position
- `expected.added_items`: for Recall — items produced by the operator
- `expected.removed_indices`: for Filter — indices of items removed
- `expected.item_order`: for Reorder — final item index order after sorting
- `expected.warnings`: if present, each string must be a substring of a warning message
- Omitted fields in `expected` are not checked

## File naming

`fixtures/operators/{operator_name}.json` — one file per operator, e.g. `fixtures/operators/filter_condition.json`

## Coverage

| Operator | Cases | Type |
|----------|-------|------|
| filter_condition | 4 | Filter |
| filter_truncate | 4 | Filter |
| filter_paginate | 5 | Filter |
| transform_copy | 5 | Transform |
| transform_dispatch | 3 | Transform |
| transform_normalize | 4 | Transform |
| transform_size | 2 | Transform |
| transform_by_lua | 8 | Transform (Lua) |
| merge_dedup | 3 | Merge |
| reorder_sort | 4 | Reorder |
| recall_static | 2 | Recall |
| **Total** | **44** | |

Not converted (require external services or non-deterministic):
- transform_redis_get/set (requires Redis)
- transform_by_remote_pineapple (requires HTTP)
- transform_resource_lookup, recall_resource (requires resource.Context)
- reorder_shuffle_by_salt (non-deterministic)
