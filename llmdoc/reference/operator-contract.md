# 算子开发契约

本参考文档面向新增或修改 Pineapple 算子的开发者。

## 权威文件

以下文件为唯一事实源：

- `internal/types/operator.go`
- `internal/types/operator_io.go`
- `internal/registry/registry.go`
- `operator.go`
- `operator_io.go`
- `registry.go`
- `operators/` 下的代表性实现

## 算子生命周期

算子实例经历两个阶段。

1. `Init(params map[string]any) error`
   - 引擎构建期间调用一次
   - 仅接收业务参数
   - 引擎保留键已被剥离
   - 默认值已被应用

2. `Execute(ctx context.Context, input *OperatorInput, output *OperatorOutput) error`
   - 每次请求执行时调用
   - 从提供的输入快照读取
   - 向提供的输出收集器记录写入
   - 可能跨请求在同一算子实例上并发运行

该并发模型意味着算子结构体上的任何可变状态必须在 `Init()` 后不可变或显式同步。

## 必需接口

公共算子接口通过 `operator.go` 暴露，在 `internal/types/operator.go` 中定义：

- `Init(params map[string]any) error`
- `Execute(ctx, input, output) error`

使用 `operator_io.go` 中的 `OperatorInput` 和 `OperatorOutput`，而非直接触及运行时内部。

## 注册契约

算子通过 `registry.go` 中的 `pine.Register(schema, factory)` 注册，通常在算子源文件的 `init()` 函数中。

### 必需 Schema 字段

`OperatorSchema` 必须提供：

- `Name` — 稳定的算子类型名如 `transform_copy`
- `Type` — 六种算子类型之一
- `Description` — 非空的人类可读描述
- `Params` — 按参数名索引的业务参数 map
- `Factory` 不是 Schema 的一部分；由注册调用单独提供

每个 `ParamSpec` 应提供：

- `Type` — 文档/codegen 类型标记
- `Required` — 调用者是否必须提供
- `Default` — 可选默认值
- `Description` — 非空描述

### 注册失败行为

`internal/registry.Register` 有意严格，在无效定义时 panic，包括：

- 空算子名
- 无效算子类型
- 空算子描述
- 任何参数缺少描述
- 重复注册

将 Schema 注册视为启动时校验。缺失元数据被认为是程序员错误，不是可恢复的运行时条件。

## 保留 JSON/配置键

这些键为引擎所有，在 `Init(params)` 接收其 map 之前被过滤：

- `type_name`
- `$metadata`
- `$code_info`
- `skip`
- `recall`
- `sources`
- `debug`
- `row_dependency`
- `common_defaults`
- `item_defaults`
- `for_branch_control`

不要定义依赖这些名称的业务参数。

## 可选接口

### `MetadataAware`

若算子实现 `internal/types/operator.go` 中的 metadata-aware 接口，引擎将在 `Init()` 后注入字段元数据。

典型模式：

- 嵌入 `MetadataHolder`
- 在 `Execute()` 中读取 `CommonInput`、`CommonOutput`、`ItemInput`、`ItemOutput`

这是算子获知应读写哪些字段的标准方式。

### `DebugAware`

若算子实现 debug-aware 接口，引擎在 `Init()` 后注入逐算子调试设置。

典型模式：

- 嵌入 `DebugHolder`
- 当算子需要超出标准运行时 trace 的专用调试行为时查阅 debug 信息

大多数算子仅需运行时 trace 捕获，但 Lua 是同时嵌入 metadata 和 debug holder 的示例。

## 输入/输出 API 契约

### 从 `OperatorInput` 读取

使用只读访问器：

- `Common(field)`
- `Item(index, field)`
- `ItemCount()`
- `CommonKeys()`
- `ItemKeys(index)`

不要假设完整 frame 或任意未声明字段存在；输入从声明的元数据投影。

### 写入 `OperatorOutput`

仅使用算子类型允许的输出方法：

- `SetCommon`
- `SetItem`
- `AddItem`
- `RemoveItem`
- `SetItemOrder`
- `SetWarning`

`SetWarning` 与算子类型正交，用于非致命 warning。

## 算子类型表

`internal/types/operator.go` 定义六种算子类型。运行时校验检查每次执行使用的输出方法。

