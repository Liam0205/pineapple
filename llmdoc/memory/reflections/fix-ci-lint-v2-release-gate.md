# [修复 CI lint v2 与 Release 触发机制反思]

## Task
- 第一轮：修复 `golangci-lint-action@v7` 与 v1 `.golangci.yml` 不兼容，以及 `release.yml` 未被 CI 阻塞。
- 第二轮：发现第一轮的"在 release.yml 中复制 lint jobs"方案仍有问题——Release 可能在 CI 完成之前就发布。改为 `workflow_run` 触发机制，Release 只在 CI 全部通过后才执行。

## Expected vs Actual
- Expected: Release 在所有质量检查通过后才执行发布。
- Round 1 actual: 在 release.yml 中复制了 go-lint/python-lint 并加入 `build.needs`。质量检查通过了，但 Release 仍独立于 CI 并行运行，先于 CI 完成。
- Round 2 actual: 将 release.yml 触发机制改为 `workflow_run: workflows: ["CI"], types: [completed]`，删除所有重复检查 jobs。Release 现在严格依赖 CI 结果。

## What Went Wrong
- Round 1 错误地认为"在 release.yml 中复制检查 jobs"等同于"依赖 CI"。实际上两个 workflow 仍是独立并行的——tag push 同时触发 CI 和 Release，Release 内部的检查可能先于 CI 完成。
- 这暴露了对 GitHub Actions workflow 隔离模型的理解仍然不够深入：即使两个 workflow 运行相同的检查，它们也不是同一组 jobs。
- 正确的做法是消除重复，让 Release 通过 `workflow_run` 显式等待 CI 完成。

## Root Cause
- 对 GitHub Actions 的 workflow 间依赖机制不熟悉，第一轮修复时选择了"复制检查"而非"依赖结果"。
- `workflow_run` 是 GitHub 提供的跨 workflow 依赖机制，但因为不常用而被忽略。

## Missing Docs or Signals
- 仍然缺少 CI 质量基线的稳定文档，应该在其中明确：
  - 所有质量检查集中在 CI workflow
  - Release 通过 `workflow_run` 依赖 CI，不重复检查
  - `workflow_run` 的 `head_branch` 过滤用于区分 tag push 和普通 push

## Promotion Candidates
- 适合提升为稳定文档：
  - CI 质量基线指南（`llmdoc/guides/`），涵盖 lint/test/release gate 架构
  - GitHub Actions workflow 间依赖的设计约定
- 保留在 memory：
  - `workflow_run` 的 `head_branch` 对 tag push 的具体行为（待实际验证后确认）

## Follow-up
- 推送后验证：CI 跑完后 Release 才触发，且 `head_branch` 过滤对 tag push 生效。
- 如果 `head_branch` 对 tag push 的行为不符合预期，需要改用 API 查询 tag 的方式。
