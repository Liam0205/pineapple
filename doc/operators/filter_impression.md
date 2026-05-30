# filter_impression

**Type**: Filter

Benchmark stub: simulates impression-based filtering.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| bench_profile | any | No | - | Latency profile. |
| min_remaining_ratio | float | No | `1.5` | Stub param. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | - |
| CommonOutput | - |
| ItemInput | - |
| ItemOutput | - |

## DSL Usage

```python
flow.filter_impression(
    bench_profile=...,
    min_remaining_ratio=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
