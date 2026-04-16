# 流程抽象

## 算子 (Operator)

算子是 Pineapple 的基本计算单元。

### 分类

| 类型 | 维护方 | 说明 |
|------|--------|------|
| 通用算子 | 工程架构团队 | 过滤、召回、模型预估等，各业务直接复用 |
| 自定义算子 | 业务/算法团队 | 满足通用算子无法覆盖的定制化逻辑 |
| Lua 增强算子 | 业务/算法团队 | 通用算子内嵌 Lua 脚本，无需编写新 Go 算子即可实现特定逻辑 |

### 算子接口

每个算子在 DSL 调用时声明：
- **common_input / common_output**: 读写的 common 侧数据字段
- **item_input / item_output**: 读写的 item 侧数据字段
- **配置参数**: 算子行为的可配置项（业务参数）

输入输出声明在 **DSL 侧**（非 Go 算子注册侧），使同一算子类型在不同场景下可以灵活地读写不同字段。

### DSL 示例

```python
flow = SomeFlowClass(name="example")

flow.op_a(
    common_input=["common_foo", "common_bar"],
    common_output=["common_baz"],
    item_input=["item_foo"],
    item_output=["item_baz"],
    other_params="some_value"
) \
.op_b(
    common_input=["common_baz"],
    item_input=["item_baz"],
    item_output=["item_qux"],
    other_params="some_value"
)
```

## DAG 构建

采用 **数据驱动的隐式构图**（DragonFly 方式）：

1. 每个算子在 DSL 中声明自己的输入和输出数据字段。
2. 引擎匹配各算子的输入/输出字段名，自动推导出依赖关系和 DAG 拓扑。
3. 无数据依赖的算子自动并行执行。

"隐式"指 DAG 的边由引擎推导，而非用户手动指定算子间的依赖。

### 示例

```
op_a: 输出 [common_baz, item_baz]
op_b: 输入 [common_baz, item_baz], 输出 [item_qux]
op_c: 输入 [item_baz], 输出 [item_score]
```

推导出的 DAG：
```
op_a ──▶ op_b
  └────▶ op_c     ← op_b、op_c 并行
```

### 全流程漂移

由于 DAG 完全由数据依赖决定，算子在 DSL 中的编排顺序改变时，DAG 结构会自动重新推导。这使得流程编排非常灵活。

## DAG 调度

- 基于拓扑排序执行。
- 无依赖的算子并行调度。
- 目标：无锁设计，支持高并发场景。

## 分层解耦

```
┌─────────────────────────────┐
│   算法工作空间 (Python DSL)   │  编排算子、提交配置
├─────────────────────────────┤
│        JSON 配置 (契约)       │
├─────────────────────────────┤
│   架构工作空间 (Go 算子)      │  实现算子、提供引擎二进制
└─────────────────────────────┘
```

算法团队与架构团队通过 JSON 配置解耦，互不干扰。
