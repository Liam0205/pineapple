# 算子开发契约

本参考文档面向新增或修改 Pineapple 算子的开发者。

## 权威文件

以下文件为唯一事实源：

- `pine-go/internal/types/operator.go`
- `pine-go/internal/types/operator_io.go`
- `pine-go/internal/registry/registry.go`
- `pine-go/operator.go`
- `pine-go/operator_io.go`
- `pine-go/registry.go`
- `pine-go/operators/` 下的代表性实现

### Pine-Java 对等实现

Pine-Java 完整实现了全部 18 个内置算子，位于 `pine-java/src/.../operators/`。Java 侧的对等文件为：

- `pine-java/src/.../Operator.java` — 算子接口
- `pine-java/src/.../OperatorInput.java` / `OperatorOutput.java` — IO 类型
- `pine-java/src/.../Registry.java` — 注册表（含独立 Schema 注册与校验）
- `pine-java/src/.../ParamSpec.java` — 参数规格声明
- `pine-java/src/.../OperatorSchema.java` — 算子 Schema 定义
- `pine-java/src/.../operators/AllOperators.java` — 全量注册入口（18 算子含完整 ParamSpec 声明）

Java 侧为独立 Schema 源，拥有完整的 schema-based 注册：`Registry.register(OperatorSchema, Supplier<Operator>)`。`Registry.exportSchemaJSON()` 导出与 Go 格式一致的 JSON，供 CI 交叉验证。`validateAndExtractParams()` 执行与 Go 等效的严格校验：过滤保留键、检查必填参数、注入默认值、拒绝未声明参数。

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

### Pine-Java 执行签名

Java 侧等效签名为 `void execute(CancellationToken token, OperatorInput input, OperatorOutput output) throws PineErrors.OperatorException`。`CancellationToken` 是 volatile boolean，等效于 Go 的 `context.Context` 取消信号。算子中的长时间循环应检查 `token.isCancelled()` 以支持协作式取消。声明 checked `OperatorException` 使引擎能区分预期算子错误（包装为 `ExecutionError`）与意外运行时异常（包装为 `PanicError`），语义对应 Go 的 error/panic 分界。

## 必需接口

公共算子接口通过 `pine-go/operator.go` 暴露，在 `pine-go/internal/types/operator.go` 中定义：

- `Init(params map[string]any) error`
- `Execute(ctx, input, output) error`

使用 `pine-go/operator_io.go` 中的 `OperatorInput` 和 `OperatorOutput`，而非直接触及运行时内部。

### Pine-C++ 注册与 CRTP traits

C++ 侧提供两种注册路径：

- **`PINE_REGISTER_OPERATOR_T(Type, schema)`**（首选）——通过 `OperatorTraits<T>` 在编译期 `std::is_base_of_v` 检查四个 marker 位（`ConsumesRowSet` / `MutatesRowSet` / `AdditiveWritesRowSet` / `ConcurrentSafe`），调用 `register_operator_typed<T>(schema)` → `register_operator_with_traits(schema, factory, ...)` 直接填充 `OperatorEntry` 的标记字段，跳过注册时的 `dynamic_cast` probe 和 factory 调用。重量级构造器（Lua pool、libcurl handle、redis pool seed）只在 per-Engine 实例化时付一次构造成本。
- **`PINE_REGISTER_OPERATOR(schema, factory)`**（legacy）——运行时调用 factory 创建临时实例并 `dynamic_cast` 探测 marker。仍可用但非首选。

两种路径的校验逻辑等价：空 name、空 description、空 param description、null factory、重复 name 均 throw `RegistryError`。

17 个内置算子已全部迁移到 `PINE_REGISTER_OPERATOR_T`。新增 C++ 算子应使用此宏。

## 注册契约

算子通过 `pine-go/registry.go` 中的 `pine.Register(schema, factory)` 注册，通常在算子源文件的 `init()` 函数中。

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

### 严格参数校验

`pine-go/internal/registry/registry.go` 的 `ValidateAndExtractParams` 现在对传入参数执行严格检查：

