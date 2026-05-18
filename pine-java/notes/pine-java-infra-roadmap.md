# Pine-Java 基础设施路线图

## 现状对比

### Pine-Go 基础设施全景

| 维度 | Go | Python | Java |
|------|-----|--------|------|
| CI lint | golangci-lint | ruff | ❌ 无 |
| CI test | `go test ./...` + coverage artifact | pytest + coverage artifact | `mvn test` ✅ |
| Fixture 测试 | `fixtures/fixture_test.go` (44 cases) + `pipelines/` (13 cases) | — | `FixtureTest.java` + `PipelineFixtureTest.java` ✅ |
| Cross-validation | — | — | ci.yml 有 job，依赖 go-test + java-test ✅ |
| Fuzz | 4 个 target（config/dag/dataframe/parallel） | — | ❌ 无 |
| Benchmark | `benchmarks/` (5 files) + CI job | — | `BenchmarkTest.java` + CI job ✅ |
| Integration E2E | `integration/` (6 files) + `testdata/` (12 configs) | — | ❌ 无（仅 fixture） |
| Stress test | `PINEAPPLE_STRESS=1` 门控 | — | ❌ 无 |
| Codegen | `cmd/pineapple-codegen` → `apple_generated/` + `doc/operators/` | — | `Codegen.java` 存在但无 CI freshness check |
| Version | `version.go` → `0.6.6` | `apple/_version.py` → `0.6.6` | `pom.xml` → `0.1.0-SNAPSHOT` ❌ 不同步 |
| 发版 | git tag `v{semver}`，Go module 自动可用 | PyPI Trusted Publisher 自动发布 | ❌ 无发布机制 |
| 下游使用 | `import pine "github.com/..."` + `pkg/server.Run()` | `pip install pineapple-apple` | ❌ 无公共 artifact |

## 待办事项

### 1. 创建独立而对等的脚本/CI/测试

**已有：**
- `ci.yml` 已包含 `java-test`、`java-benchmark`、`cross-validation` 三个 job
- Fixture 测试消费同一套 JSON，覆盖算子级和管线级

**缺口：**

| 项 | Go 侧对应 | Java 需要做 |
|-----|-----------|------------|
| Lint | golangci-lint (ci.yml `go-lint`) | 引入 spotbugs 或 checkstyle，加 `java-lint` job |
| Coverage artifact | `go test -coverprofile` → artifact | `jacoco-maven-plugin` → artifact |
| Fuzz | 4 个 native fuzz target | JQF 或 Jazzer 等价物，至少覆盖 Config 解析和 DAG 构建 |
| Integration E2E | `integration/` 6 个文件，测试完整引擎执行 | Java 侧的引擎级集成测试（非 fixture，直接构造 Engine + execute） |
| HTTP Server 测试 | `pkg/server/server_test.go` 覆盖 handler | `PineServer` 的 handler 测试（request/response/error code） |
| Stress test | `PINEAPPLE_STRESS=1` 门控 | Java 等价物，可用 JMH 或简单并发循环 |
| Version bump | `scripts/bump-version.sh` 同步 Go + Python | 脚本需扩展覆盖 `pom.xml`，同步 Java 版本 |

### 2. 丰富可公用的 fixture 测试

**已有：** 11 个算子 fixture + 13 个管线 fixture，Go/Java 共享。

**缺口：**
- 外部依赖算子无 fixture：`transform_redis_get`、`transform_redis_set`、`transform_resource_lookup`、`recall_resource`、`transform_by_remote_pineapple` — 需要 mock/stub 或专用 fixture 设计
- 边界值 fixture：空 items、单 item、null 字段、类型边界（极大/极小数、-0.0、NaN）
- GoFormat fixture：单独建一组 format-specific fixture，让 Go 生成 expected output，Java 消费验证
- 错误路径 fixture：当前 fixture 仅覆盖 happy path，可加入 expected-error fixture（验证 Go/Java 抛同样的错误消息）

### 3. 创建 cross-validation 测试和 CI

**已有：** `cross-validation` job 确保两套 test suite 都在同一 fixture 集上通过。

