# observe_log

**Type**: Observe

Reads declared input fields and writes them to Go standard log. This is a read-only operator: it produces no output fields and does not modify the DataFrame. It is exempt from dead-code detection.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| log_prefix | string | No | `""` | Prefix prepended to each log line. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | `[<fields to observe>]` |
| CommonOutput | `[]` |
| ItemInput | `[<fields to observe>]` |
| ItemOutput | `[]` |

## DSL Usage

```python
flow.observe_log(
    log_prefix=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
