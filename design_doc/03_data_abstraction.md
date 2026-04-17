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

### 存储实现：行存与列存可切换

提供统一的 DataFrame 接口，底层支持行存和列存两种实现，业务可通过 benchmark 选择。

**行存实现（MVP 优先）：**

```go
// 每个 item 是一个 map
type RowStore struct {
    common map[string]any
    items  []map[string]any
}
```

- 实现简单，快速验证整体架构
- 缺点：大量小对象分配，GC 压力大，cache 不友好

**列存实现（性能优化）：**

```go
type ColumnStore struct {
    common  map[string]any      // 单行，直接用 map
    columns map[string]Column   // item 侧按列存储
    rowCount int
}

type Column struct {
    dtype       DataType   // Int64, Float64, String, SliceInt64, SliceFloat64, SliceString, MapStringInt64, ...
    int64s      []int64
    float64s    []float64
    strings     []string
    sliceInt64s [][]int64
    // ... 其他类型
    nulls       []bool     // 标记哪些行是 nil
}
```

- 列操作高效，cache 友好，与 Lua 列模式天然对齐
- 实现复杂度较高

**切换策略：**

接口层抽象统一，底层行存/列存可替换，不影响上层算子代码。最终由业务自行跑 benchmark 决定采用哪一种。

## 缺失值处理

搜推广场景下特征缺失很常见（新用户无历史特征、部分 item 缺少某些属性），DataFrame 必须原生支持 nil 值。

### 策略

**特征列必须存在，值可以为 nil。**

- 特征列存在但值为 nil → 使用 defaults 填充（如果配了），否则算子收到 nil。
- 特征列不存在（未被任何前序算子产出，也不在 flow input 中）→ **Apple 解析时报错，拒绝生成 JSON**。这是依赖断裂，属于配置错误，不是 defaults 能解决的问题。

defaults 解决的是"数据稀疏"问题（列存在，部分行的值缺失），不是"配置错误"问题。

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
| DataFrame | 原生支持 nil，字段存在但值为空 与 字段不存在 均表现为 nil |
| Go 算子 | `interface{}` 为 nil，算子开发者自行判断 |
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

// SetCommon 写入 common 侧字段。
func (out *OperatorOutput) SetCommon(field string, value any)

// SetItem 写入第 index 个 item 的字段。
func (out *OperatorOutput) SetItem(index int, field string, value any)

// SetWarning 设置可恢复错误（降级结果）。引擎记录日志但 DAG 继续。
func (out *OperatorOutput) SetWarning(err error)
```

#### 设计原则

- **抽象 accessor**：算子通过方法访问数据，不感知底层是行存还是列存。MVP 用行存实现，将来切换列存时算子代码不需要修改。
- **引擎创建 output**：`OperatorOutput` 由引擎创建（已知 ItemCount），作为参数传入算子，而非由算子自行分配。引擎据此控制底层分配策略。
- **无状态可重入**：算子在 `Init` 后不持有可变状态，`Execute` 可被多个 goroutine 并发调用。算子可持有只读配置和线程安全资源（如连接池），不可持有请求级状态。
- **错误约定**：`return nil` 表示正常执行；`output.SetWarning(err)` 表示可恢复错误（DAG 继续）；`return err` 表示不可恢复错误（DAG 终止）。
