# 流程抽象

## 算子 (Operator)

算子是 Pineapple 的基本计算单元。

### 分类

| 类型 | 维护方 | 说明 |
|------|--------|------|
| 通用算子 | 工程架构团队 | 过滤、召回、模型预估等，各业务直接复用 |
| 自定义算子 | 业务/算法团队 | 满足通用算子无法覆盖的定制化逻辑 |
| Lua 算子 | 业务/算法团队 | 专门的算子类型，接受 Lua 脚本作为配置，无需编写新 Go 算子即可实现特定逻辑 |

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

### 同名字段的写入规则

同一个字段可以被多个算子输出（如对 score 做多轮调整），但必须遵循以下规则：

**写已存在字段但未声明读取 → DSL 解析时报错。**

```python
# 错误: op_c 输出 foo 但未读取 foo，而 foo 已被 op_a 输出
flow.op_a(common_output=["foo"]) \
    .op_b(common_input=["foo"], common_output=["bar"]) \
    .op_c(common_output=["foo"])  # ❌ 报错
```

用户必须二选一：
- 写入新字段名，避免冲突
- 显式声明读取该字段（`common_input=["foo"]`），明确依赖

**读且写同名字段 → 合法，引擎自动处理依赖。**

```python
# 正确: op_a 和 op_b 都读写 foo，依赖链明确
flow.op_a(common_input=["foo"], common_output=["foo"]) \
    .op_b(common_input=["foo"], common_output=["foo"])  # ✅ 合法
```

### 数据冒险处理

借鉴计算机体系结构中的数据冒险 (Data Hazard) 概念，引擎在 DAG 构建时自动处理三种冒险：

| 类型 | 含义 | 引擎行为 |
|------|------|----------|
| RAW (Read-After-Write) | 读者依赖写者 | 自动建立依赖：读者等待写者完成 |
| WAW (Write-After-Write) | 后写者依赖前写者 | 自动建立依赖：按 DSL 顺序串行化同字段写入 |
| WAR (Write-After-Read) | 写者等读者先读完 | 自动建立依赖：写者等待所有先序读者完成 |

依赖推导基于 **DSL 编排顺序**：对同一字段，引擎追踪该字段最近的写者和所有未被后续写覆盖的读者，自动添加依赖边。

### DSL 编排顺序的语义

由于同名字段的读写依赖于 DSL 顺序，**编排顺序不只是语法糖，它参与了依赖关系的推导**。当同一字段被多次输出时，顺序决定了"读的是哪个版本"。

全流程漂移仍然成立：调换算子顺序会导致 DAG 自动重新推导。但需注意，如果涉及同名字段的读写，调换顺序可能改变语义（读到不同版本的数据）。

### 简单示例

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

## Lua 算子

Lua 算子是一种专门的算子类型，允许业务/算法同学通过编写 Lua 脚本实现轻量逻辑，无需开发新的 Go 算子。

### 数据流

```
DataFrame ──(Go 按算子配置取列)──▶ Go ──▶ Lua 脚本 ──▶ Go ──(更新 DataFrame)──▶ DataFrame
```

### 设计要点

- Lua 运行在沙箱中，**不直接访问 DataFrame**。
- Go 层根据算子的 input 配置，从 DataFrame 中提取相应列数据传入 Lua。
- Lua 完成计算后，将结果返回给 Go。
- Go 层负责将结果写回 DataFrame（按 output 配置）。
- 数据进出全程由 Go 管控，确保 DataFrame 的并发安全和数据一致性。

### DSL 示例

**行模式（默认）** — 直觉友好，逐 item 处理：

```python
flow.lua_op(
    common_input=["user_age", "user_gender"],
    item_input=["item_category", "item_price"],
    item_defaults={"item_price": 0.0},
    item_output=["item_adjusted_score"],
    script="""
        function handler(common, items)
            local age = common["user_age"]
            for i, item in ipairs(items) do
                if age < 18 then
                    item["item_adjusted_score"] = item["item_price"] * 0.8
                else
                    item["item_adjusted_score"] = item["item_price"]
                end
            end
            return nil, items
        end
    """,
)
```

**列模式** — 性能优化，按列处理：

```python
flow.lua_op(
    common_input=["user_age"],
    item_input=["item_price"],
    item_output=["item_adjusted_score"],
    columnar=True,
    script="""
        function handler(common, item_columns)
            local prices = item_columns["item_price"]
            local scores = {}
            for i = 1, #prices do
                scores[i] = prices[i] * (common["user_age"] < 18 and 0.8 or 1.0)
            end
            return nil, {item_adjusted_score = scores}
        end
    """,
)
```

### 行模式 vs 列模式

| | 行模式 (默认) | 列模式 (`columnar=True`) |
|---|---|---|
| items 参数 | `[]map[string]any` — 每个元素是一个 item 的特征 map | `map[string][]any` — 每个 key 对应一列值的数组 |
| items 返回值 | `[]map[string]any` 或 nil | `map[string][]any` 或 nil |
| 优点 | 直觉友好，逐 item 处理 | 减少 Lua table 分配，与 DataFrame 列存对齐 |
| 适用场景 | 默认选择 | item 数量大、字段多时的性能优化 |

common 参数和返回值在两种模式下相同，都是 `map[string]any` 或 nil。

### handler 函数签名

Lua 脚本必须定义一个名为 `handler` 的函数：

**行模式：**
```lua
function handler(common, items)
    -- common: table {field_name -> value}，或 nil（无 common_input 时）
    -- items:  table 的数组 [{field_name -> value}, ...]，或 nil（无 item_input 时）
    -- 返回: common_out (table 或 nil), items_out (table 数组或 nil)
    return common_out, items_out
end
```

**列模式：**
```lua
function handler(common, item_columns)
    -- common:       table {field_name -> value}，或 nil
    -- item_columns: table {field_name -> [values...]}，或 nil
    -- 返回: common_out (table 或 nil), item_columns_out (table {field_name -> [values...]} 或 nil)
    return common_out, item_columns_out
end
```

Lua 代码以字符串形式直接写在 Python DSL 中，作为 `script` 参数传入。最终序列化到 JSON 配置中，由 Go 引擎在运行时加载执行。无需管理外部 Lua 脚本文件。
