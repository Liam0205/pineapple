# [修复 DAG 中止时空 trace 条目反思]

## Task
- 修复 Pineapple DAG 引擎在算子 fatal error 中止时，响应 `trace` 数组泄露未执行节点零值条目的问题。
- 同步更新相关设计文档、README、可观测性说明与回归测试。

## Expected vs Actual
- Expected outcome.
  - DAG 在致命错误后应尽快取消未开始的下游 goroutine，同时返回的 `trace` 只包含实际执行或被记录的算子条目。
  - 运行时行为、测试、design_doc、README 与 llmdoc 对 trace 语义的描述应保持一致。
- Actual outcome.
  - `internal/runtime/scheduler.go` 的 `Run()` 先按 DAG 节点总数预分配 `traces`，取消后未执行的 goroutine 直接沿 `ctx.Done()` 返回，没有写入 trace。
  - `wg.Wait()` 后直接返回整个切片，导致响应中出现 `Name == ""`、`Duration == 0` 的零值空条目。
  - 本次通过在返回前过滤 `Name == ""` 条目修复问题，并补充文档与测试约束该语义。

## What Went Wrong
- 调度器把“按节点数预分配切片”当成了实现细节，但在取消路径上没有补齐“哪些索引实际被写入”的收尾逻辑。
- 错误路径的可观测输出缺少断言，已有测试覆盖了 fatal error 传播，却没有检查 trace 内容是否只包含有效条目。
- 上一轮修复曾被用户提醒需要同步更新 design_doc 和 README，说明自己对“代码修复之外还要补齐文档语义”的执行习惯还不够稳定；这次虽已补齐，但仍暴露出 llmdoc 架构文档此前遗漏了 trace 过滤行为。
- 遇到本地 golangci-lint v2 与项目 `.golangci.yml` v1 格式不兼容时，容易把它当作“本次修复之外的噪音”；实际上这已是明确的工具链信号，需要单独记录后续动作，而不是默认跳过。

## Root Cause
- 根因是预分配切片与取消语义组合后，没有在最终返回前根据有效记录做裁剪或过滤。
- 更深一层的原因是错误路径的返回结构没有被当作稳定契约验证：实现假设“未写入元素留在切片里也无害”，但 API 消费方会直接看到这些零值条目。
- 工具链问题的根因则是项目 lint 配置仍停留在 golangci-lint v1 格式，而本地环境已升级到 v2，二者不兼容。

## Missing Docs or Signals
- 已有且有帮助的信息：
  - `design_doc/bug_empty_trace_on_dag_abort.md` 先完成了根因分析，直接把修复范围收敛到 `Run()` 的返回路径，显著减少了试错。
  - `llmdoc/architecture/dag-engine.md` 已说明取消后等待中的 goroutine 会提前停止，这帮助快速确认 bug 落在 trace 收尾而不是取消机制本身。
- 缺失或需要更新的信息：
  - `llmdoc/architecture/dag-engine.md` 的错误处理/trace 段落此前没有写明：取消后未执行节点不会留下零值 trace，`Run()` 返回前会过滤空名称条目。
  - 稳定文档中还缺少一条更明确的流程信号：已有根因分析 design_doc 的 bug，应优先沿分析文档限定的最小修复面实施，避免无关重构。
  - 项目文档尚未说明当前 `.golangci.yml` 与 golangci-lint v2 的兼容性现状；这应在独立任务中补齐，而不是混入本次 bugfix。

## Promotion Candidates
- 适合提升为稳定文档：
  - 在 `llmdoc/architecture/dag-engine.md` 的错误处理或 debug trace 章节补一句：取消路径下未执行节点不会出现在最终 trace 中，返回前会过滤零值条目。
  - 在流程类文档中强调：对于已有完整根因分析的 bug，优先做局部修复并同步文档/测试，避免扩大改动面。
- 更适合先保留在 memory：
  - 本地 golangci-lint v2 与项目 v1 配置不兼容，需要升级配置格式，但这是独立治理事项，不应和当前功能性 bugfix 绑在一起提交。
  - 若错误路径返回结构是用户可见契约，测试除了校验报错本身，还应显式校验附带的 trace/stats 等观测字段内容。

## Follow-up
- 将本反思保存在 memory，并在后续 `/llmdoc:update` 时把 DAG 引擎文档补充为“取消后会过滤零值 trace 条目”。
- 单独立项升级 `.golangci.yml` 到 golangci-lint v2 格式，并按用户要求安装/对齐正确工具链后恢复 lint 验证。
