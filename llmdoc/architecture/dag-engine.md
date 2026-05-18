# DAG 引擎架构

本文档描述 Pineapple 最深层的执行模型：JSON 如何成为不可变引擎、DAG 如何推导、算子如何调度，以及哪些不变量保证了正确性。

## 适用范围

当任务涉及以下文件时使用本文档：

- `pine-go/pine.go`
- `pine-go/internal/config/`
- `pine-go/internal/dag/`
- `pine-go/internal/runtime/`
- `pine-go/internal/dataframe/`
- `pine-go/internal/types/`

这是核心运行时的检索路径。

## 引擎生命周期

`pine-go/pine.go` 构建一次 `Engine` 后跨请求复用。引擎本身在构建后不可变，对并发 `Execute()` 调用是安全的。

### Engine options 与根级运行时配置

`pine.NewEngine(jsonConfig, opts...)` 现在接受可选的引擎级 option。当前稳定 option 为：

- `pine.WithMetrics(provider)` — 为引擎和支持的算子注入 `pine-go/pkg/metrics.Provider`
- `pine.WithLogPrefix(prefix)` — 为进程级标准库 logger 设置全局日志前缀，并启用带 `file:line` 的标准日志 flags
- `pine.WithDebug(enabled)` — 覆盖根级 debug 配置，强制开启或关闭全局调试采集

若调用方未提供 provider，引擎会回退到 `metrics.Nop()`；该默认实现丢弃全部观测，保留零配置 `/stats` 能力，同时避免 Pineapple 核心直接依赖任意具体监控后端。

日志前缀的来源有两层：

- JSON 根级 `log_prefix`
- Go option `pine.WithLogPrefix(...)`

优先级固定为 Go option 高于 JSON 配置。`NewEngine` 在解析完 `pine-go/internal/config.RootConfig` 后调用标准库 `log.SetPrefix()` 应用最终值，并同时调用 `log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)`，因此日志输出会包含 `file:line`，且该前缀与 flags 会一起影响引擎日志以及复用标准库 logger 的第三方算子日志。

全局 debug 的来源同样有两层：

- JSON 根级 `debug`
- Go option `pine.WithDebug(...)`

优先级同样固定为 Go option 高于 JSON 配置，但实现上需要额外区分“未设置 option”和“显式传入 false”。因此 `WithDebug` 在 `NewEngine` 内部通过 `*bool` 风格状态表达三态：未设置时沿用 JSON，显式 `true/false` 时覆盖 JSON。最终值一旦确定，`NewEngine` 会把每个编译后算子的 `opCfg.Debug` 统一置为该值，使 flow 级开关在运行时表现为“所有算子都启用/禁用 debug”。

该设计明确区分两类运行时注入：

- 观测 provider 注入：面向 `pine-go/pkg/metrics` 的可插拔外部指标
- 日志前缀注入：面向标准库全局 logger 的进程级日志格式控制
- 全局 debug 注入：面向全部编译后算子的运行时 debug trace 默认开关

同时，Pineapple 的观测通道仍保持分离：

- 原子统计：始终开启，驱动 `Engine.Stats()`、`Engine.SchedulerStats()` 和服务器 `/stats`
- Provider metrics：按需开启，供应用侧接入 Prometheus 等外部监控系统

### 四步编译流水线

`pine.NewEngine()` 遵循固定的编译流水线：

1. **解析 JSON 配置**（`pine-go/internal/config.Load`）
   - 读取根配置。
   - 将引擎保留键与业务参数分离。
   - 校验最低配置结构。

2. **展开算子序列**（`pine-go/internal/config.ExpandOperatorSequenceWithSubFlows`）
   - 解析 `pipeline_group["main"]`。
   - 将 `pipeline_group.main.pipeline` 视为递归入口：条目既可以是叶子算子名，也可以是 `pipeline_map` 中的 SubFlow 路径。
   - 递归展开 `pipeline_map`，生成用于校验和 DAG 构建的扁平算子序列。
   - 同时生成 `opToSubFlow` 映射，记录每个算子的直接父 SubFlow 路径；顶层算子映射为空字符串。
   - 对 SubFlow 引用做环检测；循环引用会在加载期报错。

3. **校验 `sources` 顺序引用**（`pine.validateSourcesOrder`）
   - 在扁平序列上按声明顺序扫描 `sources`。
   - 只允许引用已经出现在当前算子之前的命名上游。
   - 不存在的 source 名称通常会更早在 `pine-go/internal/config.Load` 的 `validate()` 中被拦截；这里重点兜底前向引用。
   - 这与 Apple 侧的 `validate_sources_references` 形成纵深防御，保证手写 JSON 或绕过 DSL 的输入也不能构造因果倒置的 source 边。

4. **构建算子实例**（`pine-go/internal/registry.BuildOperator`）
   - 查找已注册的 Schema。
   - 从参数中过滤保留键。
   - 应用默认值和必填参数检查。
   - 调用 `factory()` 然后 `Init(params)`。
   - 对 `MetadataAware`、`DebugAware`、`MetricsAware` 和 `ResourceAware` 算子按固定顺序应用引擎级注入：Metadata → Debug → Metrics → Resource。
   - `MetricsAware` 注入使用 `NewEngine` option 中提供的 `metrics.Provider`；默认是 no-op provider。

4. **构建 DAG**（`pine-go/internal/dag.Build`）
   - 推导屏障边。
   - 推导数据冒险边。
   - 添加显式 `sources` 边。
   - 传递性归约：移除被更长路径隐含的冗余边，保留保持可达性的最小边集。
   - 运行拓扑排序校验。

输出是不可变的 `runtime.Plan`，包含图、编译后算子和 flow contract。

## 每请求执行生命周期

