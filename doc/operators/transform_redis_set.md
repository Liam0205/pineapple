# transform_redis_set

**Type**: Transform

Generic Redis write operator. Writes a value by key with optional TTL.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| key_prefix | string | Yes | - | Key prefix prepended to the suffix built from common_input fields. Supports {{field}} interpolation. |
| resource_name | string | Yes | - | Name of a redis_connection resource to borrow the client from. |
| data_type | string | No | `"string"` | Redis data type: "set", "string", or "list". |
| fail_on_error | bool | No | `False` | Return fatal error on Redis infrastructure failure instead of logging and continuing. |
| ttl | int | No | `0` | TTL in seconds. 0 means no expiry. Supports {{field}} interpolation. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | `[<key_suffix_fields...>, <value_field>]` |
| CommonOutput | `[]` |
| ItemInput | `[]` |
| ItemOutput | `[]` |

## DSL Usage

```python
flow.transform_redis_set(
    key_prefix=...,
    resource_name=...,
    data_type=...,
    fail_on_error=...,
    ttl=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
