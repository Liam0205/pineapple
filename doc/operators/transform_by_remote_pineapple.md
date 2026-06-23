# transform_by_remote_pineapple

**Type**: Transform

Calls a downstream Pineapple service and maps response fields back to the local frame.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| host | string | Yes | - | Downstream service host. |
| port | int64 | Yes | - | Downstream service port. |
| allow_private | bool | No | `False` | Allow connections to private/loopback addresses (dev/internal use). |
| common_request | any | No | - | Downstream common field names, positionally mapped to common_input. |
| common_response | any | No | - | Downstream common response field names, positionally mapped to common_output. |
| endpoint | string | No | `"/execute"` | Downstream endpoint path. |
| fail_on_error | bool | No | `True` | true=fatal on downstream error; false=warning and skip. |
| item_request | any | No | - | Downstream item field names, positionally mapped to item_input. |
| item_response | any | No | - | Downstream item response field names, positionally mapped to item_output. |
| max_response_size | int64 | No | `10485760` | Maximum response body size in bytes (default 10 MB). |
| timeout | float64 | No | `5` | Request timeout in seconds. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | `[<local_common_fields...>]` |
| CommonOutput | `[<local_common_output_fields...>]` |
| ItemInput | `[<local_item_fields...>]` |
| ItemOutput | `[<local_item_output_fields...>]` |

## DSL Usage

```python
flow.transform_by_remote_pineapple(
    host=...,
    port=...,
    allow_private=...,
    common_request=...,
    common_response=...,
    endpoint=...,
    fail_on_error=...,
    item_request=...,
    item_response=...,
    max_response_size=...,
    timeout=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
