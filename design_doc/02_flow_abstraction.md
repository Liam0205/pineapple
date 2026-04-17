# 流程抽象

## Flow

Flow 是一个完整的计算流程，由一组算子编排而成。Flow 定义了与服务层之间的输入输出契约。

### DSL 声明

```python
flow = Flow(
    name="recall_and_rank",
    # 输入: 必须显式声明，Pine 启动时校验服务层是否能提供
    common_input=["user_age", "user_id", "user_tags"],
    item_input=["item_id", "item_category", "item_price"],
    # 输出: 可选声明，用于检测无效算子
    common_output=["final_strategy"],
    item_output=["item_score", "item_rank"],
)
```

### 输入契约（必选）

`common_input` 和 `item_input` 必须显式声明，用于双向校验：

- **Pine 侧**: 引擎启动时校验服务层能否提供 flow 声明的所有 input。
- **Apple 侧**: DSL 解析时校验每个算子的输入字段，要么来自 flow 的 input，要么来自前序算子的 output。未被覆盖的字段 → 报错。

### 输出契约（可选）

`common_output` 和 `item_output` 为可选声明。如果声明了，Apple 据此检测无效算子：

- 从 flow 声明的 output 字段出发，反向追踪所有被依赖的算子。
- 叶子节点算子的输出如果未被 flow output 覆盖，且不被其他算子依赖 → 该算子无效。
- **检测到无效算子时，报错退出，拒绝生成 JSON 配置文件。**

此机制从源头防止无用算子膨胀，类似编译器的"死代码消除"。

### Flow 组合

大型流程可拆分为多个子 Flow 独立编写，最终组合成一个完整的 Pipeline。这是一种语法糖，方便对不同阶段分别编写和复用。

```python
# 各阶段独立编写
parse_sample = (
    SubFlow(name="parse_sample")
    .op_a(...)
    .op_b(...)
)

extract_features = (
    SubFlow(name="extract_features")
    .op_c(...)
    .op_d(...)
)

# 统合为完整 Pipeline
pipeline = Flow(
    name="recommend_pipeline",
    common_input=["user_id", "query"],
    item_output=["item_id", "item_score"],
    sub_flows=[parse_sample, extract_features],
)
```

**关键语义：**

- **DAG 跨 flow 推导**: 组合后，所有子 flow 的算子被打平到同一个 DAG 中。子 flow 的边界在 DAG 构建时是透明的。例如 `parse_sample` 输出 `foo`，`extract_features` 输入 `foo`，引擎自动推导出依赖关系。
- **子 flow 可复用**: 同一个子 flow 片段可被多个 pipeline 引用（如 batch 和 stream pipeline 共享 `parse_sample`）。
- **输入输出契约在顶层 Flow 上**: 子 flow 不声明输入输出契约，契约只在最终组合的 Flow 上定义。

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

### 核心规则

**一个 Lua 算子只做一件事：要么处理 common，要么处理 item。不允许同一个算子中混合两种类型。**

如果业务需要先算 common 再算 item，拆成两个算子，DAG 自动处理依赖关系。

### 数据流

```
DataFrame ──(Go 设置 Lua 全局变量)──▶ Lua 函数执行 ──(按位置返回)──▶ Go 写回 DataFrame
```

- Go 将输入字段设为 Lua 全局变量（非 table，直接用变量名访问）。
- Lua 函数的返回值按位置与 `common_output` / `item_output` 一一对应。
- 数据进出全程由 Go 管控。

### 类型一：处理 item 特征 (`function_for_item`)

Go 对每个 item 调用一次 Lua 函数。Common 字段作为标量全局变量始终可用（只读），item 字段为当前 item 的标量值。

```python
flow.enrich_by_lua(
    common_input=["user_age"],
    item_input=["item_price"],
    function_for_item="adjust_price",
    item_output=["item_adjusted_score"],
    lua_script="""
        function adjust_price()
            -- user_age: 标量, 来自 common (只读)
            -- item_price: 标量, 当前 item 的值
            if user_age < 18 then
                return item_price * 0.8
            else
                return item_price
            end
        end
    """,
)
```

