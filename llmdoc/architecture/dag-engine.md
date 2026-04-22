# DAG 引擎架构

本文档描述 Pineapple 最深层的执行模型：JSON 如何成为不可变引擎、DAG 如何推导、算子如何调度，以及哪些不变量保证了正确性。

## 适用范围

当任务涉及以下文件时使用本文档：

- `pine.go`
- `internal/config/`
- `internal/dag/`
- `internal/runtime/`
- `internal/dataframe/`
- `internal/types/`

这是核心运行时的检索路径。

## 引擎生命周期

`pine.go` 构建一次 `Engine` 后跨请求复用。引擎本身在构建后不可变，对并发 `Execute()` 调用是安全的。

### 四步编译流水线

`pine.NewEngine()` 遵循固定的编译流水线：

1. **解析 JSON 配置**（`internal/config.Load`）
   - 读取根配置。
   - 将引擎保留键与业务参数分离。
   - 校验最低配置结构。

2. **展开算子序列**（`internal/config.ExpandOperatorSequence`）
   - 解析 `pipeline_group["main"]`。
   - 按 `pipeline_map` 顺序展开。
   - 生成用于校验和 DAG 构建的扁平算子序列。

3. **构建算子实例**（`internal/registry.BuildOperator`）
   - 查找已注册的 Schema。
   - 从参数中过滤保留键。
   - 应用默认值和必填参数检查。
   - 调用 `factory()` 然后 `Init(params)`。
   - 对 `MetadataAware` 和 `DebugAware` 算子应用引擎级注入。

4. **构建 DAG**（`internal/dag.Build`）
   - 推导屏障边。
   - 推导数据冒险边。
   - 添加显式 `sources` 边。
   - 传递性归约：移除被更长路径隐含的冗余边，保留保持可达性的最小边集。
   - 运行拓扑排序校验。

输出是不可变的 `runtime.Plan`，包含图、编译后算子和 flow contract。

## 每请求执行生命周期

`Engine.Execute()` 使用预编译的 plan，但创建全新的请求状态：

1. 根据 flow contract 校验传入请求。
2. 从请求的 common 字段和 items 创建请求本地的 `internal/dataframe.Frame`。
3. 在 `internal/runtime.Run` 中运行调度器。
4. 将最终 frame 投影到声明的结果字段。

此拆分很重要：

- 编译时工作属于引擎构建阶段
- 可变状态属于请求本地 frame
- 算子实例跨请求共享，必须支持并发执行

## DAG 构建模型

`internal/dag/dag.go` 从执行语义推导依赖关系，而非要求用户手动指定所有边。

### 图模型

图按 DSL 声明顺序存储算子。每个节点追踪前驱和后继索引。名称→索引查找支持显式 source 引用和 merge 边构建。

声明顺序很重要，因为冒险追踪器按序列遍历算子并从该顺序推导因果关系。

## 四阶段构建算法

### 阶段 1：屏障边

屏障算子包括：

- Filter
- Merge
- Reorder

对每个屏障算子，Pineapple 添加：

- 从所有更早算子到该屏障的边
- 从该屏障到所有更晚算子的边

这使屏障成为全序栅栏。

存在原因：

- Filter 可能移除行，改变后续所有算子看到的 item 集合
- Merge 合并多源结果，必须观察到所有更早的贡献
- Reorder 全局改变 item 顺序，必须在后续 item 消费者执行前稳定

屏障语义有意强于普通字段冒险。

### 阶段 2：数据冒险边

冒险扫描运行两遍：

- 一遍处理 common 字段
- 一遍处理 item 字段

每遍使用逐字段追踪器，维护三个状态：

- `lastMutWriter` — 最近的覆写型写者
- `additiveWriters` — 不互相冲突的追加型写者
- `activeReaders` — 可能产生 WAR 边的读者

扫描按 DSL 顺序遍历算子，先处理读后处理写。

#### 读处理

当算子读取一个字段时，Pineapple 从以下来源添加 RAW 依赖：

- 该字段的最新覆写型写者
- 该字段的所有追加型写者

然后可能将该算子注册为活跃读者。

例外：Observe 算子获得 RAW 边但不成为活跃读者。这防止日志或观测算子通过 WAR 边阻塞下游写者。

#### 写处理

当算子覆写一个字段时，Pineapple 添加：

- 从上一个覆写型写者的 WAW 依赖
- 从所有追加型写者的 WAW 依赖
- 从所有活跃读者的 WAR 依赖

然后更新追踪器状态，使该算子成为新的覆写型写者，并按需清除读者/追加状态。

#### 追加型 vs 覆写型写者

