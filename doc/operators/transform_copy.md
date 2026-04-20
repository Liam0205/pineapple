# transform_copy

**Type**: Transform

Copies field values between common and item dimensions.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| direction | string | Yes | - | Copy direction: "common_to_item", "item_to_common", "common_to_common", or "item_to_item". |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | `[<source_fields...>]` |
| CommonOutput | `[<target_field>]   (collects all item values into a list)` |
| ItemInput | `[<source_field>]` |
| ItemOutput | `[<target_fields...>]` |

## DSL Usage

```python
flow.transform_copy(
    direction=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
