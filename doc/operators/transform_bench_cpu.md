# transform_bench_cpu

**Type**: Transform

Benchmark-only CPU-bound operator. Computes iterative fib per item. Not available in pine-python.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| iterations | int | No | `100` | Number of fib(32) computations per item. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | - |
| CommonOutput | - |
| ItemInput | - |
| ItemOutput | - |

## DSL Usage

```python
flow.transform_bench_cpu(
    iterations=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
