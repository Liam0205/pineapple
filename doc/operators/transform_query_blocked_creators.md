# transform_query_blocked_creators

**Type**: Transform

Benchmark stub: simulates MySQL blocked-creators query.

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
flow.transform_query_blocked_creators(
    bench_profile=...,
    mysql_dsn=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
