# transform_size

**Type**: Transform

Outputs the current item count to a common field.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | `[]` |
| CommonOutput | `[<target_field>]` |
| ItemInput | `[]` |
| ItemOutput | `[]` |

## DSL Usage

```python
flow.transform_size(
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
