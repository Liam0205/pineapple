# API Reference

## `POST /execute`

Execute the pipeline.

**Request body:**

```json
{
  "common": {"user_id": "123", "user_age": 25},
  "items": []
}
```

**Response body:**

```json
{
  "common": {"user_id": "123", "user_age": 25},
  "items": [
    {"item_id": "a", "item_score": 0.95}
  ],
  "warnings": [],
  "trace": [
    {"name": "recall_static_ABA9A7", "duration_ms": 0.123, "skipped": false}
  ]
}
```

Trace output is controlled by `common._return_trace = true`.

## `GET /health`

Health check. Returns `{"status": "ok"}`.

## `GET /stats`

Engine runtime statistics:

```json
{
  "operators": {"<name>": {"exec_count": 100, "skip_count": 0}},
  "scheduler": {"run_count": 100, "peak_concurrency": 4},
  "server": {"reload_count": 3, "reload_error_count": 0, "last_reload_duration_ns": 5234000},
  "operator_detail": {"<name>": {"borrow_count": 100}}
}
```

- `operators`: Per-operator cumulative statistics
- `scheduler`: Scheduler-level statistics
- `server`: Config hot-reload statistics
- `operator_detail`: Custom statistics from operators implementing `StatsProvider`

## `GET /dag`

Returns a visualization of the compiled DAG structure.

| Parameter | Values | Description |
|-----------|--------|-------------|
| `format` | `dot` (default) / `mermaid` | Output format |
| `collapse` | `0` (default) / `1` / `2` / ... | Collapse by SubFlow depth level |

```bash
curl -s http://localhost:8080/dag | dot -Tsvg -o dag.svg
curl http://localhost:8080/dag?format=mermaid
curl http://localhost:8080/dag?format=dot&collapse=1
```

## Observability

### Built-in Statistics

The `GET /stats` endpoint is always available, backed by atomic counters with zero external dependencies.

### Prometheus Integration

External metrics export is supported via the `pkg/metrics.Provider` interface. The core library has no dependency on `prometheus/client_golang`:

```go
mp := promadapter.New(prometheus.DefaultRegisterer)
server.Run(server.Config{
    ConfigPath: *configPath,
    Addr:       *addr,
    Metrics:    mp,
})
```

### Custom Operator Metrics

Operators may optionally implement `StatsProvider` to expose custom statistics to `/stats`, or implement `MetricsAware` to register custom Prometheus metrics.

See [Observability Design Doc](../design_doc/08_observability.md) for details.
