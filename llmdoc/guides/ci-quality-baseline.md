# CI 工程质量基线

本指南描述 Pineapple 的 CI 质量检查架构和接入约定。

## 适用范围

当任务涉及以下情况时使用本指南：

- 修改 `.github/workflows/` 中的 CI 配置
- 新增或调整 lint 规则
- 变更覆盖率或 fuzz 配置
- 评估 release gate 触发机制

## CI workflow 架构

`.github/workflows/ci.yml` 包含多个 job（数量与依赖关系以该文件为准，禁止在本指南中硬编码计数）。典型分组：

| Job | 职责 | 依赖 |
|-----|------|------|
| go-lint | golangci-lint | 无 |
| python-lint | ruff check（apple + pine-python） | 无 |
| go-test | Go 测试 + 覆盖率 | 无 |
| java-test | Java 测试 + 覆盖率 | 无 |
| python-test | Python DSL 测试 + 覆盖率 | 无 |
| pine-python-test | Python 引擎测试 + 覆盖率 | 无 |
| cpp-build | pine-cpp Release 构建（4 个可执行文件） | 无 |
| cpp-sanitizer | pine-cpp ASan/UBSan smoke | cpp-build |
| cpp-lint | pine-cpp `-Werror` 严格构建 + 基础卫生检查 | 无 |
| cpp-test | pine-cpp doctest 单测套件 | cpp-build |
| codegen-check | 重生成 + git diff 校验 | 无 |
| fuzz | Go native fuzz 短时运行 | go-test |
| pine-python-fuzz | Hypothesis property-based fuzz | pine-python-test |
| benchmark | Go benchmark + job summary + artifact | go-test |
| pine-python-benchmark | Python 引擎 benchmark | pine-python-test |
| cross-validate | 多 section 跨运行时校验 | go-test + java-test + pine-python-test + cpp-build |
| differential-fuzz | CI 模式 100 轮四引擎差异模糊测试 | go-test + java-test + pine-python-test + pine-cpp-test |

另有独立 nightly workflow：

- **Nightly differential-fuzz**（`.github/workflows/nightly-diff-fuzz.yml`）：nightly 运行四引擎差异比对，固定 10000 轮（手动触发可通过 `inputs.rounds` 覆盖）；不再按工作日/周末分流，CI 通过 shell `timeout` 在 340min 处保护 step 流程，发现分歧或 cancelled 时均会自动创建 GitHub issue
- **Nightly cross-runtime benchmark**（`.github/workflows/nightly-benchmark.yml`）：每日 22:30 UTC+8 运行 `scripts/bench-cross-runtime.sh`，对比 Go/Java/C++ 三运行时（pine-python 因吞吐显著低且开销稀释结论已被收编入复盘，nightly 默认不参与）在多维矩阵下的最大吞吐：DAG 规模（默认 `5,50,100,200`）× 存储模式（`row,column`）× 算子类型（`cpu,io,mixed`，对应新增的 `transform_bench_cpu` / `transform_bench_sleep` 与 lua 混合管道）× 可选 fan-out 并行度。仅运行最大吞吐阶段（不再有顺序延迟与固定 QPS=500 阶段）。Job 超时提升为 90min，自动下载上次成功 artifact 并通过 `scripts/bench-compare.py` 生成 delta 报告，`scripts/bench-analyze.py` 提供单次运行的多维度分析（runtime ranking / parallelism effect / storage effect）。完成后通过 Bark 推送通知，运行前停止 runner 上非必要服务以提升隔离性

所有质量检查集中在 CI workflow 中。Release workflow 通过 `workflow_run` 依赖 CI 结果，不重复任何检查。

Benchmark job 将 `go test -bench` 输出写入 `benchmark.txt`，同时追加到 `$GITHUB_STEP_SUMMARY` 供 PR/CI 页面直接查看；artifact 作为可下载原始结果保留。Python 引擎 benchmark 使用 `pytest-benchmark`，同样生成 summary 和 artifact。

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

## Java lint

工具：`checkstyle`，配置位于 `pine-java/checkstyle.xml`。

