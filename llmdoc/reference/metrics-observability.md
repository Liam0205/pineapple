# 指标与观测参考

本文档描述 Pineapple 0.5.0 起稳定支持的可插拔观测模型：内建 `/stats` 原子统计始终可用，外部监控系统通过 `pine-go/pkg/metrics` Provider 抽象接入。

## 权威文件

- `pine-go/pkg/metrics/metrics.go`
- `pine-go/pkg/metrics/nop.go`
- `pine-go/pine.go`
- `pine-go/internal/runtime/engine_metrics.go`
- `pine-go/internal/runtime/scheduler.go`
- `pine-go/internal/runtime/stats.go`
- `pine-go/internal/types/operator.go`
- `pine-go/operators/lua/lua.go`
- `pine-go/operators/lua/pool.go`
- `pine-go/pkg/server/server.go`
- `pine-go/pkg/server/http_metrics.go`

## 设计目标

Pineapple 把生产可观测性拆成两条稳定通道：

1. 进程内原子统计
   - 零配置启用
   - 为 `/stats` 提供数据
   - 不依赖外部监控后端

2. 外部 Provider metrics
   - 由调用方按需注入
   - 面向 Prometheus 等采集系统
   - 不让 Pineapple core 直接依赖具体监控 SDK

该拆分允许 Pineapple 默认保持轻依赖，同时让接入方在自己的项目中实现约 80 行级别的 Prometheus adapter。

## `pine-go/pkg/metrics` 抽象面

`pine-go/pkg/metrics/metrics.go` 定义的稳定接口只有三类 metric 和一个 provider：

- `Counter`
- `Gauge`
- `Histogram`
- `Provider`

共同约束：

- metric handle 支持 `With(labelValues ...string)` 派生带标签实例
- `MetricOpts` / `HistogramOpts` 描述名称、帮助文案、标签名和 histogram buckets
- `DurationSeconds(time.Duration)` 把 Go duration 转成秒，供时长 histogram 使用

Pineapple 只依赖这些接口，不导入 `prometheus/client_golang`。

## 默认 provider

`metrics.Nop()` 返回默认 no-op provider：

- 丢弃全部观测
- 不分配真实后端对象
- 作为 `pine.NewEngine` 和 `server.Config.Metrics` 的默认值

因此未启用外部 metrics 时，Pineapple 仍保留 `/stats` 诊断能力，但不会把 Prometheus 等依赖强加给所有使用者。

## 引擎侧接入点

### `pine.NewEngine(jsonConfig, opts...)`

引擎构建支持 option 注入，当前稳定项为：

- `pine.WithMetrics(provider)`
- `pine.WithLogPrefix(prefix)`

行为约束：

- 未提供 provider 时自动回退到 `metrics.Nop()`
- `Engine` 在构建期预创建 `EngineMetrics`
- 引擎在构建期（`NewEngine`/`Engine.create()`/`Engine()` 初始化末尾）对所有已编译算子名调用 `PreInitOperators`，确保 Prometheus 等 metrics backend 从启动即暴露零值时间序列；同时 Stats 也预注册算子名，使 `/stats` 在首次请求前即可返回完整算子列表（各计数器初始为 0）
- 同一 provider 会被传给所有实现 `MetricsAware` 的算子实例
- 日志前缀可来自 JSON 根级 `log_prefix` 或 `pine.WithLogPrefix(...)`
- 当两者同时存在时，`pine.WithLogPrefix(...)` 优先
- 最终通过标准库 `log.SetPrefix()` 应用前缀，并调用 `log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)`，因此会影响引擎日志和复用标准库 logger 的算子日志，输出包含 `file:line`

### 可选接口注入顺序

对单个算子实例，引擎在构建期按以下固定顺序调用可选接口：

1. `MetadataAware`
2. `DebugAware`
3. `MetricsAware`

该顺序是承载性的。像 `pine-go/operators/lua/lua.go` 这样的实现依赖 `DebugHolder.OperatorName()` 已先被注入，以便把 operator 实例名作为 metric label 使用。

## 双通道观测模型

### 通道一：内建原子统计

`pine-go/internal/runtime/stats.go` 始终维护：

- 每算子执行次数、跳过次数、错误次数
- 每算子总耗时、最大耗时、平均耗时
- 调度器 `run_count`
- 调度器 `peak_concurrency`

