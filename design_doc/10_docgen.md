# 算子文档自动生成

## 需求

从 Go 代码生成 Python 代码的过程中，同步生成算子的使用文档。

## 信息来源

Go 算子源码已包含生成文档所需的全部信息，分布在两处：

### 1. 源码结构化注释

每个算子源文件顶部有统一格式的注释：

```go
// Operator: filter_condition
// Category: Filter
// Description: Removes items where a specified field equals a given value.
//
// Params:
//   - field (string, required): Item field to check.
//   - value (any, required): Items where field == value are removed.
//
// Metadata contract (typical usage):
//   CommonInput:  []
//   CommonOutput: []
//   ItemInput:    [<field>]
//   ItemOutput:   []
package filter
```

提供：算子名、分类、功能描述、参数语义说明、典型元数据契约。

### 2. Schema 注册信息

`pine.Register` 调用中的 `OperatorSchema`：

```go
pine.Register(pine.OperatorSchema{
    Name: "filter_condition",
    Params: map[string]pine.ParamSpec{
        "field": {Type: "string", Required: true},
        "value": {Type: "any", Required: true},
    },
}, func() pine.Operator { ... })
```

提供：参数类型、是否必选、默认值（机器精确，作为参数描述的权威来源）。

## 生成方案

### 生成时机

扩展现有的 `pineapple-codegen` 工具。在生成 Python DSL 代码的同时，同步生成算子文档。一次 codegen 运行同时输出 Python 代码和 Markdown 文档。

### 数据合并

注释和 Schema 两套数据互补：

- **Schema（权威）**：参数类型、必选/可选、默认值
- **注释（补充）**：分类、功能描述、参数语义说明、元数据契约

解析注释后，按算子名关联到 Schema，合并生成完整文档。

### 输出结构

```
doc/operators/
├── README.md              ← 索引：按分类分组列出所有算子
├── filter_condition.md
├── filter_truncate.md
├── feature_normalize.md
├── feature_dispatch.md
├── lua.md
├── merge_dedup.md
├── observe_log.md
├── recall_static.md
└── reorder_sort.md
```

### 单算子文档格式

```markdown
# <operator_name>

**Category**: <category>

<description>

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| ... | ... | ... | ... | ... |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | [...] |
| CommonOutput | [...] |
| ItemInput | [...] |
| ItemOutput | [...] |

## DSL Usage

​```python
flow.<name>(
    <param>=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
​```
```

### 索引文件格式

`README.md` 按 Category 分组，每组内按字母序列出算子名和简短描述，链接到各自的详细文档。

## 实现要点

1. **注释解析**：使用 `go/parser` + `go/ast` 读取 package-level doc comment，逐行解析各段落
2. **模板渲染**：使用 `text/template` 生成 Markdown，与现有 Python 模板共享辅助函数
3. **命令行参数**：新增 `-doc-dir`（默认 `doc/operators`）和 `-operators-dir`（默认 `operators`）
4. **幂等生成**：每次运行覆盖已有文档，确保文档与代码同步