- 4-space indent 规则
- `failOnViolation=true`：checkstyle 违规直接导致构建失败
- `OneStatementPerLine` 规则：强制每行最多一条语句，拒绝 `if (...) return;` 等单行压缩写法

## C++ lint

工具：`clang-format`，配置位于 `pine-cpp/.clang-format`（基于 Google style）。

- CI `cpp-lint` job 包含 `-Werror` 严格构建 + 基础卫生检查
- `clang-format` 应用于所有 `pine-cpp/` 源文件（`include/`、`src/`、`cmd/`、`operators/`、`tests/`）
- 本地开发可通过 `clang-format -i` 或编辑器集成自动格式化

## 覆盖率

- Go：`go test -coverprofile=coverage.out -covermode=atomic`，产物上传为 artifact（保留 30 天）
- Python DSL：`pytest-cov` 输出 XML 报告，产物上传为 artifact（保留 30 天）
- Pine-Python：`pytest-cov` 输出 XML 报告，产物上传为 artifact（保留 30 天）

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

### Go native fuzz

当前覆盖的入口：

- `pine-go/internal/config/` — `FuzzLoad`：JSON 配置解析、reserved key 过滤、展开序列引用完整性
- `pine-go/internal/dag/` — `FuzzBuild`：DAG 图构建、pred/succ 对称性、拓扑序合法性
- `pine-go/internal/dataframe/` — `FuzzApplyOutputStorageEquivalence`：RowFrame 与 ColumnFrame 的 ApplyOutput/ToResult 语义一致性
- `pine-go/internal/runtime/` — `FuzzDataParallelEquivalence`：data_parallel 多 shard 与单 shard transform 语义一致性

CI 中 fuzz 运行时间为 30s/入口，并使用 `-run=^$ -parallel=4` 固定为短时 smoke，用于回归防护而非深度探索。

### Pine-Python Hypothesis fuzz

Pine-Python 使用 Hypothesis 进行 property-based testing，覆盖：

- 配置解析不变量
- DAG 构建对称性
- DataFrame storage mode 等价性

CI 中 Hypothesis 使用默认 settings profile（200 examples），依赖 `pine-python-test` job 成功。

### 差异模糊测试（Differential Fuzz）

`scripts/differential-fuzz.py` 生成随机管道配置，**Go/Java/Python/C++ 四引擎**执行并比对输出（R3-X4 接入 C++）：

- CI 模式：100 轮，发现分歧则 job 失败
- **Nightly 模式**（`.github/workflows/nightly-diff-fuzz.yml`）：固定 **10000 轮**，4 引擎 6 pairs 比对，发现分歧时自动创建 GitHub issue 附带复现 fixture（手动触发可通过 `inputs.rounds` 覆盖；周末自动升级模式已移除，统一由 `inputs.rounds` 显式控制）
- 分歧产物保存为 CI artifact，可直接下载复现
- Stability runs：每个配置执行 3 次以排除非确定性差异
- **15 个算子类型**（R3-X5 从 10 扩展）：filter_truncate, filter_condition, filter_paginate, recall_static, recall_resource, reorder_sort, reorder_shuffle_by_salt, merge_dedup, transform_by_lua, transform_copy（4 方向）, transform_dispatch, transform_size, transform_normalize, transform_resource_lookup, observe_log
- **Lua table-aware 用例**：`LUA_ITEM_FUNCTIONS` 覆盖 array input（`#item_tags`）、array 累加（`for i=1,#item_vals`）、array return（`return {a, b}`）三种 host ↔ Lua 复合类型路径，由 `random_items` 生成 `item_tags`/`item_vals` 数据
- **11 个随机化维度**：pipeline 拓扑 / 算子参数 / 数据形状 / 边界值 / data_parallel / storage_mode（50/50 row/column）/ SubFlow / skip / sources（显式 DAG 边）/ common_defaults+item_defaults / debug+_return_trace / 请求直接提供 items / 稀疏 items（部分行缺字段）/ 嵌套 dict/array 值
- **跨存储模式比较**：每个 fixture 自动在 row 和 column 两种 storage_mode 下执行，输出在同一引擎内进行 row-vs-column 等价比较
- **Stabilize sort 条件收紧**：仅在 operator 有非 skip 的 `common_input` 时才附加 skip 字段，避免不必要的 skip 导致非确定性
- **Stratified 报告**：summary 输出 `row=A/B column=C/D` pass/fail 分布 + per-dimension 覆盖计数

