# filter_blocked_creator

**Type**: Filter

Benchmark stub: simulates blocked-creator filtering.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| bench_profile | any | No | - | Latency profile. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | - |
| CommonOutput | - |
| ItemInput | - |
| ItemOutput | - |

## DSL Usage

```python
flow.filter_blocked_creator(
    bench_profile=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
