# transform_dispatch

**Type**: Transform

Copies a common-side field value to every item as an item-side field.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | `[<common_field>]` |
| CommonOutput | `[]` |
| ItemInput | `[]` |
| ItemOutput | `[<item_field>]` |

## DSL Usage

```python
flow.transform_dispatch(
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