此区分是 Pineapple 并行性的核心。

- **覆写型写者**覆盖或结构性改变字段，因此与其他访问冲突。
- **追加型写者**贡献独立数据，下游读者必须看到，但彼此不冲突。

在 item 字段上，Recall 算子被视为追加型写者。这意味着：

- 写相同逻辑 item 字段的 recall 算子之间不产生 WAW/WAR 冲突
- 下游读者依赖所有相关 recall
- 后续覆写型写者仍依赖每个追加型 recall 写者

这就是多个 recall 阶段可以在 merge 或 transform 消费结果前并行运行的原因。

### 阶段 3：显式 merge source

带 `sources` 的算子从每个命名 source 算子添加硬边。这用用户声明的 merge 祖先补充推导出的冒险图。

这对 merge 算子最为重要——当仅凭字段级元数据不够明确时，merge 算子必须等待特定上游生产者。

### 最终校验

传递性归约完成后，`TopologicalSort` 校验图是否无环。环表示由屏障、冒险或显式 source 边暗示的不可能排序。

归约保证可达性不变：若原图中 u 可达 v，归约后仍可达。调度器的执行顺序约束完全由可达性（`done[pred]` channel 的 happens-before 语义）决定，因此归约不改变执行语义。

## 行依赖模型

某些算子依赖 item 集合整体而非特定 item 字段。Pineapple 无需单独的图机制即可建模这种依赖。

### `_row_set_` 哨兵

在 item 字段冒险扫描期间，引擎注入名为 `_row_set_` 的合成哨兵字段。

规则：

- Recall 算子作为 `_row_set_` 的追加型写者。
- 屏障算子重置 `_row_set_` 追踪器。
- `RowDependency=true` 的算子作为 `_row_set_` 的读者。

这捕获了集合级因果关系，例如：

- `transform_size` 等算子需要在完整 recall 行集就绪后才能计算 item 数量
- 等待产生行的 recall，而无需发明假的业务字段名

该哨兵仅限内部使用。用户流水线不应将其视为真实字段。

## 调度器架构

`internal/runtime/scheduler.go` 执行编译后的图。

### 调度模型

调度器使用：

- 每算子一个 goroutine
- 每算子一个 done channel
- 通过 channel close/broadcast 等待前驱
- Frame 实现内部自行保证并发安全（调度器不持有 frame 锁）

每个 goroutine：

1. 等待所有前驱 done channel 或 context 取消。
2. 检查 skip 条件（如有）。
3. 从共享 DataFrame 构建输入快照（Frame 方法自行加锁）。
4. 运行 `Execute`。当 `DataParallel > 1` 时，调度器改为委托 `internal/runtime/parallel.go` 中的 `parallelExecute`：按 item 将输入切成 N 份，启动 N 个带独立 panic recovery 的 goroutine 执行，再在返回调度器前合并输出。
5. 根据算子类型契约校验输出。
6. 将输出应用回 frame（Frame 方法自行加锁）。
7. 记录 trace 和 stats。
8. 关闭自身 done channel，使依赖者可以继续。

`data_parallel` 仅是单节点运行时优化：它不改变 DAG 构建、依赖推导或图结构，调度器拿到的仍是同一张执行图。

调度器仅用一把小 `warningsMu` 保护 warnings 切片。`traces[idx]` 每个 goroutine 写自己的索引，无需锁。`fatalErr` 由 `sync.Once` 保护。

### 为何调度器不需要 frame 锁

DAG 基于字段级数据冒险（RAW/WAW/WAR）建边，保证并发执行的算子访问不同字段。屏障算子（Filter/Merge/Reorder）有全序边，不会与其他算子并发。`done[pred]` channel 的 close 在 Go 内存模型下提供 happens-before 保证，前驱写入对后继可见。

Frame 实现通过内部单个 `sync.RWMutex` 保证并发安全：读操作（`Common`、`Item`、`BuildInput`、`ToResult`）取 RLock，写操作（`SetCommon`、`ApplyOutput`）取 Lock。RowFrame 和 ColumnFrame 使用相同的锁策略。

### Skip 处理

控制流被编译为普通算子加一个 `skip` common 字段引用。运行时调度器直接通过 Frame 方法读取该字段（Frame 内部加锁保证安全）：

- `true` 表示跳过执行
- `false` 表示正常执行

被跳过的算子仍参与图和 trace 流，但不运行业务逻辑。