### DAG 差异模糊测试（DAG Differential Fuzz）

`scripts/dag-differential-fuzz.py` 在 DAG 构建层面进行三引擎差异比对，与上述执行级差异测试互补：

- 生成随机管道配置，在 Go/Java/Python 中构建 DAG
- 比对边集（依赖关系）和拓扑排序，而非执行输出
- 检测 DAG 构建逻辑的跨引擎不一致，即使执行结果恰好相同的情况也能发现

fuzz 通用策略分两步推进：

1. 先保证"不 panic"——fuzz 目标在任意输入下不应 panic
2. 逐步增加语义断言——对解析结果校验不变量（例如 Go/Java/Python 三引擎的 DAG fuzz 均断言"每个拥有 item 字段的算子在构建后的图中必须有 `_row_set_` 依赖边"这一行集安全不变量）

单个 fuzz target 应设置输入规模预算，避免随机大输入把 CI 变成解析器压力测试。

## Cross-validate 架构

`scripts/cross-validate.sh` 运行多 section 跨运行时校验。具体 section 列表以 `scripts/cross-validate/` 目录为准，禁止在本指南中硬编码层数。sections 默认并行执行（`scripts/cross-validate/_parallel.sh` 调度），`--serial` 可回退串行；单 section 内各运行时也并行运行。

C++ 端是否参与某次比对取决于该 section 中对 `CPP_RUN` / `CPP_DAG` / `CPP_SERVER` / `CPP_CODEGEN` 的引用，以及 `scripts/cross-validate/_prebuild.sh` 是否成功构建 pine-cpp 二进制（输出到 `$WORK_DIR/pineapple-*-cpp`）。

### 端口隔离

各 section 使用独立的千位端口段避免并行执行时端口冲突（如 Section 4 用 4xxx，Section 6 用 6xxx）。每个 section 脚本内部通过 `BASE_PORT` 变量分配端口，确保各运行时 server 实例不会竞争同一端口。

### Section 4: Column-Store Row-vs-Column 比较

Section 4 (`04-column-store.sh`) 验证 `storage_mode: row` 和 `storage_mode: column` 在各引擎内产生相同输出：

- 同一 fixture 分别以 row 和 column 模式执行，比较输出一致性
- 覆盖 Go 引擎内部的 RowFrame vs ColumnFrame 等价性
- 使用与 Section 3 相同的比较策略（支持 `strict_order` flag）

### Section 3 执行 parity 的比较策略

Section 3 (`03-execution-parity.sh`) 比较四引擎 `/execute` 输出：

- **默认 list comparison**：`normalize_json` 规范化（递归 key 排序 + int→float 统一）后字符串精确比较。items 数组**顺序敏感**。
- **Set comparison**（fixture 声明 `"strict_order": false` 时）：`normalize_json_set` 额外对 items 数组按 JSON 序列化排序后比较。**顺序无关**，仅验证 item 集合一致。
- **适用场景**：fixture 有并行 DAG 节点（如多个 recall_static 无 trailing sort）时，item 插入顺序不确定，必须用 set comparison 避免假阳性。

### Differential fuzz 的 stabilizing sort 机制

`scripts/differential-fuzz.py` 在检测到 ≥2 recall-type 算子 + 下游 `filter_paginate` 时，自动在 paginate 前插入 `_stabilize_sort`（按 `_fuzz_distinctive_score` 排序），确保 paginate 输入确定性。这解决了"并行 recall → 非确定性位置 → paginate 切到不同 item 子集"的假阳性问题。

### Metrics Parity section

`scripts/cross-validate/13-metrics-parity.sh` 验证各运行时 pre-init 行为和 `/stats` 数值一致性，包含：

