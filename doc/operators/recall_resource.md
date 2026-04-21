# recall_resource

**Type**: Recall

Recalls items from a named resource.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| resource_name | string | Yes | - | Name of the resource to read. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | - |
| CommonOutput | - |
| ItemInput | - |
| ItemOutput | `[<fields present in the resource items>]` |

## DSL Usage

```python
flow.recall_resource(
    resource_name=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
