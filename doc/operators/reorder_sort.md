# reorder_sort

**Category**: Reorder

Sorts items by a numeric field in ascending or descending order.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| field | string | Yes | - | Item field to sort by. |
| order | string | No | `"desc"` | Sort direction — "asc" or "desc". |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | `[]` |
| CommonOutput | `[]` |
| ItemInput | `[<field>]` |
| ItemOutput | `[]` |

## DSL Usage

```python
flow.reorder_sort(
    field=...,
    order=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
