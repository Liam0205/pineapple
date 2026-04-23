# [重复发生的 errcheck 测试代码 CI 失败反思]

## Task
- 针对本次再次因 Go 测试代码未检查 error return value 而触发 CI `errcheck` 失败的情况，记录原因、根因与后续改进方向。

## Expected vs Actual
- Expected outcome.
  - 在已有 `llmdoc/guides/ci-quality-baseline.md` 明确提示 errcheck 高频盲区的前提下，测试代码提交前应在本地被 lint 拦截，不应等到推送后由 CI 暴露问题。
- Actual outcome.
  - 在编写 `operators/transform/redis_get_test.go` 和 `redis_set_test.go` 后，多个 `op.Init(...)`、`s.Set(...)`、`s.SAdd(...)`、`s.RPush(...)` 的 error return value 未检查，直到推送 `v0.5.1` tag 后才由 CI 发现并导致失败，随后不得不修复、移动 tag、force push。

## What Went Wrong
- 已有 guide 只强调“哪些区域容易漏 errcheck”，但没有把本地 lint 绑定为提交前的明确 gate，导致执行时仍把它当成可选项。
- 完成测试编写后直接提交推送，没有在本地运行 `golangci-lint run ./...` 做全量验证。
- 对测试代码存在心理降级：把测试 helper 与测试数据准备视为“辅助代码”，审查标准低于生产代码。
- 结果是同一类问题在第二次任务中重复出现，说明单靠被动提醒不足以改变执行习惯。

## Root Cause
- 根因不是“不知道 errcheck 会检查测试”，而是工作流中缺少“提交前必须跑 lint”的硬性动作，导致已有知识没有转化为执行约束。
- `standard-workflow.md` 当前只写了“逐步验证”和运行相关测试，但没有明确将 `golangci-lint run ./...` 列为提交前必过步骤，因此在时间压力下容易只跑测试、不跑 lint。
- CI 与本地心智不一致：本地把测试代码当作较轻量的辅助实现，CI 则对所有 `.go` 文件一视同仁。

## Missing Docs or Signals
- 已有但不够强的信号：
  - `llmdoc/guides/ci-quality-baseline.md` 已记录 errcheck 高频盲区，并明确“不能因为是测试或示例代码就放宽 errcheck”。这条知识是正确的，但属于被动提示。
- 缺失或需要强化的信号：
  - `llmdoc/guides/standard-workflow.md` 未明确要求在提交前运行 `golangci-lint run ./...` 作为硬性 gate。
  - 现有文档没有把“测试代码与生产代码遵循同等 lint 标准”写成流程动作，只是写成注意事项，执行优先级不足。
  - 缺少一个更直接的提交前检查清单，提醒“新增或修改 Go 测试后，必须本地过 `go test` 与 `golangci-lint run ./...`”。

## Promotion Candidates
- 适合提升为稳定文档：
  - 在 `llmdoc/guides/standard-workflow.md` 的“验证”步骤中，明确加入 `golangci-lint run ./...` 为提交前必须通过的步骤，而不是可选验证。
  - 在 `llmdoc/guides/ci-quality-baseline.md` 中，把 errcheck 盲区提示从“易漏区域”提升为行动指令：凡修改 Go 代码，尤其是测试、benchmark、integration/helper，提交前必须本地跑 lint。
  - 可考虑增加一份更简短的提交前检查清单，列出 Go 任务最小 gate：相关 `go test`、`golangci-lint run ./...`、必要时 codegen freshness。
- 更适合先保留在 memory：
  - “测试代码容易被心理上降级”为辅助代码、从而放松 errcheck 标准，这属于执行偏差模式，适合作为反思记忆持续提醒。
  - 本次涉及 `v0.5.1` tag 修复、移动 tag、force push 的具体事故细节，适合作为案例背景保留在 memory，不必进入稳定架构文档。

## Follow-up
- 后续应优先把 `golangci-lint run ./...` 提升为标准工作流中的显式提交前 gate，并在涉及 Go 测试修改的任务中把 lint 与测试同等对待，避免再次将问题留给 CI 暴露。
