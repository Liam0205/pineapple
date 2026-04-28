# 算子类型体系

算子按**类型 (Type)** 分类。类型是算子注册时的强制元信息，决定了：

1. **允许的 `OperatorOutput` 方法** — 运行时校验，违规则报错
2. **DAG 依赖语义** — 不同类型的依赖推导规则不同
3. **Python DSL 命名约定** — codegen 强制方法名以类型对应的动词前缀开头

每个算子**必须且只能**属于一种类型。

## 总览

| 类型 | 允许的 Output 方法 | DAG 语义 | DSL 前缀 | 对 DataFrame 的影响 |
|------|-------------------|----------|----------|---------------------|
| Recall | `AddItem` | 等待产出其依赖字段的 Transform 完成；Recall 间可并行 | `recall_` | 增加 item 行 |
| Transform | `SetCommon`, `SetItem` | 字段级 RAW/WAW/WAR 追踪，无依赖可并行 | `transform_` | 读写字段值 |
| Filter | `RemoveItem` | Barrier | `filter_` | 删除 item 行 |
| Merge | `RemoveItem`, `SetItem` | Barrier | `merge_` | 合并/去重 item 行 |
| Reorder | `SetItemOrder` | Barrier | `reorder_` | 改变 item 行序 |
| Observe | 无 | 只读 RAW 依赖，不阻塞下游 | `observe_` | 只读 |

所有类型均可调用 `SetWarning`。DSL 前缀一律为**动词**。

## 类型详解

### Recall

从外部索引或服务中获取候选 item，产出新的 item 集合。这是唯一"凭空产生 item"的算子类型。

- **允许的 Output 方法**: `AddItem`
- **DAG 语义**: Recall 算子声明了 `common_input` / `item_input`，这些字段要么来自上游请求（已存在，无需等待），要么由某个 Transform 算子产出。Recall 只需等待**实际产出其所依赖字段的那些 Transform 算子**完成。多个 Recall 之间可以并行执行。
- **DSL 前缀**: `recall_`

引擎在写回 Recall 产出的 item 时，自动注入 `_source` 字段（值为算子名），供 Merge 算子识别来源。`_source` 是可依赖、可输出的普通字段，无特殊限制。

**`recall` 参数由类型驱动，用户不需要手动传。** codegen 生成的 Python 方法自动注入 `recall=True`；编译器根据算子类型自动识别 Recall 语义。DSL 调用时方法名以 `recall_` 开头，一眼可见。

```python
flow.recall_static(
    item_output=["item_id", "item_price"],
    items=[...],
)
# 不需要 recall=True，类型即身份
```

多个召回算子各自独立产出 item，直接写入主 DataFrame。不同召回源可能产出同一个 item（重复），也可能产出不同的字段集（异构 schema）。这些都由后续的 Merge 算子处理。

### Transform

对 common 和/或 item 的字段做读取、计算、写入等操作。这是最通用的算子类型。

