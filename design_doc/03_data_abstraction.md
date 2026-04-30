# 数据抽象

## DataFrame

引擎内置高性能的表结构数据模型。

### 逻辑结构

- **Item 表**: 多行，每行一个 item，每列一个特征字段。
- **Common 表**: 单行，存放所有 item 共享的数据（如用户特征、请求上下文）。

```
Item 表:
┌──────────┬──────────┬──────────┬────────────┐
│ item_id  │ price    │ ctr      │ tags       │
│ (int64)  │ (float64)│ (float64)│ ([]string) │
├──────────┼──────────┼──────────┼────────────┤
│ 1001     │ 99.0     │ 0.12     │ ["a","b"]  │
│ 1002     │ 50.0     │ nil      │ ["c"]      │
│ 1003     │ 75.0     │ 0.15     │ nil        │
└──────────┴──────────┴──────────┴────────────┘

Common 表:
┌──────────┬──────────┬──────────────────────┐
│ user_age │ user_id  │ user_tag_weights     │
│ (int64)  │ (string) │ (map[string]float64) │
├──────────┼──────────┼──────────────────────┤
│ 25       │ "u_123"  │ {"sports":0.8}       │
└──────────┴──────────┴──────────────────────┘
```

### 特征类型体系

3 种基础类型 × 3 种结构，外加 nil：

| 结构 \ 基础类型 | int64 | float64 | string |
|----------------|-------|---------|--------|
| 标量 | `int64` | `float64` | `string` |
| 切片 | `[]int64` | `[]float64` | `[]string` |
| 字典 | `map[string]int64` | `map[string]float64` | `map[string]string` |

任何特征值均可为 nil，表示缺失。

### 数据访问接口

提供统一的键值化接口：
- 通过 `(field_name)` 访问 common 侧数据。
- 通过 `(item_index, field_name)` 访问 item 侧数据。

