# observe_datahub

**Type**: Observe

Benchmark stub: simulates DataHub MQ write.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| bench_profile | any | No | - | Latency profile. |
| key_fields | array | No | - | Stub param. |
| mode | string | No | `""` | Stub param. |
| resource_name | string | No | `""` | Stub param. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | - |
| CommonOutput | - |
| ItemInput | - |
| ItemOutput | - |

## DSL Usage

```python
flow.observe_datahub(
    bench_profile=...,
    key_fields=...,
    mode=...,
    resource_name=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
