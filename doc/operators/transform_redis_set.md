# transform_redis_set

**Type**: Transform

Generic Redis write operator. Writes a value by key with optional TTL.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| data_type | string | No | `"string"` | Redis data type: "set", "string", or "list". |
| fail_on_error | bool | No | `False` | Return fatal error on Redis infrastructure failure instead of logging and continuing. |
| key_prefix | string | Yes | - | Key prefix prepended to the suffix built from common_input fields. |
| redis_addr | string | Yes | - | Redis server address (host:port). |
| redis_db | int | No | `0` | Redis DB number. |
| redis_password | string | No | `""` | Redis password. |
| ttl | int | No | `0` | TTL in seconds. 0 means no expiry. |

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
    data_type=...,
    fail_on_error=...,
    key_prefix=...,
    redis_addr=...,
    redis_db=...,
    redis_password=...,
    ttl=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