`Engine.Execute()` 使用预编译的 plan，但创建全新的请求状态：

1. 根据 flow contract 校验传入请求。
2. 从请求的 common 字段和 items 创建请求本地的 `pine-go/internal/dataframe.Frame`。
3. 在 `pine-go/internal/runtime.Run` 中运行调度器。
4. 将最终 frame 投影到声明的结果字段。

此拆分很重要：

- 编译时工作属于引擎构建阶段
- 可变状态属于请求本地 frame
- 算子实例跨请求共享，必须支持并发执行

## DAG 构建模型

`pine-go/internal/dag/dag.go` 从执行语义推导依赖关系，而非要求用户手动指定所有边。

### 图模型

图按 DSL 声明顺序存储算子。每个节点追踪前驱和后继索引。名称→索引查找支持显式 source 引用和 merge 边构建。

每个 `Node` 还带有 `SubFlow` 字段，用来记录该算子直接归属的 SubFlow 层级路径，例如 `recall/candidates`。顶层算子记为空字符串。这个字段不参与调度、冒险分析或拓扑约束；它只作为稳定的可视化分组元数据，供 collapsed DAG 渲染使用。

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

稳定约束是：`sources` 只能引用已经在扁平声明序列中出现过的命名上游，不能前向引用未来节点。Go 侧在 `pine-go/pine.go` 的 `validateSourcesOrder` 中对展开后的序列再次校验这条规则；不存在名称的情况通常更早由 `config.Load` 拒绝，运行到这里的重点是阻止”名字存在但顺序在后”的因果倒置配置。

这对 merge 算子最为重要——当仅凭字段级元数据不够明确时，merge 算子必须等待特定上游生产者。
### 最终校验

传递性归约完成后，`TopologicalSort` 校验图是否无环。环表示由屏障、冒险或显式 source 边暗示的不可能排序。

当检测到环时，错误信息不再是泛化的 “DAG contains a cycle”，而是会列出仍有入度、也就是实际参与环的算子名，例如 `DAG contains a cycle involving operators: [recall_a merge ranker]`。这让手写 JSON、DSL 降级后的控制流、或复杂的 SubFlow/source 组合在出现闭环时，可以直接定位到参与循环的算子集合，而不是只知道“某处有环”。

诊断语义还包含一个重要边界：错误中只报告真正处于环内的节点，不会把仅仅依赖该环、但自身不属于闭环的下游节点一起报出来。

归约保证可达性不变：若原图中 u 可达 v，归约后仍可达。调度器的执行顺序约束完全由可达性（`done[pred]` channel 的 happens-before 语义）决定，因此归约不改变执行语义。

## DAG 测试覆盖面

`pine-go/internal/dag/dag_test.go` 现在不仅覆盖单一 hazard 或单一 barrier 语义，还专门覆盖多特性交互与诊断质量。

### 环诊断测试

- `TestTopologicalSortCycleReportsNodeNames` — 验证完整环会在错误中报告所有参与闭环的算子名。
- `TestTopologicalSortPartialCycleReportsOnlyCycleNodes` — 验证错误只包含真正位于环中的节点，不把环外下游节点误报为 cycle participant。

这两项测试与 `TopologicalSort` 的新错误格式共同构成稳定契约：DAG 构建失败时，调用方可以依赖错误中的算子名集合做人工排查或上层报错包装。

### 组合语义回归测试

以下测试补强了 Pineapple DAG 的“语义组合面”，确保多个机制叠加时仍保持既有不变量：

- `TestNestedSubFlowWithBarrierAndSources` — 覆盖 recall + merge + filter + SubFlow 的组合，确认嵌套 SubFlow 场景下 barrier 与显式 `sources` 边仍能共同给出正确依赖骨架。
- `TestControlFlowWithBarrierInteraction` — 覆盖控制字段依赖与 barrier 语义交互，确认控制流守卫不会破坏屏障建立的全序约束。
- `TestRowDepWithRecallAndBarrierCombined` — 覆盖 row dependency、recall additive 写入与 barrier reset 的组合，确认 `_row_set_` 哨兵在多阶段 item 集合变更下仍能表达正确因果关系。
- `TestObserveInNestedSubFlowDoesNotBlock` — 覆盖嵌套 SubFlow 中的 observe 非阻塞语义，确认 observe 仍只建立 RAW 依赖，不会在复杂图形里意外产生 WAR 阻塞。

这些测试的意义不只是增加 case 数量，而是把几个容易一起回归的核心不变量钉住：

- barrier 仍是全序栅栏
- recall 仍是 item 行集上的 additive writer
- row dependency 仍只通过 `_row_set_` 建模，而不是退化成对无关列写入的过度串行化
- observe 仍是非阻塞读者，即使放进嵌套 SubFlow 或与 barrier、control-flow 混合
- `sources` 仍是对推导图的显式补边，而不是绕开 hazard / barrier 规则的替代机制

因此，`pine-go/internal/dag/dag_test.go` 现在既验证单点规则，也验证这些规则在真实流水线组合形态下不会互相破坏。

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

`pine-go/internal/runtime/scheduler.go` 执行编译后的图。

### 调度模型

调度器使用：

- 每算子一个 goroutine
- 每算子一个 done channel
- 通过 channel close/broadcast 等待前驱
- Frame 实现内部自行保证并发安全（调度器不持有 frame 锁）
- 双通道观测：同一执行路径同时更新原子统计（`pine-go/internal/runtime/stats.go`）与外部 Provider metrics（`pine-go/internal/runtime/engine_metrics.go`）

每个 goroutine：

