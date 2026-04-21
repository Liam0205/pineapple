# CI 工程质量基线

本指南描述 Pineapple 的 CI 质量检查架构和接入约定。

## 适用范围

当任务涉及以下情况时使用本指南：

- 修改 `.github/workflows/` 中的 CI 配置
- 新增或调整 lint 规则
- 变更覆盖率或 fuzz 配置
- 评估 release gate 触发机制

## CI workflow 架构

`.github/workflows/ci.yml` 包含 7 个 job：

| Job | 职责 | 依赖 |
|-----|------|------|
| go-lint | golangci-lint | 无 |
| python-lint | ruff check | 无 |
| go-test | Go 测试 + 覆盖率 | 无 |
| python-test | Python 测试 + 覆盖率 | 无 |
| codegen-check | 重生成 + git diff 校验 | 无 |
| fuzz | Go native fuzz 短时运行 | go-test |
| benchmark | Go benchmark + artifact | go-test |

所有质量检查集中在 CI workflow 中。Release workflow 通过 `workflow_run` 依赖 CI 结果，不重复任何检查。

## Go lint

工具：`golangci-lint`，配置位于 `.golangci.yml`。

errcheck 高频盲区——以下区域容易遗漏 error return value 检查：

- HTTP handler（`pkg/server/`）
- Benchmark（`benchmarks/`）
- Integration test helper（`integration/`）
- Test helper 函数

不能因为是测试或示例代码就放宽 errcheck。

## Python lint

工具：`ruff`，配置位于 `pyproject.toml` 的 `[tool.ruff]`。

`apple_generated/` 已通过 `extend-exclude` 排除。生成产物的 lint 问题应通过修复 codegen 源或其输入解决，不应手工修改产物。

## 覆盖率

- Go：`go test -coverprofile=coverage.out -covermode=atomic`，产物上传为 artifact（保留 30 天）
- Python：`pytest-cov` 输出 XML 报告，产物上传为 artifact（保留 30 天）

当前无硬性覆盖率阈值。覆盖率报告用于趋势观察，不作为门禁。

## Fuzz

入口选择原则：优先覆盖高扇出输入边界。

当前覆盖的入口：

- `internal/config/` — `FuzzLoad`：JSON 配置解析
- `internal/dag/` — `FuzzBuild`：DAG 图构建

fuzz 策略分两步推进：

1. 先保证"不 panic"——fuzz 目标在任意输入下不应 panic
2. 逐步增加语义断言——对解析结果校验不变量

CI 中 fuzz 运行时间为 30s/入口，用于回归防护而非深度探索。

## Codegen 目录边界

以下目录为生成产物，由 `cmd/pineapple-codegen` 生成：

- `apple_generated/`
- `doc/operators/`

规则：

- lint 工具应排除这些目录
- 产物中的问题通过修改 codegen 源（`pkg/codegen/`）或算子 Schema 解决
- CI 的 `codegen-check` job 通过 `git diff --exit-code` 强制产物与当前 Schema 一致

## Release gate

Release workflow（`.github/workflows/release.yml`）不包含质量检查 job。

触发机制：

- `on: workflow_run` 监听 CI workflow 完成
- `github.event.workflow_run.conclusion == 'success'` 确保 CI 通过
- `startsWith(github.event.workflow_run.head_branch, 'v')` 区分 tag push 和普通 push

此设计确保 release 仅在 CI 全部通过后才触发，同时避免重复执行质量检查。

## 检索指针

- CI 配置：`.github/workflows/ci.yml`
- Release 配置：`.github/workflows/release.yml`
- Go lint 配置：`.golangci.yml`
- Python lint 配置：`pyproject.toml` `[tool.ruff]`
- Fuzz 入口：`internal/config/fuzz_test.go`、`internal/dag/fuzz_test.go`
