# 项目概述

Pineapple 是一个面向请求时数据处理的高性能流水线引擎。流水线通过 Python Apple DSL 声明，编译为 JSON 配置，由 Go/Java/Python 运行时构建依赖感知的 DAG 并执行。仓库采用四对等目录布局：`apple/`（Python DSL）、`pine-go/`（Go 引擎）、`pine-java/`（Java 引擎）、`pine-python/`（Python 引擎）。

## Pineapple 是什么

Pineapple 包含四个主要部分：

- `apple/` 中的 Python 声明层，用户通过算子链式调用、子流程、控制流和资源声明来描述流水线。
- `pine-go/` 中的 Go 执行层（模块路径 `github.com/Liam0205/pineapple/pine-go`），以 `pine-go/pine.go` 和 `pine-go/internal/` 为核心，加载 JSON 配置、构造算子、推导依赖关系、并行执行流水线。
- `pine-java/` 中的 Java 执行层，功能对等的替代运行时，适用于 JVM 生态部署场景。
- `pine-python/` 中的 Python 执行层（包名 `pineapple-pine`），功能对等的第三运行时，使用 lupa/LuaJIT 执行 Lua 脚本、ThreadPoolExecutor 调度 DAG、mtime-polling 实现热加载。

四层通过 JSON 解耦。Python DSL 不在运行时调用 Go/Java/Python 引擎，各引擎侧不知道 Python DSL 对象。契约是各引擎 `Engine.create()`/`pine.NewEngine()`/`Engine(json_config)` 消费的配置格式。Go 模块路径为 `github.com/Liam0205/pineapple/pine-go`。

## 为何如此拆分

此拆分服务于五个目标：

- Python 提供简洁的流水线声明、校验和组合体验。
- Go 提供面向并发的运行时，用于请求执行、DAG 调度和长期服务部署。
- Java 提供 JVM 生态的对等运行时，共享同一 JSON 配置格式和算子语义。
- Python 引擎提供纯 Python 生态的对等运行时，适用于快速原型与 ML 集成场景。
- JSON 创建稳定的边界，支持代码生成、测试和跨语言演进，无需运行时桥接。

这也解释了 `pine-go/cmd/pineapple-codegen/main.go` 和 `pine-java/Codegen.java` 的存在：Go 算子 Schema 是类型化 helper 和生成算子文档的唯一事实源，Java 侧的 Codegen 可从同一 Schema JSON 生成等效的 Python helper 和文档。

## 核心执行模型

Pineapple 流水线是一系列命名算子，每个算子声明其读写的字段元数据。引擎构建时：

1. 通过 `pine-go/internal/config/` 解析 JSON 配置。
2. 从 `pipeline_group` 和 `pipeline_map` 展开声明的算子序列。
3. 通过 `pine-go/internal/registry/` 中的注册表构建算子实例。
4. 在 `pine-go/internal/dag/` 中根据字段级数据冒险、行集标记和显式 merge source 构建 DAG。

请求时，引擎创建请求本地的 `pine-go/internal/dataframe.Frame`，按 DAG 在 `pine-go/internal/runtime/` 中执行算子，最后通过 flow contract 投影最终结果。

## 核心概念

### 算子

算子是业务逻辑的基本单元。它们实现 `pine.Operator` 接口，通过 Schema 注册，声明：

- 稳定的类型名如 `transform_copy`
- 六种算子类型之一：Recall、Transform、Filter、Merge、Reorder、Observe
- 用于校验和代码生成的参数规格

内置算子位于 `pine-go/operators/` 下，通过 `init()` + `pine.Register(...)` 自注册。Blank import `pine-go/operators/all.go` 注册全部内置集合。

### Apple DSL

`apple/flow.py` 中的 Apple DSL 记录算子调用、将控制流降级为普通算子 + skip 字段、校验字段使用、并输出 Go 消费的 JSON 配置。它支持动态分发（`flow.some_op(...)`）和 `apple_generated/` 中生成的类型化 helper，但运行时契约始终是 JSON。

### DAG 运行时

Go 引擎不按声明顺序执行算子。它从字段读写和行集标记推导依赖关系，使独立算子并行运行，同时在冒险或行集变异算子需要时保持顺序。

### 资源

资源独立于算子，由 `pine-go/pkg/resource/` 管理。资源在流输出 JSON 中声明，由服务端资源管理器加载，后台定时刷新，注入请求上下文供算子使用。

## 入口点与打包边界

### Go 入口点

- `pine-go/cmd/pineapple-server/main.go` — 运行 `pine-go/pkg/server/` 中的 HTTP 服务，提供 `/health`、`/execute`、`/stats`、`/dag` 端点，并允许通过 `server.Config.Middlewares` 注入业务侧 HTTP middleware。
- `pine-go/cmd/pineapple-codegen/main.go` — 读取已注册的算子 Schema，生成 Python helper 和可选文档。

### Java 入口点

- `pine-java/PineServer.java` — 基于 `com.sun.net.httpserver` 的 HTTP 服务，提供与 Go 对等的 `/health`、`/execute`、`/stats`、`/dag` 端点，支持 middleware 链、config hot-reload 和 reload metrics。
- `pine-java/Codegen.java` — 读取 Schema JSON，生成 `operators.py`、`resources.py`、`__init__.py` 及算子文档。

### Python 引擎入口点

- `python -m pine.cli.run` — 命令行执行管道
- `python -m pine.cli.server` — HTTP 服务（`/health`、`/execute`、`/stats`、`/dag`）
- `python -m pine.cli.dag` — 渲染 DAG 可视化
- `python -m pine.cli.codegen` — 从 Registry 生成 Python DSL 代码

