# recall_static

**Type**: Recall

Emits a configurable static set of items for testing and validation.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| items | any | Yes | - | JSON array of item maps to emit as candidates. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | `[]` |
| CommonOutput | `[]` |
| ItemInput | `[]` |
| ItemOutput | `[item_id, ...]` |

## DSL Usage

```python
flow.recall_static(
    items=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