1. 等待所有前驱 done channel 或 context 取消。
2. 检查 skip 条件（如有）。
3. 从共享 DataFrame 构建输入快照（Frame 方法自行加锁）。
4. 运行 `Execute`。当 `DataParallel > 1` 时，调度器改为委托 `pine-go/internal/runtime/parallel.go` 中的 `parallelExecute`：按 item 将输入切成 N 份，启动 N 个带独立 panic recovery 的 goroutine 执行，再在返回调度器前合并输出。该并发路径只会出现在引擎加载期已通过 `pine-go/pine.go` `validateDataParallel` 校验的算子上：Apple 侧仅做结构性校验（必须是 Transform、且不能声明 `common_output`），真正的能力判定由 Go 在持有算子实例时完成，要求实例实现 `pine-go/internal/types/operator.go` (`ConcurrentSafe`)。
5. 根据算子类型契约校验输出。
6. 将输出应用回 frame（Frame 方法自行加锁）。
7. 记录 trace 和双通道 stats/metrics。
8. 关闭自身 done channel，使依赖者可以继续。

`data_parallel` 仅是单节点运行时优化：它不改变 DAG 构建、依赖推导或图结构，调度器拿到的仍是同一张执行图。

该能力只适合逐 item 独立的 Transform，并采用显式 opt-in 模型：

- Apple/编译期只做结构性约束：`data_parallel > 1` 时算子必须是 Transform，且 `$metadata.common_output` 必须为空。
- Go/引擎加载期在 `pine.NewEngine()` 的 `validateDataParallel` 中做最终能力检查：实例必须实现 `pine-go/internal/types/operator.go` (`ConcurrentSafe`)。
- `pine-go/internal/types/operator.go` 同时提供可嵌入的 `ConcurrentSafeMarker`，作为内置和外部算子的标准声明方式。
- 未实现 `ConcurrentSafe` 的 Transform 默认不允许进入并发分片路径；当前 `transform_normalize` 保持未标记，因为它依赖整个 item 集合语义，按 shard 执行会把全局 min/max 变成分片 min/max。

这把 `data_parallel` 的实例能力判定收敛到真实持有算子实例的 Go 层，替代旧的跨 Python/Go 名称 blocklist 模式，避免双端事实源漂移。

原子统计与 Provider metrics 分工如下：

- `Stats.RecordRun()` / `SchedulerRuns.Inc()`：记录每次 `Engine.Execute()` 触发的一次调度器运行
- `Stats.RecordConcurrency()` / `ActiveOps.Add(±1)`：追踪历史峰值并上报当前活跃算子数
- `RecordExec`、`RecordSkip`、`RecordError` 与 `OpExecTotal`、`OpSkipTotal`、`OpErrorTotal`、`OpExecDuration`：分别记录成功、跳过、失败与执行时长

`pine-go/internal/runtime/engine_metrics.go` 在 `NewEngine` 时预创建 scheduler 级 metric handle，避免请求热路径反复声明 metric。

### 为何调度器不需要 frame 锁

DAG 基于字段级数据冒险（RAW/WAW/WAR）建边，保证并发执行的算子访问不同字段。屏障算子（Filter/Merge/Reorder）有全序边，不会与其他算子并发。`done[pred]` channel 的 close 在 Go 内存模型下提供 happens-before 保证，前驱写入对后继可见。

Frame 实现通过内部单个 `sync.RWMutex` 保证并发安全：读操作（`Common`、`Item`、`BuildInput`、`ToResult`）取 RLock，写操作（`SetCommon`、`ApplyOutput`）取 Lock。RowFrame 和 ColumnFrame 使用相同的锁策略。

### Skip 处理

控制流被编译为普通算子加 `skip` 字段列表引用。JSON 契约中的 `skip` 现在是”零个或多个 common 控制字段名”的列表；Go 配置加载器在 `pine-go/internal/config/load.go` 中通过 `normalizeSkip()` 兼容旧版单字符串 JSON，并在加载后统一归一为列表语义。运行时调度器直接通过 Frame 方法读取这些字段（Frame 内部加锁保证安全）：

- `skip` 列表中任一字段只要是 Lua truthy 值，该算子就跳过执行
- Go 运行时的判定与 Lua 一致：只有 `nil` 和 `false` 视为 falsy；任何非 `nil`、非 `false` 的值都视为 truthy
- 因此手写 JSON 或其他非 Apple 来源若把控制字段写成 `1`、非空字符串等非 bool truthy 值，运行时也会触发跳过，而不会像旧版严格 `== true` 那样静默放行
- 空列表或缺失 `skip` 表示没有控制流守卫

这使嵌套控制流可以自然表达为“外层分支守卫 + 内层分支守卫”的合取约束：只要任一外层或内层 guard 判定为跳过，业务算子就不会运行。

被跳过的算子仍参与图和 trace 流，但不运行业务逻辑。

编译器将控制字段注入 `$metadata.CommonInput` 以建立 DAG 依赖。嵌套控制和分支内 SubFlow 会产生多个 skip 字段；Go 加载器兼容旧版单字符串 `skip`，新 JSON 使用字符串列表。引擎在两处过滤 skip 字段，使控制字段对算子透明：
- `pine-go/pine.go` 在调用 `SetMetadata` 前剔除 skip 字段，算子的 `MetadataHolder.CommonInput` 不含控制字段
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
- 输出写回失败（`ApplyOutput` 返回错误）也计为算子失败，并记录 error stats/metrics，而不是成功执行
- `Run` 返回前过滤掉未执行节点的零值 trace 条目（`Name == ""`），因此返回的 trace 仅包含实际执行或跳过的算子

Warning 与致命错误分开。算子可通过 `OperatorOutput.SetWarning` 发出 warning，执行继续。`SetWarning` 采用 first-wins 语义：若同一次执行中多次调用，仅第一次设置的 warning 被保留，后续调用为 no-op。

