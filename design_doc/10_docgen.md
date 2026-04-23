# 算子文档自动生成

## 需求

从 Go 代码生成 Python 代码的过程中，同步生成算子的使用文档。

## 信息来源

Go 算子源码已包含生成文档所需的全部信息，分布在两处：

### 1. Schema 注册信息（主要数据源，编译期强制）

`pine.Register` 调用中的 `OperatorSchema` 是文档的权威来源：

```go
pine.Register(pine.OperatorSchema{
    Name:        "filter_condition",
    Type:        "Filter",
    Description: "Removes items where a specified field equals a given value.",
    Params: map[string]pine.ParamSpec{
        "field": {Type: "string", Required: true, Description: "Item field to check."},
        "value": {Type: "any", Required: true, Description: "Items where field == value are removed."},
    },
}, func() pine.Operator { ... })
```

提供：算子名、类型、功能描述、参数类型、是否必选、默认值、参数描述。

`Register()` 在注册时校验以下字段不得为空，否则 panic：
- `OperatorSchema.Type`
- `OperatorSchema.Description`
- 每个 `ParamSpec.Description`

这确保了算子开发者**必须**填写文档信息，而不是依赖自觉。

### 2. 源码注释（仅用于 Metadata contract，可选）

算子源文件顶部的注释中如果包含 `Metadata contract` 段，会被解析并写入文档：

```go
// Metadata contract (typical usage):
//   CommonInput:  [<common_field>]
//   CommonOutput: []
//   ItemInput:    []
//   ItemOutput:   [<item_field>]
```

Metadata contract 描述的是"运行时此算子典型的 DataFrame 读写模式"，使用参数引用的模板字符串，属于文档性质的补充说明。如果注释中没有此段，文档中对应部分留空。

## 生成方案

### 生成时机

扩展现有的 `pineapple-codegen` 工具。在生成 Python DSL 代码的同时，同步生成算子文档。一次 codegen 运行同时输出 Python 代码和 Markdown 文档。

### 数据流

1. `registry.All()` 获取所有 `OperatorSchema`（包含 Type/Description/参数完整信息）
2. `ParseOperatorDocs(opsDir)` 扫描源码注释，提取 Metadata contract（best-effort）
3. 按算子名关联两路数据，渲染文档

### 输出结构

```
doc/operators/
├── README.md              ← 索引：按类型分组列出所有算子
├── filter_condition.md
├── filter_truncate.md
├── merge_dedup.md
├── observe_log.md
├── recall_static.md
├── reorder_sort.md
├── transform_by_lua.md
├── transform_by_remote_pineapple.md
├── transform_dispatch.md
├── transform_normalize.md
└── ...
```

### 单算子文档格式

```markdown
# <operator_name>

**Type**: <type>

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

`README.md` 按 Type 分组，每组内按字母序列出算子名和简短描述，链接到各自的详细文档。

## 实现要点

1. **Schema 即文档**：`OperatorSchema` 的 `Type`、`Description` 和 `ParamSpec.Description` 是生成文档的主数据源，`Register()` 中强制校验非空
2. **注释仅补充 Metadata**：`docparse.go` 使用 `go/parser` + `go/ast` 读取 package doc comment，只解析 `Metadata contract` 段
3. **模板渲染**：使用 `text/template` 生成 Markdown，与现有 Python 模板共享辅助函数
4. **命令行参数**：`-doc-dir`（默认空，不为空则生成文档）和 `-operators-dir`（默认 `operators`）
5. **幂等生成**：每次运行覆盖已有文档，确保文档与代码同步
