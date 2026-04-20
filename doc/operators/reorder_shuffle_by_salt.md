# reorder_shuffle_by_salt

**Type**: Reorder

Deterministic hash-based shuffle using a caller-provided salt.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | `[<salt_fields...>]` |
| CommonOutput | `[]` |
| ItemInput | `[<item_key_field>]` |
| ItemOutput | `[]` |

## DSL Usage

```python
flow.reorder_shuffle_by_salt(
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