### PanicError 信息分离

`pine-go/internal/types/errors.go` (`PanicError`) 现在区分面向外部的错误消息与面向内部的诊断信息：

- `PanicError.Error()` — 仅返回 panic value 描述，不包含 stack trace；该方法的输出安全暴露给 HTTP 客户端
- `PanicError.DetailedError()` — 返回包含完整 stack trace 的详细信息，仅供服务端日志记录

此设计防止通过 HTTP 错误响应泄露内部代码路径信息（信息泄漏），同时保留运维排障所需的全部诊断数据。

### Debug trace

当算子配置 `debug=true` 时，调度器捕获：

- 输入快照
- 输出快照
- 计时数据
- 跳过状态

这些填充 `pine-go/internal/types/trace.go` 记录并在最终结果中返回。

`debug=true` 的来源可以是两类：

- 算子自身配置携带的逐算子 `debug`
- `NewEngine` 根据根级 `debug` 或 `pine.WithDebug(...)` 覆盖后统一写入的 `opCfg.Debug`

因此全局 debug 不引入新的 trace 结构；它只是把既有逐算子 debug 语义批量施加到整条 flow。

## 统计、指标与服务端观测

Pineapple 现在把运行时观测拆成两层：

### 1. 总是开启的原子统计

`pine-go/internal/runtime/stats.go` 维护进程内累计统计：

- 每算子：`exec_count`、`skip_count`、`error_count`、累计/最大/平均耗时
- 调度器级：`run_count`、`peak_concurrency`

这些统计由 `Engine.Stats()` 与 `Engine.SchedulerStats()` 暴露，不依赖任何第三方监控库。

### 2. 可插拔 Provider metrics

`pine-go/pkg/metrics/` 定义最小观测接口：

- `Provider`
- `Counter`
- `Gauge`
- `Histogram`
- `MetricOpts` / `HistogramOpts`

核心原则：

- Pineapple core 不直接依赖 `prometheus/client_golang`
- 调用方自行实现适配器，然后通过 `pine.WithMetrics(provider)` 或 `server.Config.Metrics` 注入
- 默认 `metrics.Nop()` 为零成本 no-op 实现

当前稳定的引擎级 metric 名称包括：

**算子级：**

- `pine_scheduler_runs_total`
- `pine_operator_active`
- `pine_operator_exec_total{operator=...}`
- `pine_operator_exec_duration_seconds{operator=...}`
- `pine_operator_skip_total{operator=...}`
- `pine_operator_error_total{operator=...}`

**DAG 级（0.6.6 起）：**

- `pine_dag_executions_total{status=success|error}`
- `pine_dag_execution_duration_seconds`
- `pine_dag_operators_executed`

DAG 级指标在 `scheduler.Run()` 结束时统一记录，覆盖从调度开始到所有算子完成的完整区间。

### 3. 服务端 reload 观测

`pine-go/pkg/server/server.go` 还维护服务端级 reload 观测：

- 原子统计：`reloadCount`、`reloadErrorCount`、`lastReloadDurationNs`
- Provider metrics：
  - `pine_config_reload_total`
  - `pine_config_reload_errors_total`
  - `pine_config_reload_duration_seconds`

reload 时会把同一个 provider 继续传给新的 `pine.NewEngine(...)`，因此热加载前后外部指标后端保持一致。

### 4. `/stats` 组合响应

HTTP `GET /stats` 返回组合观测视图：

- `operators` — `Engine.Stats()` 的每算子统计
- `scheduler` — `Engine.SchedulerStats()` 的调度器级统计
- `server` — 服务器 reload 统计
- `operator_detail` — 仅当存在实现 `StatsProvider` 的算子时出现，承载算子自定义统计

因此 `/stats` 面向零配置诊断，而 Prometheus 等系统应通过调用方自建的 provider 适配器暴露。

## DataFrame 不变量

`pine-go/internal/dataframe/` 是请求本地的可变存储，通过 `Frame` 接口抽象。

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
- item 数据（行存为 `[]map[string]any`，列存为 `map[string][]any` + presence bitmap + `rowCount`）

`NewFrame` 浅拷贝请求输入，使后续变异不会 alias 调用方持有的 map。

### 输入投影

`BuildInput` 将 frame 投影到算子声明的元数据契约：

- 仅暴露声明的字段
- 对 nil 值应用 `common_defaults` 和 `item_defaults`
- 保留字段存在性语义：缺失字段且无默认值时不写入 `OperatorInput`；缺失字段且有默认值时写入默认值；字段显式存在但值为 nil 时仍视为“已存在”，并在有默认值时应用默认值

这意味着算子行为取决于其元数据契约，而非对完整 frame 的无限制访问。`BuildInput` 与 `ToResult` 都必须区分“字段缺失”和“字段显式存在但值为 nil”，不能把两者折叠为同一种 `nil` 语义。

### Apply 顺序不变量

`ApplyOutput` 始终按以下顺序应用算子输出：

1. common 写入
2. item 字段写入
3. item 移除
4. item 重排序
5. item 添加

此顺序是承载性的。它确保结构性 item 变异发生在普通字段写入之后、recall 添加追加之前。

结构性索引必须先校验后变异。`RemoveItem` 和 `SetItemOrder` 的越界索引应使 `ApplyOutput` 返回错误，并保持 frame 不被部分结构性修改。

后果：

- transform 可以安全地在后续 filter 移除行之前写入字段
- 重排序始终应用于当前存活的行，而非即将添加的行
- recall 添加始终在当前行集被该算子的 filter/reorder 处理后到达

任何对此顺序的更改都会改变运行时语义，必须视为架构变更。

### 结果投影

