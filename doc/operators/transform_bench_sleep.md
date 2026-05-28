# transform_bench_sleep

**Type**: Transform

Benchmark-only I/O-simulating operator. Sleeps for delay_ms per invocation.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| delay_ms | int64 | No | `5` | Milliseconds to sleep per operator invocation. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | - |
| CommonOutput | - |
| ItemInput | - |
| ItemOutput | - |

## DSL Usage

```python
flow.transform_bench_sleep(
    delay_ms=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