### Python 包

`pyproject.toml` 将 `apple/` 打包为 `pineapple-apple`。已提交的 `apple_generated/` 是开发时生成输出，不包含在发布的 wheel 中。这是有意的，因为 `apple/flow.py` 中的动态分发足以支撑运行时声明；生成类主要改善仓库内的类型化编写体验。

## 关键设计决策

### JSON 是解耦契约

Pineapple 最重要的边界是以 `pine-go/internal/config/types.go` 为根的 JSON 配置 Schema。它解耦了：

- Python 声明与 Go/Java/Python 引擎执行
- Go 算子 Schema 与生成的 Python helper
- 测试 fixture 与任一语言实现
- Go 运行时、Java 运行时与 Python 运行时（三引擎共享同一 JSON 契约）

这就是跨语言测试使用 `pine-go/testdata/e2e_apple_dsl.json` 等文件而非直接桥接的原因。共享 fixture 位于仓库根 `fixtures/` 目录（子目录：`operators/` 单算子、`pipelines/` 端到端管道、`errors/` 错误路径）。Go、Java 和 Python 引擎测试均从同一路径读取。

### Go 算子 Schema 是唯一事实源

Go 中注册的算子 Schema 驱动：

- `pine-go/internal/registry/registry.go` 中的运行时校验
- `pine-go/pkg/codegen/` 中生成的 Python 算子类
- `doc/operators/` 中生成的算子文档
- Pine-Java 侧通过 Schema JSON 做 codegen 和注册时对齐
- Pine-Python 侧维护独立注册表，通过 CI 十三层交叉验证保持对齐

Python DSL 消费这些契约但不重新定义它们。Java 和 Python 引擎各自实现等效语义但不引入新的 Schema 事实源。

### 开发者脚本基础设施

`scripts/` 提供 23 个标准化脚本覆盖完整开发流程：

- `apple-compile.sh` — 编译 Apple DSL 为 JSON
- `codegen.sh` — 从 Registry 生成 Python DSL 代码
- `cross-validate.sh` — Go vs Java vs Python 十三层跨验证
- `differential-fuzz.sh` / `differential-fuzz.py` — 三引擎差异模糊测试
- `run-pipeline.sh` — 指定后端执行管道
- `render-dag.sh` — 渲染 DAG 可视化
- `go-test.sh` / `java-test.sh` / `python-test.sh` / `test-all.sh` — 分语言和全量测试
- `go-bench.sh` / `java-bench.sh` / `python-bench.sh` — 性能基准
- `go-fuzz.sh` / `java-fuzz.sh` / `python-fuzz.sh` — Fuzz 测试
- `lint.sh` — 四语言统一 lint（ruff + golangci-lint + checkstyle + ruff pine-python）
- `bump-version.sh` — 跨四处同步版本号
- `tag-release.sh` — 创建双 tag（`vX.Y.Z` + `pine-go/vX.Y.Z`）并推送
- `bench-generate-fixtures.py` — 生成 small/medium/large 三档 benchmark fixture
- `cross-engine-bench.py` — HTTP server 模式跨引擎 benchmark（latency + RPS）
- `cross-engine-bench-cli.sh` — CLI 模式快速端到端延迟对比

### 引擎实例构建后不可变

`pine-go/pine.go` 中的 `Engine` 编译一次后跨请求共享。可变执行状态存在于请求本地的 DataFrame 和运行时 trace/stats 中。这保证了公开的引擎对并发 `Execute()` 调用是安全的。

### 校验在边界两侧进行

- Python DSL 校验声明级正确性：字段覆盖、死代码、控制流降级假设。
- Go 运行时校验配置结构、算子注册、算子参数、请求输入、运行时输出方法限制。

两层互补而非冗余。

## 质量与发布形态

Pineapple 的质量策略有七个可见层：

- `pine-go/internal/` 和 `pine-go/pkg/` 中的子系统单元测试
- `pine-go/operators/` 中的逐算子单元测试
- `pine-go/engine_test.go` 和 `pine-go/integration/` 中的引擎/集成测试
- `apple/tests/` 中的 Python DSL 测试，包括生成 JSON 并调用 Go 集成测试的跨语言测试
- `pine-java/src/test/` 中的 Java 引擎测试，包括 fixture 对等验证、ServerTest、IntegrationTest（E2E）、JazzerFuzzTest、Jacoco coverage、Checkstyle lint 和 benchmark
- `pine-python/tests/` 中的 Python 引擎测试，包括 fixture 对等验证、Hypothesis property-based fuzz 和 benchmark
- 三引擎差异模糊测试（`scripts/differential-fuzz.py`）：生成随机管道 fixture，Go/Java/Python 三引擎执行并比对输出一致性

CI 在 `.github/workflows/ci.yml` 中运行 Go 测试、Python 测试、Java 测试、Python 引擎测试、codegen 新鲜度检查、十三层三引擎跨验证和差异模糊测试。发布在 `.github/workflows/release.yml` 中在 `v*` 标签上增加 wheel 构建和 PyPI 发布。

## 项目边界

Pineapple 当前不包括：

- 直接的 Python→Go/Java/Python 引擎运行时桥接
- 多模块 Go workspace（`pine-go/` 是独立 Go module）

这些缺失很重要，因为许多变更应保持现有的 JSON 中介、注册表驱动架构，而非引入更紧密的耦合。
