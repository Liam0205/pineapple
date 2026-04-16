# 数据抽象

> **待讨论**: 本文档中标注"待讨论"的部分需要进一步确认。

## DataFrame

参考 DragonFly 的设计，引擎内置高性能的表结构数据模型。

### 逻辑结构

以推荐场景 item 侧数据为例：

```
┌──────────┬────────┬────────┬────────┐
│ item_id  │ score  │ ctr    │ tag    │
├──────────┼────────┼────────┼────────┤
│ 1001     │ 0.95   │ 0.12   │ "sports│
│ 1002     │ 0.87   │ 0.08   │ "news" │
│ 1003     │ 0.92   │ 0.15   │ "music"│
└──────────┴────────┴────────┴────────┘
```

- **Item 表**: 多行，每行一个 item，每列一个属性/特征。
- **Common 表**: 单行，存放所有 item 共享的数据（如用户特征、请求上下文）。

### 数据访问接口

提供统一的键值化接口：
- 通过 `(item_id, field_name)` 访问 item 侧数据。
- 通过 `(field_name)` 访问 common 侧数据。

### Schema Free

- 新增数据字段无需重新编译引擎。
- 算子通过字符串 key 访问数据，类似动态类型。

> **待讨论**: Go 实现中，schema-free 的数据模型如何设计？可能的方案：
> - `map[string]interface{}` 简单但性能较差
> - 列存 + 类型标签，类似 Apache Arrow
> - 预注册 schema + 偏移量访问

## 逻辑表 (待讨论)

DragonFly 支持基于物理表创建可读写的逻辑表（类似数据库视图），用于在团队间划分数据操作空间。

> **待讨论**: 是否需要逻辑表能力？这对 MVP 阶段是否必要？

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

## 数据传递与生命周期

算子之间通过 DataFrame 传递数据：
- 上游算子写入 DataFrame 的字段，下游算子读取。
- 引擎管理 DataFrame 的生命周期，确保并发安全。

> **待讨论**: 在 Go 中如何实现高效的数据传递？
> - 零拷贝在 Go GC 环境下的可行性与取舍
> - 是否使用 sync.Pool 复用 DataFrame 对象
> - 并发读写的安全策略（DAG 天然保证写后读？还是需要额外保护？）