编译器将控制字段注入 `$metadata.CommonInput` 以建立 DAG 依赖。但引擎在两处过滤 skip 字段，使控制字段对算子透明：
- `pine.go` 在调用 `SetMetadata` 前剔除 skip 字段，算子的 `MetadataHolder.CommonInput` 不含控制字段
- `scheduler.go` 在调用 `BuildInput` 前剔除 skip 字段，算子的 `OperatorInput` 不含控制字段值

DAG 推导仍使用完整的 `$metadata`（含控制字段），因此依赖关系不受影响。

### 错误处理

每个算子 goroutine 包裹了 panic 恢复。

失败行为：

- 第一个致命错误获胜
- `sync.Once` 记录它并在共享 context 上调用 `cancel()`
- 等待中的 goroutine 在取消时解除阻塞并提前停止
- panic 路径包裹为 `PanicError`
- 算子返回的失败成为 `ExecutionError` 或通过引擎的类型化错误模型传播
- `Run` 返回前过滤掉未执行节点的零值 trace 条目（`Name == ""`），因此返回的 trace 仅包含实际执行或跳过的算子

Warning 与致命错误分开。算子可通过 `OperatorOutput.SetWarning` 发出 warning，执行继续。

### Debug trace

当算子配置 `debug=true` 时，调度器捕获：

- 输入快照
- 输出快照
- 计时数据
- 跳过状态

这些填充 `internal/types/trace.go` 记录并在最终结果中返回。

## DataFrame 不变量

`internal/dataframe/` 是请求本地的可变存储，通过 `Frame` 接口抽象。

### 存储模式

`Frame` 接口有两种实现：

- `RowFrame`（`row_frame.go`）— 行存，`items []map[string]any`。结构变更（removals、reorder）高效。
- `ColumnFrame`（`column_frame.go`）— 列存，`columns map[string][]any`。构造时分配极少，字段写入高效；结构变更需遍历所有列。

通过 JSON 配置的 `storage_mode` 字段选择（`"row"` 或 `"column"`，默认 `"row"`）。`NewEngine` 将 mode 存入 `Engine.storageMode`，`Execute` 中通过 `dataframe.NewFrame(mode, common, items)` 创建对应实现。

### 并发安全

Frame 实现内部自行保证并发安全，调度器不持有外部 frame 锁。RowFrame 和 ColumnFrame 均使用单个 `sync.RWMutex`：读操作 RLock，写操作 Lock。

### 结构

两种实现都持有：

- `common map[string]any`
- item 数据（行存为 `[]map[string]any`，列存为 `map[string][]any` + `rowCount`）

`NewFrame` 浅拷贝请求输入，使后续变异不会 alias 调用方持有的 map。

### 输入投影

`BuildInput` 将 frame 投影到算子声明的元数据契约：

- 仅暴露声明的字段
- 对 nil 值应用 `common_defaults` 和 `item_defaults`

这意味着算子行为取决于其元数据契约，而非对完整 frame 的无限制访问。

### Apply 顺序不变量

`ApplyOutput` 始终按以下顺序应用算子输出：

1. common 写入
2. item 字段写入
3. item 移除
4. item 重排序
5. item 添加

此顺序是承载性的。它确保结构性 item 变异发生在普通字段写入之后、recall 添加追加之前。

后果：

- transform 可以安全地在后续 filter 移除行之前写入字段
- 重排序始终应用于当前存活的行，而非即将添加的行
- recall 添加始终在当前行集被该算子的 filter/reorder 处理后到达

任何对此顺序的更改都会改变运行时语义，必须视为架构变更。

### 结果投影

`ToResult` 通过 flow contract 声明的输出字段投影最终 frame。底层 `projectMap` 只投影显式声明的字段：空输出列表表示空输出（该维度不返回任何字段），不会回退为"返回当前存在的全部字段"。

## 算子类型约束

`internal/types/operator.go` 定义六种算子类型，并校验它们可使用哪些 `OperatorOutput` 方法。

### 类型表

| 算子类型 | 运行时角色 | 允许的输出方法 | 屏障 |
|---|---|---|---|
| Recall | 产生新 item | 仅 `AddItem` | 否 |
| Transform | 变异字段值 | `SetCommon`、`SetItem` | 否 |
| Filter | 移除行 | 仅 `RemoveItem` | 是 |
| Merge | 合并或去重行集 | `SetItem`、`RemoveItem` | 是 |
| Reorder | 改变行顺序 | 仅 `SetItemOrder` | 是 |
| Observe | 只读副作用 | 无 | 否 |

这些限制在 `Execute()` 返回后检查，是算子分类体系的运行时强制执行。

### 为何分类体系对 DAG 推导重要

DAG 构建器依赖算子类型（而非仅元数据字段）推导语义：

