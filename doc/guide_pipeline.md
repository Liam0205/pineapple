# Pipeline 编写指南（算法视角）

## 基本用法

```python
from apple.flow import Flow

flow = Flow(
    name="my_pipeline",
    common_input=["user_id", "user_age"],   # 请求级上下文字段
    item_output=["item_id", "item_score"],  # 最终输出字段
)
```

## 链式调用算子

所有算子方法返回 Flow 自身，支持链式调用：

```python
flow.recall_static(
    item_output=["item_id", "item_score"],
    items=[...],
)

flow.filter_condition(
    item_input=["item_status"],
    field="item_status",
    value="offline",
)

flow.transform_normalize(
    item_input=["item_score"],
    item_output=["item_score_norm"],
    field="item_score",
)

flow.filter_truncate(top_n=50)

flow.reorder_sort(
    item_input=["item_score_norm"],
    field="item_score_norm",
    order="desc",
)
```

## 条件分支

条件中的字段引用使用 `{{field_name}}` 模板语法：

```python
flow.if_("{{is_new_user}}") \
    .transform_dispatch(
        common_input=["default_score"],
        item_output=["item_score"],
        common_field="default_score",
        item_field="item_score",
    ) \
.else_() \
    .transform_by_lua(
        common_input=["user_id"],
        item_input=["item_id"],
        item_output=["item_score"],
        lua_script="...",
        function_for_item="score",
    ) \
.end_if_()
```

## SubFlow 组合与嵌套

SubFlow 支持任意深度嵌套，同一 SubFlow 内允许 ops 与子 SubFlow 自由穿插：

```python
from apple.flow import Flow, SubFlow

candidates = SubFlow(name="candidates")
candidates.recall_static(item_output=["item_id", "item_score"], items=[...])

recall = SubFlow(name="recall")
recall.add_subflow(candidates)
recall.merge_all(item_input=["item_id"], item_output=["item_id"])

process = SubFlow(name="process")
process.transform_normalize(item_input=["item_score"], item_output=["norm_score"], field="item_score")

flow = Flow(
    name="main",
    common_input=["user_id"],
    item_output=["item_id", "norm_score"],
    sub_flows=[recall, process],
)
```

编译后，SubFlow 路径用 `/` 分隔表示层级关系（如 `recall/candidates`）。

## 分支内嵌套 SubFlow

SubFlow 可以嵌套在条件分支内。编译器自动将外层分支的控制字段传播到 SubFlow 内所有算子：

```python
ranking = SubFlow(name="ranking")
ranking.reorder_sort(item_input=["item_score"], field="item_score", order="desc")

flow.if_("{{enabled}}") \
    .add_subflow(ranking) \
.else_() \
    .transform_dispatch(...) \
.end_if_()
```

## 资源声明

当算子依赖外部数据时，在 pipeline 中声明资源：

```python
from apple_generated.resources import FeatureIndexResource

flow.resource("my_index", FeatureIndexResource(dsn="host:3306/db"))

flow.recall_feature_index(
    resource_name="my_index",
    item_output=["item_id", "score"],
)
```

编译器校验所有 `resource_name` 引用是否有匹配的资源声明。

## Metadata 声明

每个算子调用需要声明它读写的字段：

| 参数 | 含义 |
|------|------|
| `common_input` | 读取的请求级字段 |
| `common_output` | 写入的请求级字段 |
| `item_input` | 读取的物品级字段 |
| `item_output` | 写入的物品级字段 |
| `item_defaults` | 物品级字段默认值 |
| `common_defaults` | 请求级字段默认值 |
| `sources` | 合并算子的数据来源 |
| `debug` | 启用此算子的调试快照 |
| `data_parallel` | 数据并行分片数（仅 Transform，需空 common_output） |

## 编译和校验

```python
json_str = flow.compile()       # 编译为 JSON 字符串
config = flow.compile_dict()    # 编译为 dict
```

编译器自动执行以下校验：

- **字段覆盖** — 算子读取的字段必须有上游产出
- **死代码检测** — 产出字段未被下游消费的算子会被标记
- **写后覆写** — 检测同一字段被多次写入
- **控制流完整性** — `if_` 必须有对应的 `end_if_`
- **空分支检测** — 控制块的每个分支必须有至少一个业务算子或 SubFlow
- **数据并行约束** — `data_parallel > 1` 时必须是 Transform 且 `common_output` 为空
- **参数-元数据一致性** — 业务参数与元数据声明不匹配时报错
- **报错定位** — 校验错误附带算子所在的 SubFlow 路径和源码位置
