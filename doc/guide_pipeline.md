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

## Flow 级配置

除算子级 metadata，`Flow(...)` 还接受几个作用于整条流水线的参数：

| 参数 | 含义 |
|------|------|
| `storage_mode` | DataFrame 物理存储：`"row"`（默认）或 `"column"` |
| `log_prefix` | 引擎实例级日志前缀，只作用于该引擎私有 logger |
| `debug` | `True` 时对所有算子启用 debug 快照采集 |
| `skip_dead_code` | `True` 时放过死代码校验（产出字段无人消费不再报错） |

### 怎么选 storage_mode

两种模式的执行结果完全一样，选哪个只影响性能。判据是负载形状，不存在「列存更快」这种普适结论：

- **`column`**：transform 主导，即在同一批 item 上反复做字段级扫描和计算；item 数量大；recall 之后很少发生结构变更（增删 item、重排顺序）。
- **`row`**（默认）：recall / filter / sort 主导，或者 item 数量本来就小。

原因在于两种布局各自擅长的操作不同。列存把同名字段连续存放，所以整列扫描和整列写入很划算，构造和投影也更省分配；代价是增删 item 和重排顺序要动所有列，行存在这些操作上只需搬动整行引用。真实推荐场景里 item 数常在十几个量级、DAG 又以召回和排序为主，所以默认值是行存。

想在自己的负载上量化，用 `pine-go/benchmarks/bench_storage_ab_test.go` 里的 A/B 入口：

```bash
cd pine-go/benchmarks && go test -tags pine_bench -bench=BenchmarkStorageAB -run='^$' ./...
```

也可以把两份只差 `storage_mode` 的配置交给跨引擎压测脚本对跑：

```bash
scripts/bench-cross-runtime.sh --filter <fixture 名> --modes "row,column"
```

`storage_mode` 只接受 `"row"` 和 `"column"`（或省略／`null`，此时用默认值 `"row"`）。**其他任何值都会被拒绝**：

- **走 Apple DSL 时**在编译期被拒（`apple/flow.py` 的 `_VALID_STORAGE_MODES`）
- **手写 JSON 配置**在三个运行时的配置加载层被拒，错误文案三方逐字节相同：
  `storage_mode "colunm" is invalid, must be "row" or "column"`
  （逐字节相同这一结论对**不含引号／反斜杠／控制字符／不可打印 Unicode** 的值成立；
  这类值上 pine-go 用 `%q` 转义、另两方裸拼接，细节见 `llmdoc/reference/root-config-string-fields.md`）

非字符串值（数字、布尔、数组、对象）同样被三方一律拒绝。`null` 与省略该键等价，得到默认值。
注意上面那句「文案三方相同」只对**值**层（白名单）成立；**类型**层的文案 pine-go 与另两方不同
（pine-go 走 `encoding/json` 的 `JSON parse error: ...`，另两方是 `config field "X" must be a string`）。

issue #187 之前不是这样：非法字符串值会被三方**静默接受**并落到行存（方向由 issue #179 对齐），
非字符串值则三方行为各不相同。拼错字段值却得到一个能跑、但内存与性能特征与预期相反的引擎，
是这条改动要消除的问题。

```python
flow = Flow(
    name="my_pipeline",
    common_input=["user_id"],
    item_output=["item_id", "item_score"],
    storage_mode="column",
)
```

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