这些统计通过以下 API 暴露：

- `Engine.Stats()`
- `Engine.SchedulerStats()`
- `Engine.OperatorCustomStats()`（聚合实现 `StatsProvider` 的算子）

### 通道二：外部 Provider metrics

`pine-go/internal/runtime/engine_metrics.go` 在引擎创建时预声明并缓存 metric handle。当前稳定指标名为：

**算子级指标：**

- `pine_scheduler_runs_total`
- `pine_operator_active`
- `pine_operator_exec_total{operator=...}`
- `pine_operator_exec_duration_seconds{operator=...}`
- `pine_operator_skip_total{operator=...}`
- `pine_operator_error_total{operator=...}`

**DAG 级指标（0.6.6 起）：**

- `pine_dag_executions_total{status=success|error}` — DAG 执行总次数，按成功/失败分标签
- `pine_dag_execution_duration_seconds` — 单次 DAG 执行端到端耗时
- `pine_dag_operators_executed` — 每次 DAG 执行中实际运行（非跳过、非取消）的算子数

DAG 级指标在 `scheduler.Run()` 结束时统一记录：计时覆盖从调度开始到所有算子完成的完整区间；`status` 标签由是否有 fatal error 决定；`operators_executed` 只计入 `!Skipped` 的 trace 条目。

`scheduler.go` 在热路径中同时更新两类观测：

- 调度开始时记录 scheduler run
- 算子开始/结束时维护 active gauge 与 peak concurrency
- 算子成功、跳过、失败时分别记录对应计数
- 成功和失败都记录执行时长 histogram
- DAG 执行结束时记录 DAG 级执行次数、耗时和实际执行算子数

这里的关键不是二选一，而是同一执行事实被写入两种消费面：

- `/stats` 面向人类诊断与零配置自检
- provider metrics 面向 Prometheus/Grafana 等系统

## 服务端观测

`pine-go/pkg/server/server.go` 支持通过 `server.Config.Metrics` 注入 provider，也支持通过 `server.Config.Middlewares` 在 HTTP 边界层包装整个 handler 链。

该 provider 会同时用于：

- 初始 `pine.NewEngine(configData, pine.WithMetrics(provider))`
- 后续 `reloadConfig(...)` 重建引擎时的 `pine.WithMetrics(provider)`
- server 自身的 config reload 指标
- 内置 HTTP 请求指标中间件

### 内置 HTTP 请求指标中间件（0.6.6 起）

`pine-go/pkg/server/http_metrics.go` 提供内置的 `httpMetricsMiddleware`，作为**最内层**中间件自动应用于所有 HTTP 路由，测量 handler 处理耗时（不含用户 middleware 开销）。

指标名：

- `pine_http_requests_total{method, path, status}` — HTTP 请求总数
- `pine_http_request_duration_seconds{method, path}` — HTTP 请求处理耗时

行为约定：

- `path` 标签只输出已知路径（`/execute`、`/health`、`/stats`、`/dag`），未知路径归一化为 `_other`，防止高基数标签爆炸
- `status` 标签按 HTTP 状态码桶化为 `2xx`、`3xx`、`4xx`、`5xx`、`other`
- 该中间件在用户 `Middlewares` 之内、`http.ServeMux` 路由之外包装，因此用户 middleware 的处理时间不会被计入 `request_duration`
- 当 `server.Config.Metrics` 为 nil 时，使用 `metrics.Nop()` 作为 provider，此时中间件仍执行但观测被丢弃，开销可忽略

`Middlewares` 与 metrics 注入是正交能力：middleware 包装发生在内部路由注册完成之后、`ListenAndServe` 启动之前，对 `/health`、`/execute`、`/stats`、`/dag` 一并生效，但不改变 `/stats` 数据来源、reload 计数逻辑或引擎内的 metrics provider 传递。

当前 server 级外部指标名：

- `pine_config_reload_total`
- `pine_config_reload_errors_total`
- `pine_config_reload_duration_seconds`

同时，server 还维护原子 reload 统计：

- `reloadCount`
- `reloadErrorCount`
- `lastReloadDurationNs`

## `/stats` 响应契约

`GET /stats` 返回组合响应，字段稳定分为：

- `operators` — 来自 `Engine.Stats()` 的每算子累计统计
- `scheduler` — 来自 `Engine.SchedulerStats()` 的调度器统计
- `server` — 配置热加载相关统计
- `operator_detail` — 仅当至少一个算子实现 `StatsProvider` 时出现