`ToResult` 通过 flow contract 声明的输出字段投影最终 frame。底层 `projectMap` 只投影显式声明的字段：空输出列表表示空输出（该维度不返回任何字段），不会回退为"返回当前存在的全部字段"。

RowFrame 与 ColumnFrame 必须保持稀疏字段语义一致：某一 item 未包含的字段在结果中应省略，而不是因为同列其他 item 存在该字段就输出 `null`。ColumnFrame 通过 presence bitmap 区分“字段缺失”和“字段存在但值为 nil”，该 bitmap 同时服务于 `BuildInput` 与 `ToResult`，确保输入构造与结果投影共享同一套 sparse 语义。

## 算子类型约束

`pine-go/internal/types/operator.go` 定义六种算子类型，并校验它们可使用哪些 `OperatorOutput` 方法。

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

运行时依赖 `pine-go/internal/config/types.go` 中的若干配置级字段：

- `log_prefix` — 根级全局日志前缀，供 `NewEngine` 调用 `log.SetPrefix()`，并配套设置 `log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)` 以启用 `file:line` 输出
- `debug` — 根级全局 debug 默认值；`NewEngine` 可直接消费，也可被 `pine.WithDebug(...)` 覆盖，最终批量下沉到每个算子的 `opCfg.Debug`
- `$metadata` — 声明的 common/item 输入和输出
- `skip` — 控制流守卫字段列表；任一字段只要是 Lua truthy 值即跳过，旧版单字符串 JSON 在加载期会被归一化为单元素列表
- `recall` — 从 DSL/codegen 约定保留的声明提示
- `sources` — 显式上游 source 引用；名称必须存在，且在扁平声明顺序上只能指向当前算子之前已经出现的命名上游
- `debug` — 逐算子 trace 捕获开关
- `row_dependency` — 启用 `_row_set_` 读取
- `common_defaults` / `item_defaults` — 快照构建时的输入默认值
- `for_branch_control` — 标记编译器生成的控制算子

虽然这些源自 DSL 或手写 JSON，但其语义在 Go 运行时中强制执行。

## 资源与服务器集成

资源和 HTTP 服务位于引擎旁而非 DAG 核心内部。

- `pine-go/pkg/resource/` 管理命名资源，支持后台刷新和原子读取。
- `pine-go/pkg/server/server.go` 加载引擎、启动资源、注入请求上下文、服务 `/health`、`/execute`、`/stats` 和 `/dag`，并在保留 config hot-reload 与 graceful shutdown 的前提下允许业务侧通过 `server.Config.Middlewares` 包装整个 HTTP handler 链。
- `pine-go/pkg/server.Config.Metrics` 把同一个 `pine-go/pkg/metrics.Provider` 同时传给 server reload 观测、内置 HTTP 请求指标中间件和 `pine.NewEngine(..., pine.WithMetrics(provider))`，形成统一的外部指标出口。
- `pine-go/pkg/server/server.go` 内置安全加固：
  - 可配置超时：`ReadHeaderTimeout`（默认 5s）、`ReadTimeout`（默认 10s）、`WriteTimeout`（默认 30s）、`IdleTimeout`（默认 120s），防止 slowloris 类攻击和资源耗尽
  - 可配置 `MaxRequestBodySize`（默认 10MB），通过 `http.MaxBytesReader` 在 handler 层限制请求体大小
  - 上述参数均通过 `server.Config` 字段暴露，`pine-go/cmd/pineapple-server/main.go` 提供对应命令行 flags
- `pine-go/pkg/server/server.go` HTTP 错误响应契约：
  - 所有非 200 错误响应统一使用 JSON 格式 `{"error": "..."}` (`errorResponse` 结构体)，不再使用 `http.Error` 纯文本
  - `pine.ValidationError`（如缺少必需的 common 输入字段）映射为 HTTP 400，而非 500
  - 请求体超过 `MaxRequestBodySize` 时返回 HTTP 413（Request Entity Too Large），而非 400
  - 其他引擎执行错误仍为 HTTP 500

### Server 结构体生命周期

Server 现在是 struct-based 设计：`Server` 结构体封装所有可变状态（snapshot 指针、metrics handle 等），handler 函数作为 `*Server` 的方法接收者运行。公共 API `Run(cfg Config) error` 保持不变，内部委托给 `s.run(cfg)`。

`watchConfig` 接受 `context.Context`，通过 `ticker + select` 实现干净的关闭传播：

- `ctx.Done()` 分支停止 ticker 并退出 goroutine
- 不再依赖永不退出的 goroutine 存活模式

这消除了旧版包级全局状态的竞态风险，并确保 graceful shutdown 时 watchConfig goroutine 可被确定性回收。

### 配置热加载

配置热加载同时覆盖 Engine 和 ResourceManager。`watchConfig` 检测文件变更后调用 `reloadConfig`，创建新 Manager → Start → 原子替换 snapshot → Stop 旧 Manager。失败时保持旧配置不变。

### 已知运行时操作风险

以下风险已在代码审计中确认；其中部分已修复，仍保留的条目用于提示当前仍需运维关注的不变量。

