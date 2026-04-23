# 可观测性

## MVP 必须

### 1. 白盒化回查

通过请求标识（如 uid / request_id）追踪某条请求的完整执行情况：

- 经过了哪些算子
- 每个算子的耗时
- 每个算子的输入输出数据快照

引擎在每次 DAG 执行时自动记录这些信息。回查时按请求标识检索历史记录，还原完整的执行链路。

#### Trace 返回控制

引擎内部始终记录每个算子的 trace（名称、开始时间、耗时、是否 skip）。但 HTTP 响应中默认不返回 trace，以减少传输体积。

请求方通过 common 字段 `_return_trace` 控制：

```json
{
  "common": {
    "_return_trace": true,
    ...
  },
  "items": []
}
```

- `_return_trace` 为 `true` → 响应包含 `trace` 数组
- `_return_trace` 缺失或为 `false`（默认） → 响应不含 `trace`

`_return_trace` 以 `_` 开头，属于引擎保留字段，不会被算子读取，不需要在 `common_input` 中声明。

`trace` 数组仅包含实际执行或被 skip 的算子条目。当 DAG 因算子报错而中止时，未开始执行的下游算子不会出现在 `trace` 中。

### 2. 代码治理

自动检测和报告无用算子/分支：

- **Apple 侧（静态）**：配合 flow output 契约的死代码消除，在生成 JSON 时报错（已在 02_flow_abstraction.md 中定义）。
- **Pine 侧（运行时）**：统计每个算子和控制分支的实际执行情况，定期生成报告。长期未被执行的分支或算子标记为可清理候选。

### 3. 算子 debug 参数

所有算子都有一个可选的 `debug` 参数（默认 `False`），与 `common_input` / `item_input` / `common_output` / `item_output` 同级，属于通用参数。

开启后，该算子在运行时打印调试日志（输入数据、输出数据、耗时等详细信息）。

```python
flow.transform_by_lua(
    common_input=["user_age"],
    item_input=["item_price"],
    function_for_item="adjust_price",
    item_output=["item_adjusted_score"],
    debug=True,  # 开启调试日志
    lua_script="""
        function adjust_price()
            return item_price * 0.8
        end
    """,
)
```

JSON 配置中体现为：

```json
{
  "transform_by_lua_D4E5F6": {
    "type_name": "transform_by_lua",
    "$metadata": {
      "common_input": ["user_age"],
      "item_input": ["item_price"],
      "item_output": ["item_adjusted_score"]
    },
    "debug": true,
    ...
  }
}
```

Pine 执行时，`debug: true` 的算子输出详细日志，`debug: false` 或未设置的算子不输出。

#### 引擎侧 debug 日志格式

引擎在 `debug: true` 的算子执行前后自动捕获输入/输出快照，并以 **JSON 序列化**格式打印到服务端日志。示例：

```text
[pine:debug] operator="transform_by_lua_A1B2C3" duration=1.234ms
  input: {"common":{"user_age":16},"items":[{"item_price":100},{"item_price":200}]}
  output: {"item_writes":{"0":{"item_adjusted_score":80},"1":{"item_adjusted_score":160}}}
```

输入/输出均经过 `json.Marshal` 序列化，保证可读且可直接用于 diff/分析。序列化失败时回退到 `%v`。

#### 算子侧 debug 访问

Debug 是算子的配置属性（编译时确定），不属于请求输入数据。引擎通过 `DebugAware` 接口在编译阶段注入 debug 标志和算子名，算子通过嵌入 `DebugHolder` 获得 `IsDebug()` 和 `DebugLog()` 能力。

与 `MetadataAware` / `MetadataHolder` 完全对称：

```go
type MyOp struct {
    pine.MetadataHolder
    pine.DebugHolder     // 嵌入即自动实现 DebugAware
    threshold float64
}

func (o *MyOp) Execute(ctx context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
    if o.IsDebug() {
        o.DebugLog("custom state: threshold=%v, item_count=%d", o.threshold, in.ItemCount())
    }
    // ... 正常逻辑 ...
    return nil
}
```

- `o.IsDebug() bool` — 返回当前算子的 debug 开关状态
- `o.DebugLog(format, args...)` — 仅在 `debug=true` 时打印日志，自动附加算子名前缀 `[pine:debug] operator="<name>"`；`debug=false` 时静默无操作

引擎在编译阶段调用 `SetDebugInfo(operatorName, debug)`，算子无需自行构造 logger。

### 通用算子参数汇总

以下参数所有算子类型共有：

| 参数 | 必选 | 默认值 | 说明 |
|------|------|--------|------|
| `common_input` | 否 | `[]` | 读取的 common 字段 |
| `common_output` | 否 | `[]` | 写入的 common 字段 |
| `item_input` | 否 | `[]` | 读取的 item 字段 |
| `item_output` | 否 | `[]` | 写入的 item 字段 |
| `common_defaults` | 否 | `{}` | common 字段的缺失值默认值 |
| `item_defaults` | 否 | `{}` | item 字段的缺失值默认值 |
| `debug` | 否 | `False` | 是否打印调试日志 |

## 暂不计划

- **全链路特征追踪**：跨服务的数据血缘追踪。超出单引擎范畴，需分布式 tracing 基础设施支撑。

## 已实现

### 运行时指标体系

引擎通过 `pkg/metrics` 提供可插拔的指标接口（Counter / Gauge / Histogram），默认使用零开销的 Nop 实现。用户通过 `pine.WithMetrics(provider)` 注入自定义 Provider（如 Prometheus 适配器），即可将指标导出到外部监控系统。