**执行模型：**
1. Go 将 common 字段设为 Lua 全局变量（设置一次）。
2. 对每个 item：Go 将该 item 的字段设为 Lua 全局变量，调用 `function_for_item`。
3. 返回值按位置对应 `item_output`。

### 类型二：处理 common 特征 (`function_for_common`)

Go 调用一次 Lua 函数。Common 字段作为标量全局变量，item 字段作为 **list** 全局变量（每个字段是所有 item 的值组成的数组），支持跨 item 计算。

```python
flow.enrich_by_lua(
    common_input=["user_age"],
    item_input=["item_price"],
    function_for_common="compute_stats",
    common_output=["avg_price", "max_price"],
    lua_script="""
        function compute_stats()
            -- user_age: 标量, 来自 common
            -- item_price: list {99, 50, 75, ...}, 所有 item 的值
            local sum = 0
            local max_val = -math.huge
            for i = 1, #item_price do
                local p = item_price[i] or 0
                sum = sum + p
                if p > max_val then max_val = p end
            end
            return sum / #item_price, max_val
        end
    """,
)
```

**执行模型：**
1. Go 将 common 字段设为标量 Lua 全局变量。
2. Go 将 item 字段设为 list Lua 全局变量（每个字段对应一个 array）。
3. 调用 `function_for_common` 一次。
4. 返回值按位置对应 `common_output`。

### 全局变量语义总结

| 上下文 | common 字段 | item 字段 |
|--------|-------------|-----------|
| `function_for_item` (逐 item 调用) | 标量 (只读) | 标量 (当前 item) |
| `function_for_common` (调用一次) | 标量 | list (所有 item) |

### 缺失值处理

- 默认传 nil，可选配默认值。
- DSL 中可用 `item_defaults` / `common_defaults` 声明默认值。

```python
flow.enrich_by_lua(
    item_input=["item_price"],
    item_defaults={"item_price": 0.0},
    function_for_item="process",
    item_output=["result"],
    lua_script="""
        function process()
            return item_price * 1.1
        end
    """,
)
```

### Lua 代码内联

Lua 代码以字符串形式直接写在 Python DSL 中，作为 `lua_script` 参数传入。最终序列化到 JSON 配置中，由 Go 引擎在运行时加载执行。无需管理外部 Lua 脚本文件。

### Lua State 管理

Lua state 不是线程安全的，不能被多个 goroutine 同时使用。Pine 通过**每个 Lua 算子实例维护独立的 state 池**来解决并发问题。

#### 生命周期

| 阶段 | 行为 |
|------|------|
| Init | 记录 `lua_script` 和 `function_name`；创建首个 Lua state 并加载脚本；快照 `_G` 作为全局变量基准 |
| Execute | 从 `sync.Pool` 借出 state（池空则新建并加载脚本）→ 设置输入全局变量 → 调用函数 → 收集返回值 → 清除非基准全局变量 → 归还 pool |
| 配置重载 | 旧算子实例不再被引用，pool 中的 state 随 GC 回收 |

#### 并发模型

同一个 Lua 算子被多个请求并发执行时，各请求从池中借出**不同的** Lua state，完全隔离，无竞争：

```
请求 A ──借出──▶ LuaState₁ ──执行──▶ 归还
请求 B ──借出──▶ LuaState₂ ──执行──▶ 归还
```

`sync.Pool` 的特性使 state 数量自动适应并发度：高峰期 pool 扩张，空闲时 GC 回收多余的 state。

#### 全局变量清除

每次 Execute 完成后，引擎进行严格清除：

1. 引擎知道自己设了哪些输入全局变量（来自 `$metadata` 的 `common_input` + `item_input`），逐个置 nil。
2. 对比 Init 时的 `_G` 快照，清除所有脚本运行中新增的全局变量（防止算法同学意外引入全局状态导致跨请求泄漏）。

此操作代价极低（`_G` 通常只有 30-50 个条目，遍历一次即可）。