**可加强：**
- 输出字节级比较：Go fixture test 写出 `actual.json`，Java 读入对比——确保不仅"各自通过"，而是"产出一致"
- Wire-level E2E：启动 Go server 和 Java server，发相同 HTTP 请求，diff 响应 JSON（trace 时间字段除外）
- 回归防护：每次 fixture 新增/修改，CI 自动要求两端同时通过

### 4. 考虑如何便捷地发版 pine-java

**Go 的发版模型**：推 `v{semver}` tag → CI 通过 → Go module 自动可用（`go get github.com/Liam0205/pineapple@v0.6.6`）

**Python 的发版模型**：同一 tag → CI 通过 → `release.yml` 自动构建 wheel → PyPI Trusted Publisher 发布

**Java 方案选项：**

| 方案 | 优势 | 劣势 |
|------|------|------|
| Maven Central (Sonatype OSSRH) | 业界标准，所有 Maven/Gradle 项目开箱即用 | 初始配置繁琐（GPG 签名、Sonatype 账号、staging→release 流程） |
| GitHub Packages (Maven) | 与 repo 同源，CI token 直接可用 | 下游需额外配置 `<repository>` 指向 GitHub |
| JitPack | 零配置，基于 git tag 自动构建 | 依赖第三方服务，build 环境受限 |

**推荐路径**：先用 GitHub Packages 快速打通（CI 改动最小），后续视下游需求迁移 Maven Central。

**需要做的：**
1. `pom.xml` 版本同步到 `0.6.6`（或下一版本）
2. 添加 `maven-deploy-plugin` + `distributionManagement` 配置
3. `release.yml` 加 Java publish step（`mvn deploy`）
4. `scripts/bump-version.sh` 覆盖 `pom.xml` 版本字段

### 5. 下游开发便捷性

**Go 下游工作流**：
```
go get github.com/Liam0205/pineapple@latest
import pine "github.com/Liam0205/pineapple"
import _ "github.com/Liam0205/pineapple/operators"
// pine.NewEngine(jsonConfig, opts...) + pkg/server.Run()
```

**Python 下游工作流**：
```
pip install pineapple-apple
from apple import Flow  # DSL 声明管线 → JSON config
```

**Java 下游（目标状态）**：
```xml
<dependency>
  <groupId>page.liam</groupId>
  <artifactId>pine</artifactId>
  <version>0.6.6</version>
</dependency>
```
```java
Engine engine = Engine.create(jsonConfig);
// 或 PineServer 直接启动 HTTP 服务
```

**当前缺口**：
- 无公共 artifact → 下游无法通过 Maven/Gradle 引用
- `Engine.create()` 和 `PineServer` API 已可用，但缺少入口级文档
- Java 侧 Codegen 可从 Schema JSON 生成 Python helper，但无 CI 保证新鲜度
- 下游如果同时用 Apple DSL + Pine-Java，需要一个端到端示例：Python 声明 → JSON → Java 引擎加载执行

## 优先级

| 优先级 | 事项 | 状态 | 理由 |
|--------|------|------|------|
| P0 | 版本同步 + bump 脚本覆盖 Java | ✅ 完成 | 发版前置条件 |
| P0 | 发版管线（GitHub Packages → release.yml） | ✅ 完成 | 没有 artifact 其他一切无意义 |
| P1 | Server handler 测试 + Integration E2E | ✅ 完成 | 当前 Java 测试仅覆盖 fixture，server 层零覆盖 |
| P1 | Coverage artifact (Jacoco) | ✅ 完成 | 与 Go 侧对等的质量可见性 |
| P2 | 丰富 fixture（错误路径 + 边界值 + GoFormat） | ✅ 完成 | 加强 cross-validation 信心 |
| P2 | 输出字节级 cross-validation | — | 从"各自通过"升级为"产出一致"（未纳入本轮） |
| P3 | Java lint (checkstyle) | ✅ 完成 | 代码质量基线 |
| P3 | Fuzz (Jazzer) | ✅ 完成 | Go 有 4 个 target，Java 3 个（Config/DAG/Engine） |
| P3 | 端到端下游示例 | ✅ 完成 | QuickStart.java |
| — | CI concurrency control | ✅ 完成 | 同分支前序 run 自动取消 |