- 所有未在 `Schema.Params` 中声明的参数名会被拒绝
- 错误消息会列出全部未声明的参数名

此行为的意义：

- 拼写错误的参数名会立即报错，而不是被静默忽略
- `Schema.Params` 必须声明算子接收的所有业务参数，否则引擎加载会失败
- 引擎保留键（`type_name`、`$metadata` 等）在校验前已被剥离，不受影响

因此新增算子或修改算子参数时，`Schema.Params` 必须与实际使用的参数名保持完全一致。

### 注册失败行为

`pine-go/internal/registry.Register` 有意严格，在无效定义时 panic，包括：

- 空算子名
- 无效算子类型
- 空算子描述
- 任何参数缺少描述
- 重复注册

将 Schema 注册视为启动时校验。缺失元数据被认为是程序员错误，不是可恢复的运行时条件。

### 外部构建算子实例

`pine.BuildOperator(typeName, params)` 是 `pine-go/internal/registry.BuildOperator` 的公共包装器，允许外部消费者（benchmark、测试工具）按类型名构建已注册算子的实例。

流程：查找注册表 → 校验参数 → 创建实例 → 调用 `Init(params)` → 返回 `(Operator, OperatorSchema, error)`。

若算子实现 `MetadataAware`，调用方需在 `BuildOperator` 之后手动调用 `SetMetadata` 注入字段元数据（引擎内部会自动做，外部消费者需显式处理）。

## 资源消费模式

算子可通过 `resource.FromContext(ctx)` 从请求上下文中拉取资源。无需实现特殊接口。

约定：

- 声明 `resource_name` (string) 参数——`ValidateResourceDeps` 依赖此命名约定在启动时校验资源依赖
- `Init` 中存储 `resource_name` 参数值
- `Execute` 中调用 `resource.FromContext(ctx).Get(name)` 拉取资源值
- 处理 `nil` provider（未注入）和 `(nil, false)` 返回（资源不存在）

内置资源消费算子：

- `recall_resource` — 资源值为 `[]map[string]any`，逐个 `AddItem`
- `transform_resource_lookup` — 资源值为 `map[string]any`（lookup table），按 item 字段值查找写入。非 string key 自动 coerce（float64 整数 → string）。Apple 编译器校验 `lookup_key` ∈ `item_input`、`output_field` ∈ `item_output`

## 保留 JSON/配置键

这些键为引擎所有，在 `Init(params)` 接收其 map 之前被过滤：

- `type_name`
- `$metadata`
- `$code_info`
- `skip`（运行时配置层为字符串列表；旧版单字符串 JSON 会在加载期兼容归一化）
- `recall`
- `sources`
- `debug`
- `row_dependency`
- `common_defaults`
- `item_defaults`
- `strict_common`
- `strict_item`
- `for_branch_control`
- `data_parallel`

不要定义依赖这些名称的业务参数。

## 可选接口

### `ConcurrentSafe`

若算子希望在 `data_parallel > 1` 时进入并发分片执行路径，必须实现 `pine-go/internal/types/operator.go` 中的 `ConcurrentSafe` 可选接口。

推荐模式：

- 直接嵌入 `pine.ConcurrentSafeMarker`
- 只在确认同一算子实例可被多个 shard 并发重入调用时才声明该能力

职责边界：

- Apple/编译期只做结构性校验：仅 Transform 可启用 `data_parallel`，且 `$metadata.common_output` 必须为空
- Go/引擎加载期在 `pine.NewEngine()` 的 `validateDataParallel` 中做最终能力检查：`data_parallel > 1` 时实例必须实现 `ConcurrentSafe`

未实现 `ConcurrentSafe` 的 Transform 默认不允许启用 `data_parallel > 1`。这替代了旧的名称 blocklist 模式，把事实源收敛到真实持有算子实例的 Go 层。

当前内置 Transform 中，8 个逐 item 独立实现已标记 `ConcurrentSafe`；`transform_normalize` 保持未标记，因为它依赖完整 item 集合语义。

