# CI 工程质量基线

本指南描述 Pineapple 的 CI 质量检查架构和接入约定。

## 适用范围

当任务涉及以下情况时使用本指南：

- 修改 `.github/workflows/` 中的 CI 配置
- 新增或调整 lint 规则
- 变更覆盖率或 fuzz 配置
- 评估 release gate 触发机制

## CI workflow 架构

`.github/workflows/ci.yml` 包含 10 个 job：

| Job | 职责 | 依赖 |
|-----|------|------|
| go-lint | golangci-lint | 无 |
| python-lint | ruff check | 无 |
| java-lint | checkstyle | 无 |
| go-test | Go 测试 + 覆盖率 | 无 |
| python-test | Python 测试 + 覆盖率 | 无 |
| java-test | Java 测试 + 覆盖率 | 无 |
| codegen-check | 重生成 + git diff 校验 | 无 |
| java-fuzz | Jazzer fuzz 短时运行 | java-test |
| fuzz | Go native fuzz 短时运行 | go-test |
| cross-validation | Go vs Java 11 层跨验证 | go-test, java-test |

所有质量检查集中在 CI workflow 中。Release workflow 通过 `workflow_run` 依赖 CI 结果，不重复任何检查。

Cross-validation job 安装 redis-server 后执行 `scripts/cross-validate.sh`，输出写入 `$GITHUB_STEP_SUMMARY` 供 PR 页面直接查看 PASS/FAIL 状态。

## Go lint

工具：`golangci-lint`，配置位于 `.golangci.yml`。

提交前必须对以下高风险区域的改动在本地运行 `golangci-lint run ./相关包`，重点检查 errcheck，不能依赖远端 CI 帮你捕获遗漏的 error return value：

- HTTP handler（`pine-go/pkg/server/`）
- Benchmark（`benchmarks/`）
- Integration test helper（`pine-go/integration/`）
- Test helper 函数

测试代码与生产代码遵循同一套 linter 规则，没有“测试代码可以不检查 error”的例外。

## Python lint

工具：`ruff`，配置位于 `pyproject.toml` 的 `[tool.ruff]`。

`apple_generated/` 已通过 `extend-exclude` 排除。生成产物的 lint 问题应通过修复 codegen 源或其输入解决，不应手工修改产物。

## 覆盖率

- Go：`go test -coverprofile=coverage.out -covermode=atomic`，产物上传为 artifact（保留 30 天）
- Python：`pytest-cov` 输出 XML 报告，产物上传为 artifact（保留 30 天）

当前无硬性覆盖率阈值。覆盖率报告用于趋势观察，不作为门禁。

补覆盖时优先覆盖“可稳定断言的行为边界”，例如：

- HTTP handler：使用 `httptest.NewRequest` + `httptest.NewRecorder` 直接调用 handler，验证状态码、响应体与参数分支，而不是优先启动真实 server。
- 算子单测：优先覆盖 `Init`/`Execute` 主路径、默认值、降级路径和错误路径，而不是只补 happy path。
- 带外部依赖的算子：优先使用内存替身以保留真实客户端交互但避免环境依赖；例如 Redis 测试使用 `github.com/alicebob/miniredis/v2`，不要求本地或 CI 提供外部 Redis。

以下路径通常不适合作为常规单测的优先目标，应在 coverage 评估时单独判断：

- 含 `log.Fatal`、`os.Exit` 等进程级退出逻辑的入口
- 含无限循环、文件监听、长期后台 goroutine 的 watcher/daemon 路径

这类逻辑若必须验证，优先拆出可独立断言的纯逻辑部分，再由少量集成测试覆盖整体接线。

## Fuzz

入口选择原则：优先覆盖高扇出输入边界。

当前覆盖的入口：

- `pine-go/internal/config/` — `FuzzLoad`：JSON 配置解析、reserved key 过滤、展开序列引用完整性
- `pine-go/internal/dag/` — `FuzzBuild`：DAG 图构建、pred/succ 对称性、拓扑序合法性
- `pine-go/internal/dataframe/` — `FuzzApplyOutputStorageEquivalence`：RowFrame 与 ColumnFrame 的 ApplyOutput/ToResult 语义一致性
- `pine-go/internal/runtime/` — `FuzzDataParallelEquivalence`：data_parallel 多 shard 与单 shard transform 语义一致性

fuzz 策略分两步推进：

1. 先保证"不 panic"——fuzz 目标在任意输入下不应 panic
2. 逐步增加语义断言——对解析结果校验不变量

CI 中 fuzz 运行时间为 30s/入口，并使用 `-run=^$ -parallel=4` 固定为短时 smoke，用于回归防护而非深度探索。单个 fuzz target 应设置输入规模预算，避免随机大输入把 CI 变成解析器压力测试。

## 并发压力测试

默认测试可包含轻量 HTTP 并发覆盖。服务器级重压测试必须用环境变量显式开启，例如 `PINEAPPLE_STRESS=1 GOMAXPROCS=$(nproc) go test -race -run TestServerHighConcurrencyStress -count=1 -timeout=10m ./pine-go/pkg/server/`。

重压测试默认不进 CI 门禁，适合在多核服务器、本地 release 前或排查竞态时运行。

HTTP 吞吐 benchmark 使用 `BenchmarkHTTPServerComplexDAGThroughput`。通过 `-args` 控制复杂 DAG：`-pineapple.bench.depth`、`-pineapple.bench.width`、`-pineapple.bench.fanin`、`-pineapple.bench.work`、`-pineapple.bench.items`、`-pineapple.bench.workers`、`-pineapple.bench.reload`。

## Codegen 目录边界

以下目录为生成产物，由 `pine-go/cmd/pineapple-codegen` 生成：

- `apple_generated/`
- `doc/operators/`

规则：

- lint 工具应排除这些目录
- 产物中的问题通过修改 codegen 源（`pine-go/pkg/codegen/`）或算子 Schema 解决
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
- Fuzz 入口：`pine-go/internal/config/load_test.go`、`pine-go/internal/dag/dag_test.go`、`pine-go/internal/dataframe/dataframe_test.go`、`pine-go/internal/runtime/parallel_test.go`
- Server stress 入口：`pine-go/pkg/server/server_test.go`
