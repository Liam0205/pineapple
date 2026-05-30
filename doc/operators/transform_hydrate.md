# transform_hydrate

**Type**: Transform

Benchmark stub: simulates MySQL hydration.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| bench_profile | any | No | - | Latency profile. |
| mysql_dsn | string | No | `""` | Stub param. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | - |
| CommonOutput | - |
| ItemInput | - |
| ItemOutput | - |

## DSL Usage

```python
flow.transform_hydrate(
    bench_profile=...,
    mysql_dsn=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
