# 可观测性

## MVP 必须

### 1. 白盒化回查

通过请求标识（如 uid / request_id）追踪某条请求的完整执行情况：

- 经过了哪些算子
- 每个算子的耗时
- 每个算子的输入输出数据快照

引擎在每次 DAG 执行时自动记录这些信息。回查时按请求标识检索历史记录，还原完整的执行链路。

#### Trace 返回控制

引擎内部始终记录每个算子的 trace（名称、耗时、是否 skip）。但 HTTP 响应中默认不返回 trace，以减少传输体积。

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

### 2. 代码治理

自动检测和报告无用算子/分支：

- **Apple 侧（静态）**：配合 flow output 契约的死代码消除，在生成 JSON 时报错（已在 02_flow_abstraction.md 中定义）。
- **Pine 侧（运行时）**：统计每个算子和控制分支的实际执行情况，定期生成报告。长期未被执行的分支或算子标记为可清理候选。

### 3. 算子 debug 参数

所有算子都有一个可选的 `debug` 参数（默认 `False`），与 `common_input` / `item_input` / `common_output` / `item_output` 同级，属于通用参数。

开启后，该算子在运行时打印调试日志（输入数据、输出数据、耗时等详细信息）。

```python
flow.enrich_by_lua(
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
  "enrich_by_lua_D4E5F6": {
    "type_name": "lua",
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

算子在 `Execute()` 中可以通过 `OperatorInput` 提供的方法感知 debug 状态并打印自定义日志：

- `input.Debug() bool` — 返回当前算子的 debug 开关状态
- `input.Log(format string, args ...any)` — 仅在 `debug=true` 时打印日志，自动附加算子名前缀 `[pine:debug] operator="<name>"`；`debug=false` 时静默无操作

使用示例：

```go
func (o *MyOp) Execute(ctx context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
    if in.Debug() {
        in.Log("custom state: threshold=%v, item_count=%d", o.threshold, in.ItemCount())
    }
    // ... 正常逻辑 ...
    return nil
}
```

引擎在调度时将 debug 标志和算子名注入 `OperatorInput`，算子无需自行构造 logger。

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

## 后续再做

- **DAG 可视化**：图形化展示 DAG 结构，由粗到细的业务流程展示。
- **实时监控面板**：算子级别的 CPU 消耗、耗时分布等系统指标。
- **全链路特征追踪**：跨服务的数据血缘追踪。
