# [修复 CI lint v2 与 Release gate 反思]

## Task
- 修复两个 CI/Release 问题：`golangci-lint-action@v7` 拉取 golangci-lint v2 后与仓库现有 v1 `.golangci.yml` 配置不兼容，以及 `release.yml` 未被完整 CI 检查阻塞。

## Expected vs Actual
- Expected outcome.
  - Go lint 应继续稳定通过，且 Release 只在全部 CI 质量检查通过后才执行构建与发布。
- Actual outcome.
  - 通过 `golangci-lint migrate` 将配置迁移到 v2 格式，补上 `version: "2"` 等兼容变更；同时在 `release.yml` 中新增 `go-lint`、`python-lint`，让 `build` 依赖全部 5 个检查，Release 不再绕过 lint gate。

## What Went Wrong
- 之前引入 `golangci-lint-action@v7` 时，没有同步确认 action 对应的 golangci-lint 主版本与仓库配置格式是否兼容，留下了“工具升级后才暴露”的隐患。
- 之前默认把 CI workflow 当成 Release 的前置条件，但 GitHub Actions 中 tag push 会并行触发多个独立 workflow，Release 并不会自动等待 CI 成功。
- 之前的 CI 反思已经指出缺少“CI 质量基线”指南，但没有及时沉淀成稳定文档，导致 lint 配置格式、Release gate 范围这类关键约束仍靠临时判断。

## Root Cause
- 对外部 CI 工具的“action 版本”和“配置 schema 版本”耦合理解不足，只接入了工具，没有锁定兼容矩阵。
- 对 GitHub Actions workflow 之间的隔离模型理解不够具体，把“同一次 tag push 触发”误当成“天然串行依赖”。
- 质量基线缺少稳定文档，导致 lint、test、release gate 的最小要求没有形成可复用 checklist。

## Missing Docs or Signals
- 缺少一份明确的 CI 质量基线说明，定义 Go/Python lint 方案、版本兼容要求、配置迁移策略，以及 Release 必须重复声明哪些 gate。
- 现有文档没有明确提醒：GitHub Actions 的 CI 与 Release 是独立 workflow，若要阻塞发布，必须在 release workflow 内显式补齐所有 `needs` 依赖。
- `llmdoc/memory/reflections/ci-quality-lint-coverage-fuzz.md` 已给出“缺少 CI 质量基线指南”的信号，这次问题说明该缺口已经影响到真实发布链路。

## Promotion Candidates
- 适合提升为稳定文档：
  - 在 `llmdoc/guides/` 增加 CI 质量基线指南，明确 lint/test/coverage/release gate 的默认组合。
  - 在该指南中补一条外部工具升级规则：升级 GitHub Action 时必须同时核对底层工具主版本与配置 schema 是否兼容。
  - 在 CI/Release 相关文档中补充 GitHub Actions workflow 独立性的说明，并给出“Release 必须显式复刻 gate”为检查项。
- 更适合先保留在 memory：
  - 本次 `golangci-lint migrate` 后，v2 默认启用的 linter 已覆盖原先显式列出的 `govet`、`errcheck`、`staticcheck`、`unused`、`gosimple`、`ineffassign`；这是本仓库当前配置迁移的具体背景，可先保留为任务记忆。

## Follow-up
- 后续应把“CI 质量基线”从 reflection 提升为稳定 guide，覆盖 action/工具版本兼容检查、tag release gate 设计，以及新增 workflow 时的最小质量门禁清单。
