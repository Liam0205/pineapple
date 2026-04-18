# transform_normalize

**Type**: Transform

Normalizes a numeric item field using min-max scaling to [0, 1].

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| field | string | Yes | - | Item field to normalize. |
| method | string | No | `"min_max"` | Normalization method. |
| output_field | string | No | `""` | Target field for normalized values. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | `[]` |
| CommonOutput | `[]` |
| ItemInput | `[<field>]` |
| ItemOutput | `[<output_field>]` |

## DSL Usage

```python
flow.transform_normalize(
    field=...,
    method=...,
    output_field=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
