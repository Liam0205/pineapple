# filter_condition

**Type**: Filter

Removes items where a specified field equals a given value.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| field | string | Yes | - | Item field to check. |
| value | any | Yes | - | Items where field == value are removed. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | `[]` |
| CommonOutput | `[]` |
| ItemInput | `[<field>]` |
| ItemOutput | `[]` |

## DSL Usage

```python
flow.filter_condition(
    field=...,
    value=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
