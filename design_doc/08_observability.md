# 可观测性

## MVP 必须

### 1. 白盒化回查

通过请求标识（如 uid / request_id）追踪某条请求的完整执行情况：

- 经过了哪些算子
- 每个算子的耗时
- 每个算子的输入输出数据快照

引擎在每次 DAG 执行时自动记录这些信息。回查时按请求标识检索历史记录，还原完整的执行链路。

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
