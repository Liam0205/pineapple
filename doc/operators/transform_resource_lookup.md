# transform_resource_lookup

**Type**: Transform

Enriches items by looking up values from a named resource.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| lookup_key | string | Yes | - | Item field whose value is used as the lookup key. |
| output_field | string | Yes | - | Item field to write the looked-up value to. |
| resource_name | string | Yes | - | Name of the resource to read. |
| default_value | any | No | - | Value to use when the key is not found. Missing keys are skipped if unset. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | - |
| CommonOutput | - |
| ItemInput | `[<lookup_key>]` |
| ItemOutput | `[<output_field>]` |

## DSL Usage

```python
flow.transform_resource_lookup(
    lookup_key=...,
    output_field=...,
    resource_name=...,
    default_value=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