### `MetadataAware`

若算子实现 `pine-go/internal/types/operator.go` 中的 metadata-aware 接口，引擎将在 `Init()` 后注入字段元数据。

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

`DebugHolder.OperatorName()` 会返回引擎注入的算子实例名。它不仅用于 debug log，也可被后续 `MetricsAware` 注入阶段复用，例如把 operator 名作为外部 metric label 值。

### `StatsProvider`

若算子实现 `StatsProvider`，引擎会在 `Engine.OperatorCustomStats()` 中收集该算子的自定义原子统计，并由 `pine-go/pkg/server/server.go` 挂载到 `/stats` 响应中的 `operator_detail` 字段。

该接口适合暴露零配置排障所需的进程内累计计数，例如 Lua state pool 的 borrow / return / create / active 计数。

### `MetricsAware`

若算子实现 `MetricsAware`，引擎会在 `Init()`、`SetMetadata(...)`、`SetDebugInfo(...)` 之后调用 `SetMetricsProvider(provider)`。

稳定注入顺序为：

1. `MetadataAware`
2. `DebugAware`
3. `MetricsAware`

这使得像 Lua 算子这样的实现可以在 `SetMetricsProvider` 内安全读取 `DebugHolder.OperatorName()`，把 operator 名绑定为 label 值。

设计边界：

- `MetricsAware` 面向外部指标系统，不替代 `/stats`
- provider 可能是 `metrics.Nop()`，实现必须把 no-op provider 视为正常路径
- Pineapple core 不依赖具体 Prometheus SDK；算子只依赖 `pine-go/pkg/metrics` 抽象

## 输入/输出 API 契约

### 从 `OperatorInput` 读取

使用只读访问器：

- `Common(field)`
- `Item(index, field)`
- `ItemCount()`
- `CommonKeys()`
- `ItemKeys(index)`

不要假设完整 frame 或任意未声明字段存在；输入从声明的元数据投影。

字段访问遵循 InputFieldSpec 三态模型：

- **Nullable**（默认）：字段缺失 → error；值为 nil → 透传 nil 给算子。大多数字段的默认行为。
- **Strict**（通过 `strict_common` / `strict_item` opt-in）：字段缺失或值为 nil → error。适用于算子逻辑无法处理 nil 的必需字段。
- **Defaulted**（通过 `common_defaults` / `item_defaults`）：字段缺失或值为 nil → 替换为默认值。

#### 字段模式 JSON 键 ↔ 各层字段映射

| JSON 键 | Apple DSL（OpCall 字段） | pine-go（OperatorConfig） | pine-java | pine-python | pine-cpp |
|---|---|---|---|---|---|
| `strict_common` | `strict_common` | `StrictCommon` | `strictCommon` | `strict_common` | `strict_common` |
| `strict_item` | `strict_item` | `StrictItem` | `strictItem` | `strict_item` | `strict_item` |
| `common_defaults` | `common_defaults` | `CommonDefaults` | `commonDefaults` | `common_defaults` | `common_defaults` |
| `item_defaults` | `item_defaults` | `ItemDefaults` | `itemDefaults` | `item_defaults` | `item_defaults` |

当涉及字段模式相关的 JSON 键名变更时，必须同步检查此表中所有列。历史教训：v0.9.0 翻转默认模式时运行时完成迁移但 Apple DSL 侧遗漏，导致声明能力丧失（详见 `memory/reflections/v090-nullable-strict-apple-desync.md`）。

### Pine-C++ OperatorInput 投影层

C++ 侧 `OperatorInput`（`include/pine/operator_input.hpp`）是 Frame + InputFieldSpec 之上的 lazy read-only proxy。与 Go/Java/Python 的 eager map 构建不同，C++ 采用按需读取策略：

- `build_operator_input(frame, op_name, spec)` 先批量校验 strict 字段（`Frame::batch_validate_strict_items` 虚方法，ColumnFrame/RowFrame 各自实现最优路径），校验通过后构造 proxy
- `common(field)` / `item(i, field)` 在调用时才从 Frame 读取，自动替换 defaulted 字段的 nil 值为默认值
- 算子签名为 `execute(const OperatorInput&, OperatorOutput&)`，与 Go `Execute(ctx, *OperatorInput, *OperatorOutput)` 语义等价