- zero-traffic pre-init：引擎启动后、无请求时 `/stats` 已暴露全部算子
- operator names match：各运行时的算子名集合一致
- exec_count / skip_count / error_count match：算子执行/跳过/错误计数一致
- scheduler.run_count match：调度器运行计数一致
- http.requests_total `POST /execute 2xx` 四方计数一致
- http.request_duration_seconds `POST /execute` count 四方一致
- `/stats.http` schema shape 四方一致（`requests_total` + `request_duration_seconds` 两子树存在 + duration bucket 含 `count`/`sum_ns` 字段）

### Section 15: Error Cause Chain Parity

`scripts/cross-validate/15-error-cause-chain.sh` 用 **probe binary 矩阵**验证四运行时的 ExecutionError cause chain 输出一致 -- 这是 cross-validate 框架的第二种验证模式（第一种是 fixture-driven HTTP 字节对比）。

每方 probe binary:
- pine-go: `cmd/pine-cause-chain-probe/main.go`
- pine-java: `page.liam.pine.CauseChainProbe`
- pine-python: `pine.cli.cause_chain_probe`
- pine-cpp: `cmd/pineapple-cause-chain-probe/main.cpp`

probe 流程：构造 `FakeRedisError("user:42")` -> 包装为 ExecutionError -> catch 外层 -> 用语言原生 idiom 取出 inner -> stdout 输出 `PASS:key=user:42 not found`。Section 15 收集四方 stdout 做字节级 diff。

适用场景：当 parity 维度在 HTTP 接口不可见时（语言层 API 形态、原生能力可用性），用 probe binary 把维度具象化为可比对的 stdout 字符串。

## 跨引擎 Benchmark 基础设施

`scripts/` 提供跨引擎性能对比工具链：

- `scripts/bench-generate-fixtures.py`：生成 small/medium/large 三档 fixture，用于标准化性能基准
- `scripts/cross-engine-bench.py`：HTTP server 模式跨引擎 benchmark，测量 per-request latency（median/p95/p99）+ 并发 RPS
- `scripts/cross-engine-bench-cli.sh`：CLI 模式快速端到端延迟对比

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

Pine-Java 通过 Sonatype Central Portal 发布到 Maven Central（release profile 包含 source/javadoc/GPG 签名）。

`scripts/tag-release.sh` 是创建双 tag（`vX.Y.Z` + `pine-go/vX.Y.Z`）的标准路径，自动校验五处版本源一致后创建 tag 并推送。

## 检索指针

- CI 配置：`.github/workflows/ci.yml`
- Nightly differential-fuzz：`.github/workflows/nightly-diff-fuzz.yml`
- Nightly cross-runtime benchmark：`.github/workflows/nightly-benchmark.yml`
- Release 配置：`.github/workflows/release.yml`
- Go lint 配置：`.golangci.yml`
- Python lint 配置：`pyproject.toml` `[tool.ruff]`
- C++ lint 配置：`pine-cpp/.clang-format`
- Go fuzz 入口：`pine-go/internal/config/load_test.go`、`pine-go/internal/dag/dag_test.go`、`pine-go/internal/dataframe/dataframe_test.go`、`pine-go/internal/runtime/parallel_test.go`
- Pine-Python fuzz：`pine-python/tests/` (Hypothesis)
- Differential-fuzz 脚本：`scripts/differential-fuzz.py`、`scripts/differential-fuzz.sh`
- DAG differential-fuzz 脚本：`scripts/dag-differential-fuzz.py`
- Cross-validate section 列表：`scripts/cross-validate/`
- Cross-validate metrics-parity section：`scripts/cross-validate/13-metrics-parity.sh`
- Cross-validate pine-cpp 预构建：`scripts/cross-validate/_prebuild.sh`
- 跨引擎 benchmark：`scripts/cross-engine-bench.py`、`scripts/cross-engine-bench-cli.sh`、`scripts/bench-generate-fixtures.py`
- 跨运行时 benchmark（nightly）：`scripts/bench-cross-runtime.sh`、`scripts/bench-compare.py`、`scripts/bench-analyze.py`、`scripts/bench-dag-scheduler.sh`
- Tag release：`scripts/tag-release.sh`
- Server stress 入口：`pine-go/pkg/server/server_test.go`
