# filter_truncate

**Type**: Filter

Keeps only the first N items, removing the rest.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| top_n | int64 | Yes | - | Number of items to keep. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | `[]` |
| CommonOutput | `[]` |
| ItemInput | `[]` |
| ItemOutput | `[]` |

## DSL Usage

```python
flow.filter_truncate(
    top_n=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
