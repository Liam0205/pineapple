# merge_dedup

**Category**: Merge

Deduplicates items by a key field, keeping the first occurrence.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| dedup_by | string | Yes | - | Field name to deduplicate on. |
| strategy | string | No | `"first"` | Dedup strategy — "first" keeps first occurrence. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | `[]` |
| CommonOutput | `[]` |
| ItemInput | `[item_id, _source]` |
| ItemOutput | `[item_id]` |

## DSL Usage

```python
flow.merge_dedup(
    dedup_by=...,
    strategy=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