- **server hot-reload 快照不一致 — 已修复**：`pine-go/pkg/server/server.go` 现已把 `*pine.Engine` 与 `*resource.Manager` 绑定进单一 `serverSnapshot`，并通过一个 `atomic.Pointer[serverSnapshot]` 统一管理。reload 时只做一次 snapshot swap，请求处理时也只 load 一次，因此不会再观察到”新 engine + 旧 resources”或”旧 engine + 新 resources”的混合版本快照。
- **Lua state pool 生命周期缺口 — 已修复**：`pine-go/operators/lua/pool.go` 的 `statePool` 现在提供 `Close()`，并跟踪池创建过的全部 state。引擎在 hot-reload 替换旧实例时，旧 pool 中持有的 Lua states/cgo 资源可以被完整释放，不再依赖进程退出回收。
- **Lua baseline global 恢复不完整 — 已修复**：Lua pool 现在在每个 state 借出时记录该 state 的基线快照，并在 Return 时执行完整 baseline restoration，而不只是删除新增 global key。这样即使请求代码覆盖了已有全局变量，也会在归还时恢复，避免跨请求污染。
- **`data_parallel` 能力门由引擎加载期强制执行 — 已修复**：`pine-go/pine.go` 的 `validateDataParallel` 现在在 `data_parallel > 1` 时要求算子实例实现 `ConcurrentSafe`。Apple 编译器不再维护并发安全 blocklist，只保留结构性校验（Transform + 无 `common_output`），从而把能力判定收敛到 Go 的单一事实源。`transform_normalize` 因依赖全集语义而保持未实现 `ConcurrentSafe`。

HTTP 路由先由 `http.ServeMux` 注册内部端点，再由内置 `httpMetricsMiddleware`（`pine-go/pkg/server/http_metrics.go`）作为最内层中间件包装，最后由 `server.Config.Middlewares []func(http.Handler) http.Handler` 在启动 `ListenAndServe` 前按切片顺序从外到内包装整个 handler 链。也就是说，`Middlewares[0]` 最先看到请求、最后看到响应；`nil` 或空切片时行为与旧版一致。内置 HTTP 指标中间件记录 `pine_http_requests_total` 和 `pine_http_request_duration_seconds`，且将未知路径归一化为 `_other` 以防止高基数标签。该注入点位于 server 边界层，不参与 Engine 编译、DAG 推导或配置热加载逻辑，因此业务侧可叠加访问日志、认证、限流等横切能力，而不必自行重写 Pineapple 的 reload / shutdown 框架。

此分离很重要：DAG 执行仅依赖请求上下文和编译后的 plan，不依赖服务器特定逻辑。

## DAG 可视化

`pine-go/internal/dag/visualize.go` 现在提供四种渲染函数：

- `RenderDOT(g *Graph) string` — 完整算子级 Graphviz DOT
- `RenderMermaid(g *Graph) string` — 完整算子级 Mermaid flowchart
- `RenderCollapsedDOT(g *Graph, level int) string` — 按 SubFlow 层级聚合后的 DOT
- `RenderCollapsedMermaid(g *Graph, level int) string` — 按 SubFlow 层级聚合后的 Mermaid

完整视图仍按算子类型着色（Recall 绿、Transform 蓝、Filter 橙、Merge 紫、Reorder 黄、Observe 灰），标签包含算子名。布局方向为自上而下（DOT `rankdir=TB`、Mermaid `graph TB`）。

### SubFlow 折叠视图

collapsed 渲染按 `Node.SubFlow` 的层级路径做前缀分组，而不是简单地“同名 SubFlow 全并到一组”。

- `pine.WithCollapse(level)` 中 `level=0` 表示不折叠
- `level=1` 表示按路径前 1 段分组，如 `recall/candidates` 与 `recall/rerank` 都折叠到 `recall`
- `level=2` 表示按前 2 段分组
- 若某节点的路径段数小于等于 `level`，则保留其完整路径作为组名
- `SubFlow == ""` 的顶层算子始终保持独立，不会被并入伪组
- 同一组内部的边不会出现在折叠视图中
- 只保留跨组边，并对重复的跨组边做去重

因此 collapsed 视图表达的是“按层级前缀聚合后的依赖骨架”，支持从全展开到逐层折叠的连续视角，而不是单一的二值 SubFlow 视图。

### API 与 HTTP 暴露面

公共 API 通过 `Engine.RenderDAG(format string, opts ...RenderOption) (string, error)` 暴露：

- format 支持 `"dot"` 和 `"mermaid"`
- `pine.WithCollapse(level)` 选择折叠层级；`0` 表示完整算子级视图，`1/2/...` 表示按路径前 N 段折叠
- 不传 `RenderOption` 时保持完整算子级视图

HTTP 端点为 `GET /dag`：

- `format=dot|mermaid` 选择输出格式，默认 `dot`
- `collapse=N` 启用层级折叠，其中 `N` 必须是非负整数
- `collapse=0` 或未指定 `collapse` 时返回完整算子级 DAG

由于 `Build()` 阶段已对执行图执行传递性归约，完整视图和 collapsed 视图都基于同一份最小边集。collapsed 渲染只是在这个最小边集上做分组、过滤组内边并去重跨组边，不重新推导执行依赖。

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
10. **观测走双通道。** `/stats` 依赖内建原子统计，外部指标依赖可插拔 provider；二者记录同一运行事实但面向不同消费场景。
11. **注入顺序固定。** 对同时实现多个可选接口的算子，引擎按 MetadataAware → DebugAware → MetricsAware → ResourceAware 顺序注入。依赖 operator name 做 metric label 的实现可依赖该顺序。
12. **全局 debug 通过覆写逐算子配置生效。** 根级 `debug` 与 `pine.WithDebug(...)` 不引入独立执行分支，而是在 `NewEngine` 中统一改写每个算子的 `opCfg.Debug`；Go option 还必须保留“未设置”与“显式 false”两种状态区分。
13. **SubFlow 只影响可视化分组，不影响执行语义。** `Node.SubFlow` 与 collapsed render 不能改变 DAG 的边、调度顺序或 hazard 推导；它们只是对既有执行图的聚合视图。

## Pine-Java 功能对等

`pine-java/` 提供完全对等的 JVM 运行时，共享同一 JSON 配置格式。

### 对等覆盖范围

Pine-Java 实现了与 Pine-Go 相同的核心架构：

