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
- 通过 `(item_index, field_name)` 访问 item 侧数据。
- 通过 `(field_name)` 访问 common 侧数据。

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

默认传 nil，可选配默认值。

- 算子从 DataFrame 读取字段时，若值不存在，默认得到 nil。
- DSL 中可为输入字段声明默认值，缺失时由引擎自动填充。
- 不配默认值时，算子（Go 或 Lua）自行处理 nil。

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

> **待讨论**: DragonFly 支持基于物理表创建可读写的逻辑表（类似数据库视图），用于在团队间划分数据操作空间。是否需要？MVP 阶段是否必要？

## 数据传递与生命周期

算子之间通过 DataFrame 传递数据：
- 上游算子写入 DataFrame 的字段，下游算子读取。
- 引擎管理 DataFrame 的生命周期，确保并发安全。

> **待讨论**: 在 Go 中如何实现高效的数据传递？
> - 是否使用 sync.Pool 复用 DataFrame 对象
> - 并发读写的安全策略（DAG 天然保证写后读？还是需要额外保护？）
