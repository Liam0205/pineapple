# [ConcurrentSafe opt-in for data_parallel safety]

## Task
- 将 `data_parallel` 的并发安全判定从“默认允许 + 双端 blocklist”改为“显式正向 opt-in”。
- 在 Go 运行时引入 `ConcurrentSafe` 可选接口与 `ConcurrentSafeMarker`，仅允许实现该接口的 Transform 在 `data_parallel > 1` 时被并发分片执行。
- 让 Apple 侧只保留结构性校验（必须是 Transform、不能声明 `common_output`），把能力判定收敛到 Go 这一侧的单一事实源。
- 审计内置 Transform，给 8 个逐 item 独立实现打标，保留 `transform_normalize` 为未标记状态，并补充 race 测试覆盖并发执行路径。

## Expected vs Actual
- Expected outcome.
  - 新增算子若未显式声明并发安全，就不能被 `data_parallel` 自动放行。
  - Python/Go 不再各自维护一份“不安全名单”，能力检查应在真正持有算子实例的 Go 层完成。
  - 新接口应与现有可选接口模式保持一致，便于算子作者理解和采用。
- Actual outcome.
  - `data_parallel` 现在改为正向 opt-in：只有实现 `ConcurrentSafe` 的算子实例才可在 `data_parallel > 1` 时通过校验。
  - Apple 侧移除了并发安全名单语义，只做结构约束；Go 侧通过实例接口检查成为唯一能力判定点，消除了双端漂移问题。
  - 8 个内置 Transform 被审计并标记为 `ConcurrentSafe`，`transform_normalize` 保持未标记，因为它依赖整个 item 集合语义。
  - `parallel_test.go` 新增并发/race 测试，为“约定上的只读执行”提供运行时验证补位。

## What Went Wrong
- 首版 `data_parallel` 采用“默认安全，列出少数已知不安全算子”的思路，本质上是 default-allow；一旦新增算子忘记进 blocklist，就会被静默放行到并发路径。
- 并发安全规则同时存在于 Go 的 `isDataParallelSafeTransform` 和 Python 的 `_DATA_PARALLEL_UNSAFE_TRANSFORMS`，维护成本高，而且任何一边漏改都会产生跨语言漂移。
- 设计上曾希望通过类型系统表达“Execute 只读、不改 receiver 状态”，但 Go 接口无法约束实现必须使用值接收者，也无法静态证明内部没有写共享状态，最终不能靠类型系统封死这类错误。
- 如果只看接口定义，容易低估“同一实例在单请求内被多 goroutine 并发调用”这一契约强度；真正可靠的补位仍需要 race detector 之类的运行时验证。

## Root Cause
- 根因首先是安全模型选型失误：把并发执行资格设计成默认允许、少量例外禁止，会天然对“未来新增实现”失守。对于这类 capability gate，正向声明比负向封禁更稳妥。
- 更深层根因是能力检查放错了层。Python 编译器看见的是配置和类型名，看不见最终构建出的 Go 算子实例；并发安全却恰恰是实例能力问题，因此双端维护名单从一开始就不是最合适的架构。
- 另一个根因是对 Go 类型系统能力边界的预期过高。接口适合表达“实现了什么能力”，不适合表达“实现内部绝不写状态”这种语义属性，所以最终必须接受“接口声明 + 约定 + race 测试”这一组合。
- 好的一面是，Pineapple 已有 `MetadataAware`、`DebugAware`、`StatsProvider`、`MetricsAware` 这套可选接口模式；`ConcurrentSafe` 能自然沿用同一模式，说明现有扩展机制本身是对的，只是最初把并发资格做成了名单而不是能力接口。

## Missing Docs or Signals
- 稳定文档应明确把 `data_parallel` 的安全模型写成“正向能力声明”，而不是只描述它的结构限制或运行时分片行为。
- `llmdoc/reference/operator-contract.md` 应补充：当算子想支持 `data_parallel > 1` 时，需要显式实现 `ConcurrentSafe`，否则即便是 Transform 也不能进入并发路径。
- `llmdoc/architecture/dag-engine.md` 应补充：并发能力检查位于 Go 运行时实例层，而不是 Python DSL 层；Apple 只负责结构性约束，不负责实例能力判断。
- 更适合留在 memory 的信号是：Go 无法在接口层约束 receiver 只读语义，因此凡是“共享实例并发重入”类能力，都应默认追加 race 测试，而不是依赖接口本身带来的安全幻觉。

## Promotion Candidates
- 适合提升到 `reference/operator-contract.md`：`ConcurrentSafe` 是 `data_parallel` 的显式 opt-in 能力接口；未实现者默认不允许并发分片执行。
- 适合提升到 `architecture/dag-engine.md`：`data_parallel` 的能力判定应由持有真实算子实例的 Go 层单点负责，Python/Apple 仅保留结构性校验，避免双端事实源漂移。
- 适合提升到相关 guide/reference：为共享算子实例新增 capability gate 时，优先使用现有 optional interface + marker 模式，而不是维护名称名单。
- 更适合先留在 memory：Go 不能静态表达“只读 receiver”，所以这类并发安全承诺需要 convention + race detector 共同兜底；这是通用工程经验，但是否升格为稳定文档要看后续是否反复踩坑。

## Follow-up
- 更新稳定文档，把 `data_parallel` 的安全模型从旧的名单叙述改为 `ConcurrentSafe` 正向声明模型，并记录 Apple/Go 各自职责边界。
- 后续新增 Transform 时，若希望支持 `data_parallel`，默认把“是否应实现 `ConcurrentSafe`”和“是否有 race 测试覆盖”作为代码评审检查项。
