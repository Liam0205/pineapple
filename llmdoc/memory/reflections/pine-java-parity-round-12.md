# Pine-Java 第十二轮 Go-parity 审计复盘

## Task
- 第十二轮审计修复（commit aebf589），处理 4 项差异（0H/1M/3L）：类型违规错误分类、ParallelExecutor 错误归属、Registry.buildOperator 错误包装、ValidateOutput 消息格式。

## Expected vs Actual
- Expected: 类型违规（Type violation）应抛出 OperatorException 被引擎归为 ExecutionError（算子主动报告的校验失败）。
- Actual: Java 实现使用 IllegalStateException，被引擎的 RuntimeException catch 归为 PanicError（意外崩溃），与 Go 对等语义不符。

## What Went Wrong

### M1: Type violation 使用 IllegalStateException 而非 OperatorException
类型违规是算子可预期的校验错误（算子输入不满足 Schema 约束），语义上等同于"算子主动拒绝"，应走 OperatorException->ExecutionError 路径。Java 实现使用了 IllegalStateException（Java 惯用表达"非法状态"），但在 Pine 错误模型中被归为 PanicError，严重性被错误抬高。

### L1: ParallelExecutor 错误归属使用占位符 operatorName
ParallelExecutor 在构造 PanicError 时使用 "parallel-shard" 作为 operatorName，而非实际执行的算子名称。错误消息中出现人造名称是真实身份未被正确传递的明确信号。

### L2: Registry.buildOperator 未包装 Init 异常
Go 的 registry 在 buildOperator 失败时返回结构化的 RegistryError。Java 实现让 Init 阶段的 Exception 直接 bubble up，调用方收到裸异常而非结构化错误，破坏了错误模型一致性。

### L3: ValidateOutput 消息格式不匹配
Go 格式为大写类型名、空格分隔违规列表。Java 实现使用了不同的大小写和分隔符，导致 wire format 不一致。

## Root Cause

1. **OperatorException 迁移的长尾效应** -- OperatorException 作为 checked boundary 在第九轮引入，但散落的 IllegalStateException 用法在后续轮次持续被发现（第十轮已反思过此问题）。根因是约定引入时未执行全量 grep 扫描，导致每轮只修复被审计触及的 callsite。

2. **错误身份信息依赖手动传播** -- ParallelExecutor 作为执行基础设施，需要从外部接收 operatorName 才能生成准确的错误消息。初始实现使用硬编码占位符绕过了参数传递需求，这种"先跑通再修"的临时方案变成了永久遗留。

3. **Registry 错误边界未对齐 Go** -- Go 的 registry 有明确的错误包装（返回 fmt.Errorf 包装的结构化错误），Java 实现在 happy path 正确但 error path 未镜像 Go 的包装逻辑。error path 审查优先级常低于 happy path。

## Missing Docs or Signals

- `architecture/dag-engine.md` 中结构化错误模型已描述 OperatorException 边界，但未列举所有应使用 OperatorException 的场景（类型违规、Schema 校验等）。这属于实现级 checklist，不适合提升到架构文档。
- Registry 的错误包装约定未文档化。Go 代码本身即文档（`fmt.Errorf("registry: %w", err)`），但跨运行时实现者可能忽略 error path 的包装模式。

## Promotion Candidates

### 暂留 memory（无需提升到稳定文档）
- **OperatorException 全量扫描教训** -- 第十轮已记录此教训，第十二轮再次验证：约定引入后应执行 `grep -r "IllegalStateException\|throw new Runtime"` 类全扫描。这是工作流教训，非架构知识。
- **错误身份传播 smell** -- "人造名称出现在错误消息中 = 真实身份未被传递"。这是代码审查经验法则，适合留在 memory 中备查。
- **Registry error path 需镜像 Go** -- 属于 parity 实现细节，不需要稳定文档。
- **parity gap 持续收敛** -- 第十二轮降至 0H/1M/3L，确认双运行时对等已接近完成。

## Follow-up
- 无稳定文档更新需求。所有修复项沿用已建立的模式（OperatorException 边界、pine: 错误前缀、Registry 错误包装）。
- 下一轮审计前可考虑执行一次 `grep -rn "IllegalStateException\|throw new RuntimeException" pine-java/` 主动清扫，避免继续出现同类遗漏。