- **引擎编译流水线**：JSON 解析 → 算子序列展开（含 SubFlow）→ sources 前向引用校验（`Engine.validateSourcesOrder`）→ 算子实例构建 → DAG 推导
- **Option pattern**：`Engine.create(jsonConfig, options...)` 接受 `withMetrics`、`withResources`、`withLogPrefix`、`withDebug` 四种 option，语义与 Go 的 `pine.NewEngine(jsonConfig, opts...)` 一致
- **DAG 推导**：屏障边 + 数据冒险边 + 显式 sources 边 + 传递性归约 + 拓扑排序
- **并发调度**：ForkJoinPool + CompletableFuture 事件驱动唤醒，语义等同 Go 侧的 per-operator goroutine + done channel 模型（无轮询）
- **DataFrame**：`Frame` 接口抽象，`DataFrame`（行存）和 `ColumnFrame`（列存），通过 `storage_mode` 配置选择
- **data_parallel**：`ParallelExecutor` 实现 Transform 级并行分片，要求 `ConcurrentSafe` 接口
- **双通道观测**：`Stats` 原子统计 + 可插拔 `Provider`（`metrics/Provider.java`），默认 `NopProvider`（等同 Go 的 `metrics.Nop()`）
- **注入顺序**：引擎编译算子时按 MetadataAware → DebugAware → MetricsAware → ResourceAware 固定顺序注入，与 Go 侧一致（ResourceAware 缺失时抛出 IllegalStateException）
- **结构化错误**：6 种错误类型 — `ConfigError`、`RegistryError`、`ValidationError`、`OperatorException`（checked）、`ExecutionError`、`PanicError`。`Operator.execute()` 声明 `throws OperatorException`；引擎据此区分预期算子错误（→ ExecutionError）与意外运行时异常（→ PanicError），对应 Go 的 error/panic 语义。所有错误类型的 `getMessage()` 统一 `pine:` 前缀

### Server 对等

`PineServer` 提供：

- `/health`、`/execute`、`/stats`、`/dag` 四个端点
- 基于 `ScheduledExecutorService` 的配置热加载（2s 间隔检测文件变更）
- 原子快照替换（`AtomicReference<Snapshot>` 封装 engine + resources）
- Middleware 链（`Middleware` functional interface，外到内包装）
- HTTP metrics middleware（`pine_http_requests_total`、`pine_http_request_duration_seconds`）
- Reload metrics（`pine_config_reload_total`、`pine_config_reload_errors_total`、`pine_config_reload_duration_seconds`）
- `_return_trace` 请求参数支持
- `max_request_body_size` 从 JSON 配置读取（等同 Go 的 `server.Config.MaxRequestBodySize`）
- 流式 `readLimitedBody` 防 OOM（分块读取，超限拒绝）
- `validateResourceDeps` 在初始加载和热加载时均调用（与 Go 行为一致）
- `/stats` 在无 engine snapshot 时返回 503
- `/dag` 校验 `collapse` 参数（负值 → 400）并捕获 renderDAG 异常
- `Engine.execute` 返回 `Result` 带 error 字段（partial result 模式，对应 Go 的 `(result, err)` 返回）

### 算子执行上下文

- **CancellationToken**（volatile boolean）：Java 对 Go `context.Context` 取消传播的等价实现
- 全部 18 个算子的 `execute()` 方法接受 `CancellationToken token` 作为第一参数
- 引擎为每次请求创建一个 request-level token，首个 fatal error 时取消。`ParallelExecutor` 额外创建 shard-level child token：各 shard 接收 child token 而非 parent，首个 shard 失败时仅取消 child token（等同 Go 的 per-shard `context.WithCancel`），shard 启动前同时检查 parent 和 child 两级取消状态
- 长时间循环（Lua item 迭代、parallel shard）检查 `token.isCancelled()`
- 不提供指令级 VM 中断（LuaJ 平台限制）——仅循环级协作式取消
- **DebugAware 接口**：引擎注入 operatorName + debug flag；`TransformByLua` 利用它进行 debug 日志输出
- **MetricsAware 接口**：引擎注入 `metrics.Provider`；算子可注册自定义指标

### Lua VM 池化与沙箱

`TransformByLua` 使用 LuaJ（`org.luaj.vm2`）实现：

- `ConcurrentLinkedQueue` 池化 Globals 实例
- 构造时记录 `baselineKeys`（脚本加载后全局表的 key 集合）
- Borrow 时对 state 的全部 baseline key 做值快照
- Return 时执行完整 baseline restoration：删除所有非 baseline key，恢复被修改的 baseline key 值
- 池有 `Close()` 方法与 volatile `closed` 标志，用于热加载生命周期管理
- Borrow 时检查 `closed` 状态，已关闭则抛出异常
- 沙箱：仅加载 `base`、`table`、`string`、`math`、`package`（LuaJ 编译器依赖），但 `require` 和 `package` 全局变量置为 NIL；移除 `io`、`os`、`debug`、`dofile`、`loadfile`

### 算子全覆盖

Pine-Java 注册全部 18 个内置算子（`AllOperators.java`），与 Pine-Go `pine-go/operators/all.go` 完全对齐：

- Filter: `filter_condition`、`filter_truncate`、`filter_paginate`
- Transform: `transform_copy`、`transform_dispatch`、`transform_normalize`、`transform_size`、`transform_by_lua`、`transform_resource_lookup`、`transform_redis_get`、`transform_redis_set`、`transform_by_remote_pineapple`
- Recall: `recall_static`、`recall_resource`
- Merge: `merge_dedup`
- Reorder: `reorder_sort`、`reorder_shuffle_by_salt`
- Observe: `observe_log`

### 跨运行时格式兼容（GoFormat）