性能收益：避免 O(N×M) eager reify（N items × M fields），大管道中节省显著分配与拷贝开销。

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

`pine-go/internal/types/operator.go` 定义六种算子类型。运行时校验检查每次执行使用的输出方法。

| 类型 | 预期角色 | 允许的输出方法 |
|---|---|---|
| Recall | 产生新行/item | `AddItem` |
| Transform | 变异 common 或 item 字段值 | `SetCommon`、`SetItem` |
| Filter | 移除行/item | `RemoveItem` |
| Merge | 合并/去重行集 | `SetItem`、`RemoveItem` |
| Reorder | 改变 item 顺序 | `SetItemOrder` |
| Observe | 只读副作用 | 无 |

`data_parallel` 仅支持 Transform。启用时，`$metadata.common_output` 必须为空；其他算子类型会在 Apple 编译期与 Go 引擎加载期的结构校验中被拒绝。

当算子被用于 `data_parallel > 1` 时，还必须满足额外契约：同一个算子实例会在单次请求内被多个 shard 并发调用，因此实例必须显式实现 `ConcurrentSafe`，才能通过 `pine.NewEngine()` 的加载期校验并进入并发路径。未实现 `ConcurrentSafe` 的 Transform 默认不允许这样使用。

需记住的附加语义：

- Filter、Merge 和 Reorder 是 DAG 构建中的屏障类型。
- Recall 在 item 字段上作为追加型写者。
- Observe 算子不在 DAG 中创建阻塞性 WAR 读者行为。
- 有 item 字段（`item_input` 或 `item_output` 非空）的 Transform/Observe 通过 auto-inject 自动获得 `_row_set_` 读依赖，无需显式标记。

### ConsumesRowSet 与 auto-inject

引擎的 DAG 构建器会自动为具有非空 `item_input` 或 `item_output` 的算子注入 `_row_set_` 读依赖，确保这些算子在行集稳定后才执行。因此：

- **不需要显式 ConsumesRowSet 的情况**：算子在 metadata 中声明了 item 字段。auto-inject 机制会自动处理依赖。大多数 Transform 和 Observe 算子属于此类。
- **需要显式 ConsumesRowSet 的情况**：算子的 metadata 中 item 字段为空，但仍然结构性地访问行集。典型例子是 `transform_size`（调用 `ItemCount()` 但 metadata 无 item 字段）和 `transform_remote_pineapple`（无条件读取并转发 items）。这些算子的行集依赖无法从 metadata 推断，必须通过标记显式声明。

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

`pine-go/operators/` 中的内置算子通常遵循此结构：

1. 包级文档注释描述算子名、类型、参数和元数据契约
2. `init()` 函数调用 `pine.Register(...)`
3. 结构体嵌入 `pine.MetadataHolder`（当需要元数据时）
4. 可选嵌入 `pine.DebugHolder`（当需要调试信息时）
5. 可选实现 `pine.StatsProvider`（当需要把进程内累计统计暴露到 `/stats` 时）
6. 可选实现 `pine.MetricsAware`（当需要向外部 provider 记录指标时）
7. `Init()` 用于参数解析和一次性初始化
8. `Execute()` 用于请求时逻辑

代表性示例：

- recall：`pine-go/operators/recall/static.go`
- transform：`pine-go/operators/transform/copy.go`
- filter：`pine-go/operators/filter/condition.go`
- merge：`pine-go/operators/merge/dedup.go`
- reorder：`pine-go/operators/reorder/sort.go`
- observe：`pine-go/operators/observe/log.go`
- 跨服务 transform：`pine-go/operators/transform/remote_pineapple.go`
- debug-aware transform：`pine-go/operators/lua/lua.go`
- stats + metrics aware transform：`pine-go/operators/lua/lua.go`、`pine-go/operators/lua/pool.go`