- 屏障创建全序栅栏
- recall 是追加型 item 写者
- observe 算子不产生活跃读者 WAR 压力
- 配置了 row dependency 的 transform 成为 `_row_set_` 读者

更改类型语义因此会同时影响校验和调度。

## 驱动运行时行为的配置和元数据语义

运行时依赖 `internal/config/types.go` 中的若干配置级字段：

- `$metadata` — 声明的 common/item 输入和输出
- `skip` — 控制流守卫字段
- `recall` — 从 DSL/codegen 约定保留的声明提示
- `sources` — 显式上游 source 引用
- `debug` — trace 捕获开关
- `row_dependency` — 启用 `_row_set_` 读取
- `common_defaults` / `item_defaults` — 快照构建时的输入默认值
- `for_branch_control` — 标记编译器生成的控制算子

虽然这些源自 DSL 或手写 JSON，但其语义在 Go 运行时中强制执行。

## 资源与服务器集成

资源和 HTTP 服务位于引擎旁而非 DAG 核心内部。

- `pkg/resource/` 管理命名资源，支持后台刷新和原子读取。
- `pkg/server/server.go` 加载引擎、启动资源、注入请求上下文、服务 `/health`、`/execute`、`/stats` 和 `/dag`。

配置热加载同时覆盖 Engine 和 ResourceManager。`enginePtr` 和 `resources` 均为 `atomic.Pointer`，`watchConfig` 检测文件变更后调用 `reloadConfig`，创建新 Manager → Start → 原子替换 → Stop 旧 Manager。失败时保持旧配置不变。

此分离很重要：DAG 执行仅依赖请求上下文和编译后的 plan，不依赖服务器特定逻辑。

## DAG 可视化

`internal/dag/visualize.go` 提供两种格式的 DAG 图渲染：

- `RenderDOT(g *Graph) string` — Graphviz DOT 格式
- `RenderMermaid(g *Graph) string` — Mermaid flowchart 格式

节点按算子类型着色（Recall 绿、Transform 蓝、Filter 橙、Merge 紫、Reorder 黄、Observe 灰），标签包含算子名和类型分类。布局方向为自上而下（DOT `rankdir=TB`、Mermaid `graph TB`）。

由于 `Build()` 阶段已对执行图执行传递性归约，`Node.Succs` 本身就是保持可达性的最小边集。渲染函数直接遍历 `Node.Succs` 画边，无需额外归约。

公共 API 通过 `Engine.RenderDAG(format string) (string, error)` 暴露，format 支持 `"dot"` 和 `"mermaid"`。HTTP 端点 `GET /dag?format=dot|mermaid`（默认 `dot`）。

## 需要保持的重要不变量

1. **校验和执行假设相同的算子顺序基础。** 扁平化的 DSL/配置序列是冒险推导的规范顺序。
2. **屏障算子是全序栅栏。** 不要随意弱化它们；许多执行顺序保证依赖于此。
3. **Recall 在 item 字段上是追加写入。** 并行 recall 行为依赖此区分。
4. **Observe 算子是非阻塞读者。** 它们不应创建 WAR 边。
5. **`_row_set_` 是内部哨兵状态。** 它建模行集因果关系而不成为用户可见数据。
6. **DataFrame apply 顺序固定。** Common 写入、item 写入、移除、重排序、添加。
7. **算子实例是共享的。** `Init()` 只发生一次；`Execute()` 必须并发安全。
8. **Frame 实现自行保证并发安全。** 调度器不持有 frame 锁。RowFrame 和 ColumnFrame 均通过内部单个 `sync.RWMutex` 实现读写分离。
9. **执行图经过传递性归约。** `Build()` 阶段移除被更长路径隐含的冗余边。`Node.Preds`/`Node.Succs` 是保持可达性的最小边集，调度器和可视化共用同一边集。

## 检索指针

- 引擎编译和请求生命周期：`pine.go`
- 配置解析和序列展开：`internal/config/load.go`、`internal/config/types.go`
- DAG 推导：`internal/dag/dag.go`
- DAG 可视化：`internal/dag/visualize.go`
- 调度器和 trace 捕获：`internal/runtime/scheduler.go`
- Data-parallel split/merge/execute：`internal/runtime/parallel.go`
- 统计：`internal/runtime/stats.go`
- Frame 接口和工厂：`internal/dataframe/frame.go`
- 行存实现：`internal/dataframe/row_frame.go`
- 列存实现：`internal/dataframe/column_frame.go`
- 算子分类和输出校验：`internal/types/operator.go`
- 共享 request/result/trace/error 类型：`internal/types/`