`GoFormat.java` 提供静态方法复制 Go 标准库数值格式化行为：

- `sprint(Object)` — 等效 Go `fmt.Sprint`；nil → `"<nil>"`，magnitude < 1e6 的整数值 float → 无小数点（阈值 1e6 匹配 Go 切换科学计数法的边界）
- `formatFloatF(double)` — 等效 Go `strconv.FormatFloat(d, 'f', -1, 64)`
- `formatG(double)` — 等效 Go `fmt.Sprintf("%g", d)`；保留完整精度
- magnitude ∈ [1e6, 1e7) 时 `formatG` 将科学计数法表示转换为定点表示，匹配 Go `%g` 在该区间的输出
- `sprint` 支持 `List<?>` 和数组类型，输出 `"[a b c]"` 空格分隔格式（匹配 Go `fmt.Sprint` 对 slice 的行为）
- `formatG` 将 `Infinity` / `-Infinity` 输出为 `"+Inf"` / `"-Inf"`（Go 惯例）
- `formatG` 对 magnitude ∈ [1e-4, 1e-3) 的小数通过 `BigDecimal.toPlainString()` 转换为定点表示
- `sprint`、`formatFloatF`、`formatG` 均保留 `-0.0` 的符号位（输出 `"-0"` 而非 `"0"`），通过 `Double.doubleToRawLongBits` 在各自的整数快捷路径前检测

消费者：`TransformResourceLookup`（key coerce）、`TransformRedisGet`（key 拼接）、`FilterCondition`（条件比较值格式化，替代旧的 `formatValue` 方法）、`ReorderShuffle`（salt 格式化，替代旧的 `formatFloatG` 方法）。第六轮 parity 审计中移除了 `FilterCondition.formatValue` 和 `ReorderShuffle.formatFloatG`，统一使用 `GoFormat` 作为跨算子格式化单一事实源。

### 资源管理

`ResourceManager` 实现：

- `FetcherFactory` 全局注册表 + `Fetcher` 接口
- 后台刷新（`ScheduledExecutorService`）
- `validateDeps()` 校验算子声明的 `resource_name` 是否已注册

### Codegen

`Codegen.java` 支持双模式操作：

- `--export-schema <path>` — 从内部 Registry 导出 Schema JSON
- `--schema-from-registry` — 从内部 Registry 直接生成 Python DSL 产物
- `-schema <path>` — legacy 模式（读取外部 JSON，保留兼容性）
- `-ops-dir <path>` — 指定算子 Java 源码目录，启用 Javadoc metadata contract 解析（等同 Go `pine-go/pkg/codegen/docparse.go`）

`ResourceRegistry.java` 提供 codegen-time 资源注册表（等同 Go `resource.All()`），使 `--export-schema` 可同时导出资源 Schema，无需运行时实例化 ResourceManager。

### Schema 独立性

Pine-Java Registry 实现完整的 schema-based 注册（`ParamSpec.java`、`OperatorSchema.java`），`validateAndExtractParams()` 执行与 Go 等效的严格校验。`Registry.exportSchemaJSON()` 导出与 Go 格式一致的 JSON。两侧通过 CI 三层交叉验证（Schema diff、Config fixtures、Execution 比对）保持对齐，无运行时耦合。

## 检索指针

- 引擎编译和请求生命周期：`pine-go/pine.go`
- 配置解析和序列展开：`pine-go/internal/config/load.go`、`pine-go/internal/config/types.go`
- DAG 推导：`pine-go/internal/dag/dag.go`
- DAG 可视化：`pine-go/internal/dag/visualize.go`
- 引擎 DAG API 与 render options：`pine-go/pine.go`
- 调度器和 trace 捕获：`pine-go/internal/runtime/scheduler.go`
- Data-parallel split/merge/execute：`pine-go/internal/runtime/parallel.go`
- 统计：`pine-go/internal/runtime/stats.go`
- 引擎级指标句柄：`pine-go/internal/runtime/engine_metrics.go`
- 可插拔指标接口：`pine-go/pkg/metrics/metrics.go`、`pine-go/pkg/metrics/nop.go`
- HTTP 服务与 `/stats`：`pine-go/pkg/server/server.go`
- 内置 HTTP 请求指标中间件：`pine-go/pkg/server/http_metrics.go`
- Frame 接口和工厂：`pine-go/internal/dataframe/frame.go`
- 行存实现：`pine-go/internal/dataframe/row_frame.go`
- 列存实现：`pine-go/internal/dataframe/column_frame.go`
- 算子分类和输出校验：`pine-go/internal/types/operator.go`
- 共享 request/result/trace/error 类型：`pine-go/internal/types/`

### Pine-Java 对等检索指针

- 引擎编译和请求生命周期：`pine-java/src/.../Engine.java`
- 配置解析：`pine-java/src/.../Config.java`
- DAG 推导：`pine-java/src/.../DAG.java`
- DAG 可视化：`pine-java/src/.../DAGVisualizer.java`
- Frame 接口和工厂：`pine-java/src/.../Frame.java`
- 行存实现：`pine-java/src/.../DataFrame.java`
- 列存实现：`pine-java/src/.../ColumnFrame.java`
- HTTP 服务：`pine-java/src/.../PineServer.java`
- 可插拔指标接口：`pine-java/src/.../metrics/Provider.java`
- 结构化错误：`pine-java/src/.../PineErrors.java`
- 算子注册表：`pine-java/src/.../Registry.java`
- CancellationToken：`pine-java/src/.../CancellationToken.java`
- DebugAware：`pine-java/src/.../DebugAware.java`
- MetricsAware：`pine-java/src/.../MetricsAware.java`
- Codegen：`pine-java/src/.../Codegen.java`