算子通过抽象 accessor 读写数据，不感知底层存储格式（行存/列存）。详见 [Go 算子接口](#go-算子接口)。

### Schema Free

- 新增数据字段无需重新编译引擎。
- 算子通过字符串 key 访问数据。
- 字段的类型在写入时确定，同一字段在所有行中类型一致。

### 字段命名约束

`_` 前缀保留给引擎内部字段（如控制流生成的 `_if_1`、`_else_2`，以及运行时字段 `_source` 等）。

- **input 允许引用**：`common_input` / `item_input` 可以引用 `_` 前缀字段（用户可能需要读取引擎内部状态，如 `_source`）。
- **output 禁止使用**：`common_output` / `item_output` 中，用户声明的字段不得以 `_` 开头，否则编译期报错。这避免用户字段与引擎内部字段冲突。
- **控制流豁免**：编译器为 `if_/elseif_/else_` 生成的控制算子（`for_branch_control=True`）输出的 `_if_*`、`_elif_*`、`_else_*` 字段不受此限制。
- **Flow contract 同样受约束**：Flow 的 `common_output` 和 `item_output` 也不得包含 `_` 前缀字段。

此约束由 Apple 编译器在 `compile_flow()` 阶段强制校验。

### 存储实现：行存与列存可切换（已实现）

提供统一的 `Frame` 接口，底层支持行存和列存两种实现。通过 JSON 配置的 `storage_mode` 字段选择（`"row"` 或 `"column"`，默认 `"row"`）。

**行存实现 `RowFrame`：**

```go
type RowFrame struct {
    mu     sync.RWMutex
    common map[string]any
    items  []map[string]any
}
```

- 实现简单，结构变更操作（removals、reorder）高效
- 缺点：大量小对象分配，GC 压力较大
- Go map 不支持并发读写（即使不同 key），因此只能做读写分离（RWMutex），不支持字段级无锁

**列存实现 `ColumnFrame`：**

```go
type ColumnFrame struct {
    mu       sync.RWMutex
    common   map[string]any
    columns  map[string][]any
    present  map[string][]bool   // per-item field presence bitmap
    rowCount int
}
```

- 构造和字段写入时分配数量极少（`[]any` 列式布局）
- `present` bitmap 记录每个 item 上每个字段是否显式存在，用于区分「字段缺失」与「字段存在但值为 nil」
- 结构变更操作需遍历所有列，开销随列数线性增长
- 与 RowFrame 一样，通过单个 `sync.RWMutex` 保证并发安全（读操作 RLock，写操作 Lock）

> **设计选择**：列存使用 `[]any` 而非 typed columns。理由：当前系统全程使用 `any`，typed columns 需要类型推断/声明机制，复杂度高。`[]any` 相比 `[]map[string]any` 已大幅减少 GC 压力。如后续 profiling 证明必要，可引入 typed columns。

**切换策略：**

`Frame` 接口统一抽象，上层算子代码不感知底层存储格式。引擎在编译时读取 `storage_mode` 配置，每次请求创建对应类型的 Frame。

## 缺失值处理

搜推广场景下特征缺失很常见（新用户无历史特征、部分 item 缺少某些属性），DataFrame 必须原生支持 nil 值。

### 策略

**特征列必须存在，值可以为 nil。**

- 特征列存在但值为 nil → 使用 defaults 填充（如果配了），否则算子收到 nil。
- 特征列不存在（未被任何前序算子产出，也不在 flow input 中）→ **Apple 解析时报错，拒绝生成 JSON**。这是依赖断裂，属于配置错误，不是 defaults 能解决的问题。

defaults 解决的是"数据稀疏"问题（列存在，部分行的值缺失），不是"配置错误"问题。

### Missing vs Explicit-nil

在 DataFrame 层面，我们区分两种状态：

- **字段缺失（missing）**：item 上某个字段根本不存在。在 RowFrame 中体现为 map key 不存在；在 ColumnFrame 中体现为 `present[field][i] == false`。
- **显式 nil（explicit-nil）**：item 上字段存在，但值为 nil。在 RowFrame 中体现为 `item[field] = nil`（key 存在）；在 ColumnFrame 中体现为 `columns[field][i] == nil && present[field][i] == true`。

这个区分贯穿 DataFrame 的三个核心操作：

| 操作 | 行为 |
|------|------|
| `BuildInput` | 只为 present 的字段写入 `OperatorInput` map key。missing 字段不写 key（除非有 default）。 |
| `ApplyOutput` | 写入任何值（包括 nil）都标记为 present。 |
| `ToResult` | 只投影 present 的字段到结果 JSON。missing 字段从输出中省略，不会变成 `null`。 |

**Defaults 的应用规则**：defaults 对 missing 和 explicit-nil 都生效——无论字段是缺失还是显式为 nil，只要配了 default，算子就会收到 default 值。区分 missing 和 explicit-nil 的意义不在于 default 行为，而在于：

1. `OperatorInput.ItemKeys()` / `CommonKeys()` 正确反映原始数据的稀疏结构。
2. 算子（如 `transform_by_remote_pineapple`）将输入序列化为 JSON 发给下游时，missing 字段不会被错误地编码为 `null`。
3. Debug trace / 输入快照不会把缺失字段显示为 `null`，避免误导排障。

### DSL 示例

```python
flow.some_op(
    item_input=["item_price", "item_score"],
    item_defaults={"item_price": 0.0},  # item_score 缺失时为 nil
    item_output=["item_rank"],
)
```

### 各层面的 nil 语义

| 层面 | 行为 |
|------|------|
| DataFrame | 原生支持 nil。区分「字段存在但值为 nil」（key present）与「字段不存在」（key absent）；`BuildInput` 和 `ToResult` 均保留此区分 |
| Go 算子 | `interface{}` 为 nil，算子开发者自行判断；可通过 `ItemKeys()` / `CommonKeys()` 查询实际存在的字段 |
| Lua 行模式 | `item["field"]` 为 nil，用 `if value ~= nil then` 判断 |
| Lua 列模式 | 数组中对应位置为 nil |

## 逻辑表

MVP 跳过。后续讨论另一种形式的逻辑表（非 DragonFly 的行子集视图方式，具体待定义）。

参考: [demo_logical_table.md](demo_logical_table.md) 中记录了 DragonFly 风格的逻辑表示例，已决定不采用。
实际场景中，召回在排序之前完成，通过 truncate 算子直接截断 topN 个 item，无需视图。

## 数据传递与生命周期

### 引擎托管写入

算子**不直接写 DataFrame**。数据传递流程如下：

```
引擎从 DataFrame 取数据 ──▶ 构造 OperatorInput ──▶ 算子通过 accessor 读取输入
                                                  ──▶ 算子通过 accessor 写入输出
                                                  ──▶ 引擎从 OperatorOutput 写回 DataFrame
```

- 算子通过 `OperatorInput` 的 accessor 方法读取输入数据，通过 `OperatorOutput` 的 accessor 方法写入输出数据。
- 算子不持有 DataFrame 引用，不感知底层存储格式。
- 引擎创建 `OperatorInput` 和 `OperatorOutput`，在算子执行前后负责数据的搬入搬出。
- 写回操作由引擎串行化处理，避免 Go map 并发写 panic。
- 此模型与 Lua 算子一致：数据进出全程由引擎管控。

### 为什么需要引擎托管

Go 的 `map` 并发写不安全（即使写不同的 key 也会 panic）。在 DAG 中，两个并行算子可能同时完成并分别向 common 或同一个 item 的 map 写入不同字段，产生竞争。引擎托管写入在调度层统一解决此问题，无论底层是行存还是列存，算子开发者无需关心并发安全。

### 写回并发机制

引擎使用一把 `sync.Mutex` 保护 DataFrame 的所有读写：

```
executeOp(op):
    mu.Lock()
    input := buildInput(dataframe, op)   // 从 DataFrame 构造 OperatorInput
    mu.Unlock()

    err := op.Execute(ctx, input, output) // 并行执行，不持锁

    mu.Lock()
    applyOutput(dataframe, output)        // 将 OperatorOutput 写回 DataFrame
    mu.Unlock()
```

此方案可行的原因：
- **算子执行是耗时大头**（外部服务调用、Lua 计算），完全并行，不持锁。
- **DataFrame 读写极快**（map 取值/设值），锁竞争可忽略。
- **DAG 依赖保证顺序正确**：op_A 的写回在 close(done) 之前完成，op_B 等待 done channel 后才开始读取，不会读到过期数据。
- **将来可细化**：切列存后列间天然隔离，可按需缩小锁粒度。MVP 阶段一把锁足够。

### Go 算子接口

```go
type Operator interface {
    // Init 在配置加载时调用一次，注入算子的业务参数。
    Init(params map[string]any) error

    // Execute 每次请求调用。必须并发安全（无可变状态）。
    // 引擎创建 input 和 output，算子通过 accessor 方法读写数据。
    Execute(ctx context.Context, input *OperatorInput, output *OperatorOutput) error
}
```

#### OperatorInput — 数据读取

```go
type OperatorInput struct { /* 内部实现对算子不可见 */ }

// Common 读取 common 侧字段，不存在时返回 nil。
func (in *OperatorInput) Common(field string) any

// ItemCount 返回 item 数量。
func (in *OperatorInput) ItemCount() int

// Item 读取第 index 个 item 的字段，不存在时返回 nil。
func (in *OperatorInput) Item(index int, field string) any
```

#### OperatorOutput — 数据写入

```go
type OperatorOutput struct { /* 内部实现对算子不可见 */ }

// --- 字段级操作（Feature、Lua、Control 等通用算子） ---

// SetCommon 写入 common 侧字段。
func (out *OperatorOutput) SetCommon(field string, value any)

// SetItem 写入第 index 个 item 的字段。
func (out *OperatorOutput) SetItem(index int, field string, value any)

// --- 结构性操作（改变 item 列表本身） ---

// AddItem 新增一个 item 行。用于 Recall（产出新 item）和 Merge（合并后写入主 DataFrame）。
func (out *OperatorOutput) AddItem(fields map[string]any)

// RemoveItem 标记第 index 个 item 待删除。引擎在写回时统一移除。用于 Filter、Merge 去重等。
func (out *OperatorOutput) RemoveItem(index int)

// SetItemOrder 设置 item 的新顺序。newOrder[i] 表示新位置 i 对应原位置。用于 Sort、Reorder。
func (out *OperatorOutput) SetItemOrder(newOrder []int)

// --- 错误处理 ---

// SetWarning 设置可恢复错误（降级结果）。引擎记录日志但 DAG 继续。
func (out *OperatorOutput) SetWarning(err error)
```

不同类型的算子使用不同的方法子集：

| 算子类型 | 使用的方法 |
|---------|-----------|
| Feature / Lua / Control | SetCommon, SetItem |
| Recall | AddItem |
| Merge | AddItem, RemoveItem, SetItem |
| Filter | RemoveItem |
| Reorder / Sort | SetItemOrder |
| Observe | （无输出） |

#### 设计原则

- **抽象 accessor**：算子通过方法访问数据，不感知底层是行存还是列存。MVP 用行存实现，将来切换列存时算子代码不需要修改。
- **引擎创建 output**：`OperatorOutput` 由引擎创建，作为参数传入算子，而非由算子自行分配。引擎据此控制底层分配策略，并在写回时应用结构性操作（AddItem、RemoveItem、SetItemOrder）。
- **统一接口，按需使用**：所有算子共享同一个 `Operator` 接口和 `OperatorOutput`。不同类型的算子使用不同的方法子集，不使用的方法不调用即可。引擎通过 JSON 元数据（`recall: true`、`sources` 等）或 Go 接口断言识别算子类别，决定写回策略。
- **无状态可重入**：算子在 `Init` 后不持有可变状态，`Execute` 可被多个 goroutine 并发调用。算子可持有只读配置和线程安全资源（如连接池），不可持有请求级状态。
- **错误约定**：`return nil` 表示正常执行；`output.SetWarning(err)` 表示可恢复错误（DAG 继续）；`return err` 表示不可恢复错误（DAG 终止）。

#### ConcurrentSafe — 并发安全声明

当 DSL 声明 `data_parallel > 1` 时，引擎在多个 goroutine 中并发调用**同一实例**的 `Execute`（`internal/runtime/parallel.go`）。这要求算子的 `Execute` 在同一实例上并发调用时不会产生 data race。

Go 的类型系统无法在编译期强制「方法不写实例字段」——接口无法约束 receiver 是 pointer 还是 value，且即使用 value receiver，通过指针字段仍可间接写入。因此我们采用 **opt-in 声明 + 运行时验证** 模型：

```go
// ConcurrentSafe is an optional interface. Operators that implement it
// declare their Execute is safe for concurrent calls on the same instance.
type ConcurrentSafe interface {
    IsConcurrentSafe()
}

type ConcurrentSafeMarker struct{}
func (ConcurrentSafeMarker) IsConcurrentSafe() {}
```

**规则**：

- 引擎在构建时检查：当 `data_parallel > 1`，算子实例必须实现 `ConcurrentSafe`，否则拒绝加载（`ValidationError`）。
- 算子通过嵌入 `pine.ConcurrentSafeMarker` 声明自身并发安全。
- 安全的前提：`Execute` 不写实例字段（`o.xxx = ...`），持有的资源（如连接池、Lua state pool）自身是线程安全的。
- 需要全集语义的算子（如 `transform_normalize` 需看所有 item 的 min/max）不应标记 `ConcurrentSafe`，因为分片后结果不正确。
- `go test -race ./...` 作为运行时验证手段，捕获声明正确性的遗漏。

#### MetadataAware — 字段名自省

算子操作的字段名已在 DSL 的 `common_input` / `common_output` / `item_input` / `item_output` 中声明。通过实现可选接口 `MetadataAware`，算子可以在初始化阶段获取这些声明的字段名，而无需通过 `Params` 重复指定。

```go
// MetadataAware is an optional interface. The engine calls SetMetadata
// after Init for operators that implement it.
type MetadataAware interface {
    SetMetadata(commonInput, commonOutput, itemInput, itemOutput []string)
}
```

引擎在 `Init(params)` 之后自动检测算子是否实现 `MetadataAware`，若实现则调用 `SetMetadata` 注入 `$metadata` 中声明的字段名。

##### MetadataHolder — 嵌入式默认实现

引擎提供 `MetadataHolder` 结构体，存储四个字段名切片并提供默认的 `SetMetadata` 实现。算子通过 Go embedding 嵌入即可自动满足 `MetadataAware` 接口，无需每个算子手写 `SetMetadata`：

```go
// MetadataHolder 存储 DSL 声明的字段名，提供默认 SetMetadata。
type MetadataHolder struct {
    CommonInput  []string
    CommonOutput []string
    ItemInput    []string
    ItemOutput   []string
}

func (m *MetadataHolder) SetMetadata(commonInput, commonOutput, itemInput, itemOutput []string) {
    m.CommonInput = commonInput
    m.CommonOutput = commonOutput
    m.ItemInput = itemInput
    m.ItemOutput = itemOutput
}
```

**使用方式**——算子嵌入 `pine.MetadataHolder`，在 `Execute` 中直接访问字段名：

```go
type SortOp struct {
    pine.MetadataHolder   // 自动实现 MetadataAware
    ascending bool
}

func (o *SortOp) Execute(ctx context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
    field := o.ItemInput[0]   // 直接从嵌入的 MetadataHolder 获取
    // ...
}
```

如果算子需要自定义 metadata 处理逻辑（极少见），可以覆写 `SetMetadata` 方法，此时嵌入的默认实现不再生效。

**何时用 `MetadataAware` vs `Params`**：

| 场景 | 推荐方式 | 说明 |
|------|---------|------|
| 算子操作的字段名由调用方决定 | 嵌入 `MetadataHolder` | 字段名已在 DSL 声明中给出，不应重复 |
| 算子有固定的业务语义字段 | 硬编码 | 如 `filter_paginate` 固定读 `page`/`size` |
| 算子有与字段名无关的配置 | `Params` | 如 `reorder_sort` 的 `order="desc"` |

**反模式**：通过 `Params` 传入字段名，导致 DSL 调用时必须同时声明 `common_input=["x"]` 和 `field="x"`——信息重复，容易不一致。

**正确模式示例**（以 `reorder_sort` 为例）：

```python
# 反模式: field 名在 item_input 和 params 中重复
flow.reorder_sort(
    item_input=["score"],
    field="score",       # 冗余！
    ascending=False,
)

# 正确模式: 算子通过 MetadataHolder 获取 item_input=["score"]
flow.reorder_sort(
    item_input=["score"],
    order="desc",
)
```