## Lua 沙箱与安全模型

`pine-go/operators/lua/pool.go` 创建的 Lua VM 实施严格的沙箱隔离：

- 使用 `glua.Options{SkipOpenLibs: true}` 创建裸 state，默认不加载任何库
- 仅显式加载安全子集：`base`、`table`、`string`、`math`
- `dofile` 和 `loadfile` 被显式设为 `LNil`，阻止文件系统访问
- 未加载 `os`、`io`、`debug` 等危险库

此沙箱模型确保用户编写的 Lua 脚本无法：

- 访问宿主文件系统
- 执行系统命令
- 操作进程环境

### Context 取消传播

`pine-go/operators/lua/lua.go` 的 `Execute` 方法通过 `L.SetContext(ctx)` / `defer L.RemoveContext()` 把请求 context 注入 Lua VM。GopherLua 在指令边界检查 context cancellation，因此长时间运行的 Lua 脚本会在 context 超时或取消时被中断。

这是算子尊重 context 的典型模式：即使执行逻辑在外部 VM 中运行，仍然必须传播 Go context 以保证请求取消的及时性。

### Lua Pool 关闭保护

`pine-go/operators/lua/pool.go` 的 `newState()` 在 mutex 内检查 `sp.closed` 状态。若 pool 已关闭：

- 立即 `L.Close()` 释放刚创建的 state
- 返回 `errPoolClosed` 错误
- `Borrow()` 对 nil state 优雅处理

此保护避免 hot-reload teardown 过程中创建的 state 泄漏到已关闭的 pool。

## 元数据契约注释与生成文档

算子源文件中的文档注释由语言特定的解析器提取 metadata contract：
- Go 侧：`pine-go/pkg/codegen/docparse.go` 解析包级文档注释中的 `// Operator:` + `Metadata contract` 块
- Java 侧：`Codegen.java` 的 `parseJavadocMetadata()` 解析 Javadoc `/** Operator: ... */` 块

两侧使用相同的标注协议：

    Operator: <operator_name>
    Metadata contract
      CommonInput:  [<fields>]
      CommonOutput: [<fields>]
      ItemInput:    [<fields>]
      ItemOutput:   [<fields>]

重要边界：

- Schema 注册对名称、类型、参数和描述具有权威性
- 注释解析为生成文档补充元数据契约部分

保持注释与实际元数据用法一致，但优先在 Schema 和代码中修复运行时事实。

## Codegen 影响

算子 Schema 变更影响生成产物：

- `apple_generated/operators.py`
- `apple_generated/__init__.py`
- `doc/operators/`

Pine-Java 的 `Codegen.java` 支持双模式生成：

- `--export-schema <path>` — 从内部 Registry 导出 Schema JSON（供 CI 交叉验证）
- `--schema-from-registry` — 从内部 Registry 直接生成 Python DSL 产物
- `-schema <path>` — legacy 模式（读取外部 JSON，保留兼容性）

两侧 codegen 输出保持一致（`operators.py`、`resources.py`、`__init__.py`、`doc/operators/`）。

任何对 Schema 形状、参数类型/默认值或注册表内容的变更都应随后重新生成。CI 通过 generated-diff 门控检查新鲜度。

此外，`pine-go/pkg/codegen/template.go` 中的 `pythonType()` 会把 Schema 参数定义里的 `Type` 字段映射为 Python 类型注解。当前支持的映射为：`"string"` → `str`、`"int"` / `"int64"` → `int`、`"float64"` → `float`、`"bool"` → `bool`；未识别的类型会回退为 `Any`。新增算子参数类型时，需要同时确认 codegen 映射已覆盖，否则生成的 Python helper 会退化为宽泛类型。

Codegen 模板对参数序列化采用分类策略（`alwaysParams` / `conditionalParams`）：required 或有 Default 的参数总是写入 `_params` dict；optional 且无 Default 的参数（如 `default_value`）仅在 `is not None` 时条件写入。生成 helper 的 Python 默认参数必须使用 Schema 的 `Default` 值；不要用类型零值覆盖 Go 注册表默认语义。这避免了 Python `None` 被序列化为 JSON `null` 导致 Go 侧误判参数存在，也避免 `order=""`、`timeout=0.0` 等值覆盖 Schema 默认值。

