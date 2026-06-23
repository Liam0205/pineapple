# transform_redis_get

**Type**: Transform

Generic Redis read operator. Reads a value by key and outputs the result and a cache-hit flag.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| key_prefix | string | Yes | - | Key prefix prepended to the suffix built from common_input fields. Supports {{field}} interpolation. |
| resource_name | string | Yes | - | Name of a redis_connection resource to borrow the client from. |
| data_type | string | No | `"string"` | Redis data type: "set", "string", or "list". |
| fail_on_error | bool | No | `False` | Return fatal error on Redis infrastructure failure instead of treating as cache miss. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | `[<key_suffix_fields...>]` |
| CommonOutput | `[<result_field>, <cache_hit_field>]` |
| ItemInput | `[]` |
| ItemOutput | `[]` |

## DSL Usage

```python
flow.transform_redis_get(
    key_prefix=...,
    resource_name=...,
    data_type=...,
    fail_on_error=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
