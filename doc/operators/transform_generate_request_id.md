# transform_generate_request_id

**Type**: Transform

Benchmark stub: generates a fixed request ID.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| bench_profile | any | No | - | Latency profile. |
| prefix | string | No | `"bench"` | Stub param. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | - |
| CommonOutput | - |
| ItemInput | - |
| ItemOutput | - |

## DSL Usage

```python
flow.transform_generate_request_id(
    bench_profile=...,
    prefix=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
