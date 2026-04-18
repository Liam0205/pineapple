# merge_dedup

**Type**: Merge

Deduplicates items by a key field, keeping the first occurrence.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
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
    strategy=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
