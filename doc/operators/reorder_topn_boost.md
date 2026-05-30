# reorder_topn_boost

**Type**: Reorder

Benchmark stub: simulates top-N boost reordering.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| bench_profile | any | No | - | Latency profile. |
| size | int | No | `10` | Stub param. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | - |
| CommonOutput | - |
| ItemInput | - |
| ItemOutput | - |

## DSL Usage

```python
flow.reorder_topn_boost(
    bench_profile=...,
    size=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