- **允许的 Output 方法**: `SetCommon`, `SetItem`
- **DAG 语义**: 标准的字段级 RAW/WAW/WAR 依赖追踪（见 [02 流程抽象 — 数据冒险处理](02_flow_abstraction.md#数据冒险处理)）。无字段依赖的 Transform 可以并行执行。
- **DSL 前缀**: `transform_`

Go 通用算子和 Lua 算子均属于此类。

```python
# Go 算子示例
flow.transform_dispatch(
    common_input=["search_scene"],
    item_output=["item_search_scene"],
    common_field="search_scene",
    item_field="item_search_scene",
)

flow.transform_normalize(
    item_input=["raw_score"],
    item_output=["norm_score"],
    field="raw_score",
)

# Lua 算子示例
flow.transform_by_lua(
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

#### 数据并行

Transform 是唯一支持 `data_parallel` 的算子类型。启用后引擎自动将 items 切分为 N 片并行执行、合并输出，对算子完全透明。启用条件：`$metadata.common_output` 必须为空。详见 [02 流程抽象 — 算子级数据并行](02_flow_abstraction.md#算子级数据并行)。

#### Remote Pineapple 算子

`transform_by_remote_pineapple` 调用下游 Pineapple 服务的 `/execute` 端点，将本地 frame 字段映射为下游请求字段、将下游响应字段映射回本地输出字段。适用于跨服务特征获取场景。

```python
flow.transform_by_remote_pineapple(
    common_input=["user_age"],
    common_output=["user_score"],
    item_input=["item_id"],
    item_output=["item_feature"],
    host="feature-service",
    port=8080,
    common_request=["age"],
    common_response=["score"],
    item_request=["id"],
    item_response=["feature"],
    timeout=5.0,
    fail_on_error=True,
)
```

字段映射模型：`common_request[i]` 与 `common_input[i]` 按位置对应，`item_request[i]` 与 `item_input[i]` 按位置对应，响应侧同理。当 `common_request` 等映射参数未提供时，直接使用 metadata 字段名（无映射）。

**`fail_on_error=false` 降级行为**：下游错误降级为 warning，算子返回成功（`nil` error），但**不写入任何输出字段**——`common_output` 和 `item_output` 声明的字段均不会被写入 DataFrame。具体影响取决于字段的先前状态：若字段之前不存在，下游算子读到 `nil`；若字段已存在（由更早的算子写入），保留旧值不变。下游算子可通过 `item_defaults` / `common_defaults` 为这些字段配置兜底默认值，当字段值为 `nil` 时自动填充。

### Filter

按照某种规则删除 item。执行后 DataFrame 的 item 行数减少。

- **允许的 Output 方法**: `RemoveItem`
- **DAG 语义**: **Barrier** — DSL 中声明在 Filter 之前的所有算子执行完毕后，Filter 才执行；Filter 执行完毕后，后续算子才能开始。
- **DSL 前缀**: `filter_`

Barrier 语义的必要性：Filter 改变 item 集合的组成（哪些 item 可见），而后续算子可能对所有可见 item 做聚合计算。如果 Filter 与这类算子并行执行，聚合结果不可预测。

典型场景：
- **属性过滤**: 移除不满足条件的 item
- **曝光过滤**: 移除已曝光的 item
- **截断 (Truncate)**: 只保留前 N 个 item

```python
flow.filter_condition(
    item_input=["item_status"],
    field="item_status",
    value="offline",
)

flow.filter_truncate(top_n=200)
```

### Merge

对主 DataFrame 中的 item 做去重、字段合并等处理。

- **允许的 Output 方法**: `RemoveItem`, `SetItem`
- **DAG 语义**: **Barrier** — 同 Filter。
- **DSL 前缀**: `merge_`

Merge 比 Filter 多了 `SetItem` 权限：当遇到重复的 item 时，可能需要合并某些列的值（如取 score 最大值、合并来源标签等）。

通过 `sources` 参数引用召回算子名称，Pine 据此建立 Recall → Merge 的显式 DAG 边。

```python
flow.merge_dedup(
    sources=["recall_from_index", "recall_from_realtime"],
    item_input=["item_id", "item_score", "_source"],
    item_output=["item_id", "item_score"],
    key="item_id",
)
```

### Reorder

按照某种规则改变 item 的顺序。不增删 item，只改变排列。

- **允许的 Output 方法**: `SetItemOrder`
- **DAG 语义**: **Barrier** — 同 Filter。Reorder 改变 item 的顺序，而后续算子可能依赖 item 的位次信息（如处理 top-K 位置的信息）。
- **DSL 前缀**: `reorder_`

典型场景：
- **排序**: 按 score 降序
- **打散**: 相邻 item 不重复同类目
- **多样性**: 按类目/品牌等维度做多样性调整

```python
flow.reorder_sort(
    item_input=["item_score"],
    field="item_score",
    order="desc",
)
```

### Observe

只读取 DataFrame 中的信息，不做任何修改。用于将信息写入外部系统。

- **允许的 Output 方法**: 无（仅 `SetWarning`）
- **DAG 语义**: 对声明的输入字段有 RAW 依赖，不阻塞任何下游算子。
- **DSL 前缀**: `observe_`

Observe 与后续 Transform 即使读写同一字段也可安全并行——因为 OperatorOutput 的缓冲机制保证了 Observe 读到的是 Transform Apply 之前的值。

特点：
- 对 DataFrame 无副作用。
- 可以和其他算子并行执行（因为无数据输出，不会产生依赖）。
- **豁免于死代码消除**: 观察算子没有 output 字段，但始终保证执行。

```python
flow.observe_log(
    common_input=["user_id", "request_id"],
    item_input=["item_id", "item_score"],
    topic="recommendation_log",
)
```

## Barrier 语义

Filter、Merge、Reorder 三种类型共享 **Barrier 语义**：

```
DSL 顺序中在 Barrier 之前的所有算子
        │
        ▼  (全部完成)
    Barrier 算子执行
        │
        ▼  (完成后)
DSL 顺序中在 Barrier 之后的所有算子才能开始
```

这是因为这三种类型都会改变 item 集合的"结构"（组成或顺序），而结构变化对后续算子的语义有全局影响。

## 控制 (Control)

控制流（if/else 分支）**不是独立的算子类型**，而是 Python DSL 层面的编译期抽象。

`if_()` / `elseif_()` / `else_()` / `end_if_()` 在编译时被降级为 `transform_by_lua` 算子（带 `for_branch_control: true` 标记）+ `skip` 机制。条件表达式中的字段引用使用 `{{field_name}}` 模板语法（如 `if_("{{item_count}} > 0")`），编译器据此提取字段依赖；双花括号之外的部分为原生 Lua 表达式。编译器根据条件生成控制算子，输出隐藏的控制属性；分支内算子通过 `skip` 引用控制属性。Go 引擎无需特殊处理控制流。控制算子使用显式命名（如 `if_1`、`elseif_2`、`else_3`），使其在 DAG 可视化中可直观辨识为条件分支。

> **备注**: Control 作为编译期语法糖处理，不计划提升为独立算子类型。当前 skip 机制已满足需求，引擎层面无需感知控制流语义。

详细编译规则和 JSON 示例见 [06 JSON 配置格式 — 控制流的编译](06_json_config.md#控制流的编译)。

## 类型约束的执行机制

### 注册时 (Go `Register()`)

`OperatorSchema` 包含必填的 `Type` 字段，值必须为合法枚举（`"Recall"`, `"Transform"`, `"Filter"`, `"Merge"`, `"Reorder"`, `"Observe"`）。缺失或非法值 → 启动时 panic。

### 运行时 (调度器)

算子 `Execute()` 返回后，调度器检查 `OperatorOutput` 中实际使用了哪些方法：

| 类型 | 允许非空的 Output 字段 |
|------|----------------------|
| Recall | `addedItems` |
| Transform | `commonWrites`, `itemWrites` |
| Filter | `removedItems` |
| Merge | `removedItems`, `itemWrites` |
| Reorder | `itemOrder` |
| Observe | 无 |

`warning` 字段所有类型均允许。不符合约束则返回 error。

### codegen 时

- 生成的 Python 方法名强制以类型对应的前缀开头
- Recall 类型的方法自动注入 `recall=True`，不暴露给用户
- 不同类型可生成不同的 metadata 参数签名（如 Observe 不需要 `item_output`）

## DAG 构建规则汇总

| 类型 | 依赖来源 | 对后续算子的影响 | data_parallel |
|------|---------|----------------|---------------|
| Recall | RAW 依赖于产出其输入字段的 Transform | 下游 reader 依赖 Recall 产出字段（RAW） | 禁止 |
| Transform | 字段级 RAW/WAW/WAR | 字段级 RAW/WAW/WAR | 支持（需空 common_output） |
| Filter | Barrier: 等待所有之前的算子 | Barrier: 之后的算子等待它 | 禁止 |
| Merge | Barrier + `sources` 显式边 | Barrier | 禁止 |
| Reorder | Barrier | Barrier | 禁止 |
| Observe | RAW 依赖输入字段 | 不阻塞任何下游 | 禁止 |

所有边推导完成后，引擎执行传递性归约，移除被更长路径隐含的冗余边。最终执行图保留保持可达性的最小边集。

## 算子重命名

引入类型体系后，现有算子统一按 DSL 前缀规范重命名：

| 旧名称 | 新名称 | 类型 |
|--------|--------|------|
| `feature_dispatch` | `transform_dispatch` | Transform |
| `feature_normalize` | `transform_normalize` | Transform |
| `lua` | `transform_by_lua` | Transform |
| — | `transform_by_remote_pineapple` | Transform (新增) |
| `recall_static` | `recall_static` | Recall (不变) |
| `filter_condition` | `filter_condition` | Filter (不变) |
| `filter_truncate` | `filter_truncate` | Filter (不变) |
| `merge_dedup` | `merge_dedup` | Merge (不变) |
| `reorder_sort` | `reorder_sort` | Reorder (不变) |
| `observe_log` | `observe_log` | Observe (不变) |
