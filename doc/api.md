# API 参考

## `POST /execute`

执行 pipeline。

**请求体：**

```json
{
  "common": {"user_id": "123", "user_age": 25},
  "items": []
}
```

**响应体：**

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

Trace 通过 `common._return_trace = true` 控制是否返回。

## `GET /health`

健康检查。返回 `{"status": "ok"}`。

## `GET /stats`

引擎运行统计：

```json
{
  "operators": {"<name>": {"exec_count": 100, "skip_count": 0}},
  "scheduler": {"run_count": 100, "peak_concurrency": 4},
  "server": {"reload_count": 3, "reload_error_count": 0, "last_reload_duration_ns": 5234000},
  "operator_detail": {"<name>": {"borrow_count": 100}}
}
```

- `operators`：per-operator 累计统计
- `scheduler`：调度器级统计
- `server`：配置热重载统计
- `operator_detail`：实现 `StatsProvider` 接口的算子自定义统计

## `GET /dag`

返回 DAG 结构可视化。

| 参数 | 值 | 说明 |
|------|----|------|
| `format` | `dot`（默认）/ `mermaid` | 输出格式 |
| `collapse` | `0`（默认）/ `1` / `2` / ... | 按 SubFlow 层级折叠 |

```bash
curl -s http://localhost:8080/dag | dot -Tsvg -o dag.svg
curl http://localhost:8080/dag?format=mermaid
curl http://localhost:8080/dag?format=dot&collapse=1
```

## 可观测性

### 内置统计

`GET /stats` 端点基于 atomic 计数器，零外部依赖。

### Prometheus 接入

通过 `pkg/metrics.Provider` 接口支持外部指标导出，核心库不依赖 `prometheus/client_golang`：

```go
mp := promadapter.New(prometheus.DefaultRegisterer)
server.Run(server.Config{
    ConfigPath: *configPath,
    Addr:       *addr,
    Metrics:    mp,
})
```

### 自定义算子指标

算子可选实现 `StatsProvider` 接口暴露自定义统计到 `/stats`，或实现 `MetricsAware` 接口注册 Prometheus 指标。

详见 [可观测性设计文档](../design_doc/08_observability.md)。