同时，引擎内部保留 atomic 计数器作为独立的内置观测路径，通过 `/stats` JSON 端点始终可用，不依赖任何外部系统。

#### 指标覆盖范围

**调度器级**

| 指标 | 类型 | 说明 |
|------|------|------|
| `pine_scheduler_runs_total` | Counter | DAG 调度执行总次数 |
| `pine_operator_active` | Gauge | 当前正在执行的算子数 |
| `pine_operator_exec_total` | Counter(operator) | 算子成功执行次数 |
| `pine_operator_exec_duration_seconds` | Histogram(operator) | 算子执行耗时分布 |
| `pine_operator_skip_total` | Counter(operator) | 算子跳过次数 |
| `pine_operator_error_total` | Counter(operator) | 算子失败次数 |

**Lua pool 级**

| 指标 | 类型 | 说明 |
|------|------|------|
| `pine_lua_pool_borrow_total` | Counter(operator) | Lua state 借出总次数 |
| `pine_lua_pool_return_total` | Counter(operator) | Lua state 归还总次数 |
| `pine_lua_pool_create_total` | Counter(operator) | Lua state 创建总次数 |
| `pine_lua_pool_active` | Gauge(operator) | 当前借出的 Lua state 数 |

**配置热重载级**

| 指标 | 类型 | 说明 |
|------|------|------|
| `pine_config_reload_total` | Counter | 配置重载成功次数 |
| `pine_config_reload_errors_total` | Counter | 配置重载失败次数 |
| `pine_config_reload_duration_seconds` | Histogram | 配置重载耗时分布 |

#### Metrics Provider 接口

```go
import "github.com/Liam0205/pineapple/pkg/metrics"

type Provider interface {
    NewCounter(opts MetricOpts) Counter
    NewGauge(opts MetricOpts) Gauge
    NewHistogram(opts HistogramOpts) Histogram
}
```

接口设计对齐 Prometheus mental model：`With(labelValues...)` 按位置传值、`MetricOpts.LabelNames` 声明标签名、`Histogram.Observe(float64)` 配合 `metrics.DurationSeconds` 转换。

Pineapple 核心库不依赖 `prometheus/client_golang`。Prometheus 适配器由用户在自己的项目中实现，约 80 行代码即可完成。

#### `/stats` 端点

`GET /stats` 返回复合结构：

```json
{
  "operators": {
    "recall_static_A1B2C3": {"exec_count": 100, "skip_count": 0, ...},
    "transform_by_lua_D4E5F6": {"exec_count": 100, ...}
  },
  "scheduler": {"run_count": 100, "peak_concurrency": 4},
  "server": {"reload_count": 3, "reload_error_count": 0, "last_reload_duration_ns": 5234000},
  "operator_detail": {
    "transform_by_lua_D4E5F6": {"borrow_count": 100, "return_count": 100, "create_count": 8, "active_count": 0}
  }
}
```

- `operators`：per-operator 累计统计（exec/skip/error/duration）
- `scheduler`：调度器级统计（运行次数、峰值并发）
- `server`：配置热重载统计
- `operator_detail`：实现 `StatsProvider` 接口的算子的自定义统计（如 Lua pool）

#### Prometheus 接入示例

第三方项目实现 `metrics.Provider` 接口，约 80 行：

```go
package promadapter

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/Liam0205/pineapple/pkg/metrics"
)

type provider struct{ r prometheus.Registerer }

func New(r prometheus.Registerer) metrics.Provider { return &provider{r: r} }

func (p *provider) NewCounter(opts metrics.MetricOpts) metrics.Counter {
    c := prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: opts.Name, Help: opts.Help,
    }, opts.LabelNames)
    p.r.MustRegister(c)
    return &counter{vec: c}
}

// NewGauge, NewHistogram 类似...

type counter struct {
    vec *prometheus.CounterVec
    c   prometheus.Counter
}

func (c *counter) With(lvs ...string) metrics.Counter {
    return &counter{c: c.vec.WithLabelValues(lvs...)}
}

func (c *counter) Inc() {
    if c.c != nil { c.c.Inc() }
}
```

在 server wrapper 中注入：

```go
mp := promadapter.New(prometheus.DefaultRegisterer)
server.Run(server.Config{
    ConfigPath: *configPath,
    Addr:       *addr,
    Metrics:    mp,
})
```

### DAG 可视化

通过 `Engine.RenderDAG(format)` 方法或 `GET /dag?format=dot|mermaid` HTTP 端点获取 DAG 结构的可视化表示。

支持两种输出格式：

- **DOT (Graphviz)**：标准图描述语言，可通过 `dot -Tsvg` 渲染为 SVG/PNG。
- **Mermaid**：可直接嵌入 Markdown / GitHub README，无需额外工具即可预览。

节点按算子类型着色，标签包含算子名和类型分类。

#### 编程 API

```go
engine, _ := pine.NewEngine(jsonConfig)
dot, _ := engine.RenderDAG("dot")       // Graphviz DOT
mmd, _ := engine.RenderDAG("mermaid")   // Mermaid flowchart
```

#### HTTP 端点

```bash
# DOT 格式（默认）
curl http://localhost:8080/dag

# Mermaid 格式
curl http://localhost:8080/dag?format=mermaid

# 渲染为 SVG（需要本地安装 graphviz）
curl -s http://localhost:8080/dag | dot -Tsvg -o dag.svg
```