## 元数据契约期望

元数据字段描述算子声明的字段契约，而非偶然的实现细节。

用它们表达：

- 读取哪些 common 字段
- 读取哪些 item 字段
- 写入哪些 common 字段
- 写入哪些 item 字段

这些声明被多个系统消费：

- Apple 校验器（`apple/validator.py`）
- 运行时输入投影（`pine-go/internal/dataframe/dataframe.go`）
- DAG 依赖推导（`pine-go/internal/dag/dag.go`）
- 生成的算子文档（`pine-go/pkg/codegen/`）

不正确的元数据因此可同时导致编译时和运行时的错误行为。

## 跨运行时格式化（GoFormat）

Java 算子需要生成与 Go 运行时一致的字符串表示时（如 Redis key 拼接、lookup table key coerce、条件比较值格式化），必须使用 `GoFormat` 工具类：

- `GoFormat.sprint(v)` — 替代 `String.valueOf(v)`
- `GoFormat.formatFloatF(d)` — 替代 `Double.toString(d)` 用于十进制表示
- `GoFormat.formatG(d)` — 替代 `String.format("%g", d)`

所有需要跨运行时格式一致性的算子均应通过 GoFormat 实现。已完成统一的消费者：

- `TransformResourceLookup` — key coerce
- `TransformRedisGet` — key 拼接
- `FilterCondition` — 条件比较值（原 `formatValue` 已移除）
- `ReorderShuffle` — salt 格式化（原 `formatFloatG` 已移除）

新增算子若对用户值做字符串转换，且该结果参与跨运行时比较（Redis key、fixture 断言），应使用 GoFormat。

## 常见陷阱

- 忘记参数描述导致注册 panic。
- 使用保留键作为业务参数意味着它永远不会到达 `Init()`。
- 通过错误的输出方法写入将使运行时校验失败。
- 在算子结构体上存储请求本地可变状态可能破坏并发执行。
- 更改 Schema 但不重新生成 `apple_generated/` 和 `doc/operators/` 将导致 CI 失败。

## 网络调用安全约束

执行外部网络调用的算子（如 `transform_by_remote_pineapple`）必须遵循以下安全契约：

### SSRF 防护

- Init 阶段对目标地址做 DNS 解析校验，拒绝解析到 private/loopback 范围的地址
- 运行时通过自定义 `http.Transport` 的 `DialContext` 在拨号时再次检查解析后的 IP，防止 DNS rebinding
- 提供 `allow_private` 参数，允许在合法内网调用场景下显式跳过 SSRF 检查

### 有界响应读取

- 禁止裸 `io.ReadAll`；必须使用 `io.LimitReader(body, maxSize+1)` 读取外部响应
- 读取后检查实际长度是否超过 `maxSize`，超过则返回错误
- 通过 `max_response_size` 参数暴露上限配置，默认 10MB

### 基础设施错误处理模式

- 非致命基础设施错误（如 Redis 连接失败）应通过 `output.SetWarning()` 透传，而非静默吞没
- 提供 `fail_on_error` 参数，允许调用方切换为严格模式（基础设施错误 → 算子失败）
- 这是跨算子可复用的错误处理模式，适用于任何依赖外部服务的算子

## 检索指针

- 接口和类型约束：`pine-go/internal/types/operator.go`
- IO helper：`pine-go/internal/types/operator_io.go`
- 注册表校验和保留键：`pine-go/internal/registry/registry.go`
- 公共包装器：`pine-go/operator.go`、`pine-go/operator_io.go`、`pine-go/registry.go`
- 内置示例：`pine-go/operators/`
- Codegen 消费路径：`pine-go/pkg/codegen/codegen.go`、`pine-go/pkg/codegen/template.go`、`pine-go/pkg/codegen/docparse.go`
