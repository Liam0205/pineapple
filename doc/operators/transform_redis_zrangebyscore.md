# transform_redis_zrangebyscore

**Type**: Transform

Benchmark stub: simulates Redis ZRANGEBYSCORE.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| bench_profile | any | No | - | Latency profile. |
| key_prefix | string | No | `""` | Stub param. |
| redis_addr | string | No | `""` | Stub param. |
| redis_password | string | No | `""` | Stub param. |
| window_seconds | int | No | `0` | Stub param. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | - |
| CommonOutput | - |
| ItemInput | - |
| ItemOutput | - |

## DSL Usage

```python
flow.transform_redis_zrangebyscore(
    bench_profile=...,
    key_prefix=...,
    redis_addr=...,
    redis_password=...,
    window_seconds=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
