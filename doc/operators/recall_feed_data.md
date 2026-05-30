# recall_feed_data

**Type**: Recall

Benchmark stub: generates synthetic feed items.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| bench_item_count | int | No | `3000` | Number of items to generate. |
| bench_profile | any | No | - | Latency profile: {p50:[mean,max], p99:[mean,max], type:cpu|io}. |
| resource_name | string | No | `""` | Ignored in stub. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | - |
| CommonOutput | - |
| ItemInput | - |
| ItemOutput | - |

## DSL Usage

```python
flow.recall_feed_data(
    bench_item_count=...,
    bench_profile=...,
    resource_name=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
