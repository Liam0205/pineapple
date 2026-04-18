# transform_normalize

**Type**: Transform

Normalizes a numeric item field using min-max scaling to [0, 1].

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| method | string | No | `"min_max"` | Normalization method. |

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
    method=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