| 类型 | 预期角色 | 允许的输出方法 |
|---|---|---|
| Recall | 产生新行/item | `AddItem` |
| Transform | 变异 common 或 item 字段值 | `SetCommon`、`SetItem` |
| Filter | 移除行/item | `RemoveItem` |
| Merge | 合并/去重行集 | `SetItem`、`RemoveItem` |
| Reorder | 改变 item 顺序 | `SetItemOrder` |
| Observe | 只读副作用 | 无 |

需记住的附加语义：

- Filter、Merge 和 Reorder 是 DAG 构建中的屏障类型。
- Recall 在 item 字段上作为追加型写者。
- Observe 算子不在 DAG 中创建阻塞性 WAR 读者行为。

## 命名规范

算子名应用稳定前缀编码其分类：

- `recall_*`
- `transform_*`
- `filter_*`
- `merge_*`
- `reorder_*`
- `observe_*`

原因：

- 读者可快速推断语义
- Apple DSL 从 `recall_` 前缀推断 recall 行为
- 生成文档和类型化 helper 按这些稳定族分组

不要使用隐藏算子类型的模糊名称。

## 推荐实现模式

`operators/` 中的内置算子通常遵循此结构：

1. 包级文档注释描述算子名、类型、参数和元数据契约
2. `init()` 函数调用 `pine.Register(...)`
3. 结构体嵌入 `pine.MetadataHolder`（当需要元数据时）
4. 可选嵌入 `pine.DebugHolder`（当需要调试信息时）
5. `Init()` 用于参数解析和一次性初始化
6. `Execute()` 用于请求时逻辑

代表性示例：

- recall：`operators/recall/static.go`
- transform：`operators/transform/copy.go`
- filter：`operators/filter/condition.go`
- merge：`operators/merge/dedup.go`
- reorder：`operators/reorder/sort.go`
- observe：`operators/observe/log.go`
- debug-aware transform：`operators/lua/lua.go`

## 元数据契约注释与生成文档

算子源文件中的 Go 文档注释由 `pkg/codegen/docparse.go` 解析，生成 `doc/operators/` 中的 Markdown 文档。

重要边界：

- Schema 注册对名称、类型、参数和描述具有权威性
- 注释解析为生成文档补充元数据契约部分

保持注释与实际元数据用法一致，但优先在 Schema 和代码中修复运行时事实。

## Codegen 影响

算子 Schema 变更影响生成产物：

- `apple_generated/operators.py`
- `apple_generated/__init__.py`
- `doc/operators/`

任何对 Schema 形状、参数类型/默认值或注册表内容的变更都应随后重新生成。CI 通过 generated-diff 门控检查新鲜度。

此外，`pkg/codegen/template.go` 中的 `pythonType()` 会把 Schema 参数定义里的 `Type` 字段映射为 Python 类型注解。当前支持的映射为：`"string"` → `str`、`"int"` / `"int64"` → `int`、`"float64"` → `float`、`"bool"` → `bool`；未识别的类型会回退为 `Any`。新增算子参数类型时，需要同时确认 codegen 映射已覆盖，否则生成的 Python helper 会退化为宽泛类型。

## 元数据契约期望

元数据字段描述算子声明的字段契约，而非偶然的实现细节。

用它们表达：

- 读取哪些 common 字段
- 读取哪些 item 字段
- 写入哪些 common 字段
- 写入哪些 item 字段

这些声明被多个系统消费：

- Apple 校验器（`apple/validator.py`）
- 运行时输入投影（`internal/dataframe/dataframe.go`）
- DAG 依赖推导（`internal/dag/dag.go`）
- 生成的算子文档（`pkg/codegen/`）

不正确的元数据因此可同时导致编译时和运行时的错误行为。

## 常见陷阱

- 忘记参数描述导致注册 panic。
- 使用保留键作为业务参数意味着它永远不会到达 `Init()`。
- 通过错误的输出方法写入将使运行时校验失败。
- 在算子结构体上存储请求本地可变状态可能破坏并发执行。
- 更改 Schema 但不重新生成 `apple_generated/` 和 `doc/operators/` 将导致 CI 失败。

## 检索指针

- 接口和类型约束：`internal/types/operator.go`
- IO helper：`internal/types/operator_io.go`
- 注册表校验和保留键：`internal/registry/registry.go`
- 公共包装器：`operator.go`、`operator_io.go`、`registry.go`
- 内置示例：`operators/`
- Codegen 消费路径：`pkg/codegen/codegen.go`、`pkg/codegen/template.go`、`pkg/codegen/docparse.go`