这意味着：

- `/stats` 不要求 provider
- `/stats` 与 Prometheus export 不是同一个接口层
- 自定义算子若只想支持 `/stats`，实现 `StatsProvider` 即可
- 自定义算子若还想接入外部指标，再额外实现 `MetricsAware`

## 算子扩展点

### `StatsProvider`

定义于 `pine-go/internal/types/operator.go`，用于把算子内部累计统计暴露给 `/stats`：

- 方法：`OperatorStats() map[string]int64`
- 数据应是稳定、便于诊断的累计数值
- 推荐用于 pool、cache、worker、重试等内部子系统状态

### `MetricsAware`

定义于 `pine-go/internal/types/operator.go`，用于接收外部 provider：

- 方法：`SetMetricsProvider(p metrics.Provider)`
- 适合在算子内部预创建或缓存自己的 metric handle
- 实现必须接受 no-op provider 为正常情况

## Lua 算子作为参考实现

Lua runtime 现在同时实现两种扩展接口：

- `StatsProvider`
- `MetricsAware`

### `/stats` 侧

`pine-go/operators/lua/pool.go` 通过原子计数维护：

- `borrow_count`
- `return_count`
- `create_count`
- `active_count`

`pine-go/operators/lua/lua.go` 的 `OperatorStats()` 将这些计数暴露到 `/stats.operator_detail[operatorName]`。

### 外部 metrics 侧

Lua pool 还会创建以下 provider metrics，并绑定 `operator` label：

- `pine_lua_pool_borrow_total`
- `pine_lua_pool_return_total`
- `pine_lua_pool_create_total`
- `pine_lua_pool_active`

label 值取自 `DebugHolder.OperatorName()`，所以 Lua 算子的 metrics 注入依赖固定顺序：MetadataAware → DebugAware → MetricsAware。

## Prometheus 适配边界

Pineapple 仓库内不提供 Prometheus 具体实现。推荐模式是：

- 在业务项目里实现一个 `pkg/metrics.Provider` 适配器
- 在适配器内部使用 `prometheus/client_golang` 注册 `CounterVec`、`GaugeVec`、`HistogramVec`
- 应用启动时把适配器传给 `pine.WithMetrics(...)` 或 `server.Config.Metrics`

这样可保持 Pineapple core 的后端无关性，也避免把注册表、命名冲突和 exporter 路由绑定到核心库中。

## 跨引擎 metrics parity 验证

cross-validate section 13（`scripts/cross-validate/13-metrics-parity.sh`）验证三引擎的 pre-init 行为和 `/stats` 数值一致性，覆盖 6 项检查：

- zero-traffic pre-init：引擎启动后、无请求时 `/stats` 已暴露全部算子
- operator names match：三引擎的算子名集合一致
- exec_count match：执行计数三引擎一致
- skip_count match：跳过计数三引擎一致
- error_count match：错误计数三引擎一致
- scheduler.run_count match：调度器运行计数三引擎一致

## 版本边界

本文档描述的可插拔 metrics / observability 模型自 Pineapple `0.5.0` 起稳定存在。`0.6.6` 新增内置 HTTP 请求指标中间件和 DAG 级执行指标。涉及的公开入口包括：

- `pine.WithMetrics`
- `pine.WithLogPrefix`
- `server.Config.Metrics`
- `/stats` 的 `scheduler` 与 `operator_detail` 组合语义

## 检索指针

- 接口定义：`pine-go/pkg/metrics/metrics.go`
- no-op 默认实现：`pine-go/pkg/metrics/nop.go`
- 引擎入口与 option：`pine-go/pine.go`
- 引擎级 metric 预创建：`pine-go/internal/runtime/engine_metrics.go`
- 调度热路径记录：`pine-go/internal/runtime/scheduler.go`
- 原子统计：`pine-go/internal/runtime/stats.go`
- 算子扩展接口：`pine-go/internal/types/operator.go`
- Lua 参考实现：`pine-go/operators/lua/lua.go`、`pine-go/operators/lua/pool.go`
- HTTP `/stats` 与 reload 指标：`pine-go/pkg/server/server.go`
- 内置 HTTP 请求指标中间件：`pine-go/pkg/server/http_metrics.go`
