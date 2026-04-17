# 算子分类

算子按用途分为以下类型。每种类型对 DataFrame 的影响模式不同。

## 总览

| 类型 | 对 DataFrame 的影响 | 说明 |
|------|---------------------|------|
| 召回 (Recall) | 增加 item 行 | 从索引/服务中获取候选 item |
| 合并 (Merge) | 增加 item 行 | 将召回结果合并进主 DataFrame |
| 特征处理 (Feature) | 读写字段值 | 对 common/item 特征做计算、转换 |
| 改变顺序 (Reorder) | 改变 item 行序 | 排序、打散、多样性调整 |
| 过滤 (Filter) | 删除 item 行 | 按规则移除 item，含 truncate |
| 控制 (Control) | 不影响数据，影响执行流 | if-elseif-else，决定后续算子是否执行 |
| 观察 (Observe) | 只读 | 不影响 DataFrame，将信息写入外部系统（如 Kafka） |

## 召回 (Recall)

从外部索引或服务中获取候选 item，产出新的 item 集合。

- 输入：common 特征（如 user_id、query 等检索条件）
- 输出：一批新的 item（含 item_id 及其附属特征），存入**引擎内部暂存区**（不写入主 DataFrame）
- 特点：这是唯一"凭空产生 item"的算子类型
- JSON 中标记 `"recall": true`，Pine 据此识别

**召回结果不直接进入主 DataFrame**，而是暂存在引擎内部。多个召回算子各自暂存独立的结果，互不干扰。最终通过合并算子显式合并进主 DataFrame。

```
recall_A ──▶ 暂存区 A ──┐
                          ├──▶ merge ──▶ 主 DataFrame
recall_B ──▶ 暂存区 B ──┘
```

这样设计的原因：多个召回源可能返回同一个 item，直接写入主 DataFrame 会导致重复。合并策略（去重、择优等）是业务决策，应显式处理。

### DAG 依赖

召回算子的依赖仍然通过 `$metadata.common_input` 推导（如依赖 `user_id`）。召回 → 合并的依赖通过合并算子的 `sources` 字段显式建立，不走字段名推导。

```python
flow.recall_from_index(
    common_input=["user_id", "query_embedding"],
    item_output=["item_id", "item_score", "item_category"],
    index_name="main_index",
    top_k=1000,
)
```

## 合并 (Merge)

将一个或多个召回算子暂存的结果合并进主 DataFrame。

- 通过 `sources` 参数引用召回算子名称
- Pine 据此建立召回 → 合并的显式 DAG 边（不走字段名推导）
- 处理 item 去重（多路召回可能产生重复 item）
- 可选的合并策略（取并集、按 score 择优等）
- 合并完成后，暂存区由引擎回收

合并算子是唯一能向主 DataFrame 添加 item 行的算子。

```python
flow.merge(
    sources=["recall_from_index", "recall_from_realtime"],
    item_output=["item_id", "item_score", "item_category"],
    dedup_by="item_id",
    strategy="union",
)
```

### `item_output` 的语义

合并算子的 `item_output` 声明合并后主 DataFrame 中的 item 字段。Apple 在编译时校验各 source 召回算子的 `item_output` 是否能覆盖 merge 声明的字段。

> **待细化**: 合并策略的具体语义（union / intersect / 按 score 择优等）和冲突处理规则。

## 特征处理 (Feature)

对 common 和/或 item 的特征做读取、修改、计算、写入等操作。这是最通用的算子类型。

大多数 Go 通用算子和 Lua 算子属于此类。

### Go 算子示例

```python
# 将 common 特征分发到 item 侧
flow.dispatch_common_to_item(
    common_input=["search_scene"],
    item_output=["item_search_scene"],
)

# 特征归一化
flow.normalize(
    item_input=["raw_score"],
    item_output=["norm_score"],
    method="min_max",
)
```

### Lua 算子示例

```python
flow.enrich_by_lua(
    common_input=["user_age"],
    item_input=["item_price"],
    function_for_item="adjust_price",
    item_output=["item_adjusted_score"],
    lua_script="""
        function adjust_price()
            if user_age < 18 then
                return item_price * 0.8
            else
                return item_price
            end
        end
    """,
)
```

关于 Lua 算子的详细设计（全局变量语义、function_for_common vs function_for_item、缺失值处理等），见 [02 流程抽象 — Lua 算子](02_flow_abstraction.md#lua-算子)。

## 改变顺序 (Reorder)

按照某种规则改变 item 的顺序。不增删 item，只改变排列。

典型场景：
- **排序**: 按 score 降序
- **打散**: 相邻 item 不重复同类目
- **多样性**: 按类目/品牌等维度做多样性调整

```python
flow.sort_by(
    item_input=["item_score"],
    order="desc",
)

flow.diversify(
    item_input=["item_category"],
    window_size=3,
)
```

## 过滤 (Filter)

按照某种规则删除 item。执行后 DataFrame 的 item 行数减少。

典型场景：
- **属性过滤**: 移除不满足条件的 item
- **曝光过滤**: 移除已曝光的 item
- **去重**: 移除重复 item
- **截断 (Truncate)**: 只保留前 N 个 item

```python
flow.filter_by(
    item_input=["item_status"],
    remove_if="item_status == 'offline'",
)

flow.truncate(top_n=200)
```

## 控制 (Control)

影响 DAG 中后续算子是否执行。只考虑 if-elseif-else 逻辑。

控制算子不修改 DataFrame，只决定执行路径。

### 条件表达式

条件为 **Lua 表达式**，复用引擎已有的 Lua 运行时，无额外依赖。表达式中可引用所有 common 特征作为全局变量（与 Lua 算子的 `function_for_common` 一致）。

表达式必须求值为 boolean。

```python
flow.if_(condition="user_age > 18 and item_count > 0") \
    .some_op(...) \
    .some_other_op(...) \
.elseif_(condition="fallback_enabled ~= nil") \
    .fallback_op(...) \
.else_() \
    .default_op(...) \
.end_if_()
```

### 语法说明

沿用 Lua 语法风格：

| 操作 | Lua 语法 |
|------|----------|
| 不等于 | `~=` |
| 与 | `and` |
| 或 | `or` |
| 非 | `not` |
| 空值判断 | `x ~= nil` |
| 长度 | `#(list)` |

### 编译方式

Apple 将 `if_()` / `elseif_()` / `else_()` / `end_if_()` 降级为 Lua 控制算子 + skip 机制。每个条件分支生成一个控制算子，输出隐藏的控制属性；分支内算子通过 `skip` 引用控制属性。`elseif` / `else` 的控制算子依赖前面所有控制属性，形成链式互斥。Pine 无需特殊处理控制流。

详细编译规则和 JSON 示例见 [06 JSON 配置格式 — 控制流的编译](06_json_config.md#控制流的编译)。

## 观察 (Observe)

只读取 DataFrame 中的信息，不做任何修改。用于将信息写入外部系统。

典型场景：
- 将中间结果写入 Kafka / MQ
- 记录日志、打点
- 调试用的数据快照

```python
flow.observe_to_kafka(
    common_input=["user_id", "request_id"],
    item_input=["item_id", "item_score"],
    topic="recommendation_log",
)
```

特点：
- 对 DataFrame 无副作用，不影响 DAG 中其他算子的执行。
- 可以和其他算子并行执行（因为无数据输出，不会产生依赖）。
- **豁免于死代码消除**：观察算子没有 output 字段，但始终保证执行，不会被判定为无效算子。
