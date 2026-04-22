# 算子级数据并行框架复盘

## Task
- 为 Transform 算子新增统一的 `data_parallel` 配置：当 `data_parallel=N` 且 `N>1` 时，调度器自动将 items 切成 N 份并发执行，再合并输出，对算子实现保持透明。
- 在编译期限制该能力仅适用于 Transform；拒绝 Recall、Observe、Filter、Merge、Reorder。
- 在编译期禁止 `data_parallel` 与非空 `$metadata.common_output` 组合，以避免 shard 间 common 写入缺少安全合并语义。
- 将 `data_parallel` 作为引擎保留键处理，不传入算子 `Init()`。
- 保持 DAG 推导逻辑完全不变，仅在运行时调度路径中分流到并行执行。

## Expected vs Actual
- Expected outcome.
  - 在不改变算子接口与 DAG 推导规则的前提下，为 Transform 提供透明的算子级数据并行能力。
  - 相关限制在编译期尽早失败，避免把不支持的类型或不可合并的 common 写入拖到运行时。
  - 运行时实现拆分清晰：切分、并发执行、panic/错误回收、输出合并与调度集成职责明确。
- Actual outcome.
  - 功能按预期落地：`data_parallel` 只在 `DataParallel > 1` 时进入并行路径，普通算子路径保持不变，DAG 推导没有被触碰。
  - 编译期校验已覆盖类型限制和 `common_output` 约束；`data_parallel` 也已加入保留键集合，不会泄露到业务参数。
  - 实现分层清晰：`pine.go` 负责校验，`internal/runtime/parallel.go` 负责 split/merge/parallel/recovery，`scheduler.go` 负责接入。
  - 23 个新增测试一次通过，并通过 race detector，说明语义边界和并发安全性基本稳定。

## What Went Wrong
- 任务在同一轮里先经历了较多方案收敛讨论，再进入实现，导致上下文预算被设计讨论显著消耗，最终需要续会话完成代码与文档收尾。
- 为了让运行时并行路径复用现有 `OperatorInput` 结构，新增了 `RawCommon()` 与 `RawItems()` 访问器，把原本更封闭的输入抽象稍微向引擎内部实现细节打开了一层口子。
- 完成代码后，已明确识别出 llmdoc 中 `architecture/dag-engine.md` 与 `reference/operator-contract.md` 需要同步，但这类“运行时新路径 + 契约新保留键”的文档更新点并不是在设计阶段就被前置列成检查项，而是在实现完成后再补充识别。

## Root Cause
- 这次任务既涉及运行时并发模型，又涉及算子契约和编译期限制，前期设计收敛本身是必要的；但没有把“先确认收敛边界，再切到实现最小上下文包”作为显式执行策略，导致会话上下文被较长的讨论链条占用。
- Pineapple 当前的包边界使得运行时想要零拷贝/低摩擦地访问投影后的输入切片时，很难只靠包内 helper 达成；因此最终选择在 `internal/types` 暴露受控访问器，这是 Go 包可见性约束下的务实解，而不是理想的最小 API 面。
- 现有 llmdoc 对调度器章节和算子契约章节尚未覆盖“算子级数据并行”这一新能力，因此实现者需要在任务末尾自行比对哪些稳定文档受影响，缺少更早的提示信号。

## Missing Docs or Signals
- `llmdoc/architecture/dag-engine.md` 应补充调度器存在 `data_parallel` 分支执行路径：DAG 依赖不变，但单个 Transform 节点在运行时可进一步按 item shard 并发执行并合并结果。
- `llmdoc/reference/operator-contract.md` 应补充 `data_parallel` 是保留键，不会传入 `Init()`；同时在算子类型表或补充说明中明确只有 Transform 支持该能力，且启用时不能声明 `common_output`。
- 对未来类似特性，stable docs 里可以更明确地区分“图级并发”和“节点内部数据并发”两层并发模型，避免后来者默认所有并发都必须通过 DAG 边表达。
- 这次暴露出的 `RawCommon()` / `RawItems()` 仅服务引擎内部并行路径，属于 memory 级信号：若后续再扩展运行时内部能力，应优先评估是否继续扩大 `internal/types` API，还是需要重构 runtime/types 边界。

## Promotion Candidates
- 适合提升到 `architecture/dag-engine.md`：DAG 推导与算子级数据并行是正交的；`data_parallel` 不改变图结构，只改变单个 Transform 节点的执行方式。
- 适合提升到 `reference/operator-contract.md`：`data_parallel` 是引擎保留键、仅 Transform 可用、启用时 `common_output` 必须为空，且该配置对算子实现透明。
- 仅保留在 memory：会话上下文在“设计收敛 + 实现”混合任务中容易被前期讨论吃掉，类似任务应在方案定稿后主动压缩上下文并重新组织执行。这是流程经验，不是稳定架构事实。
- 仅保留在 memory：`OperatorInput` 为支撑运行时能力而新增原始访问器，会扩大 types 包表面积；后续若多次出现类似需求，才值得上升为关于 runtime/types 边界的稳定设计文档。

## Follow-up
- 更新 `llmdoc/architecture/dag-engine.md`，在 scheduler 部分加入 `data_parallel` 的运行时分流、split/merge 语义以及“图结构不变”的说明。
- 更新 `llmdoc/reference/operator-contract.md`，把 `data_parallel` 加入保留键列表，并在类型约束处补充其仅适用于 Transform 且要求空 `common_output`。
- 若后续继续增强节点内部并行能力，先复查 `OperatorInput` 新访问器是否仍是最小必要暴露面，再决定是否需要重构 runtime 与 types 的边界。
