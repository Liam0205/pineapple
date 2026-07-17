# 项目概述

Pineapple 是一个面向请求时数据处理的高性能流水线引擎。流水线通过 Python Apple DSL 声明，编译为 JSON 配置，由 Go / Java / C++ 各运行时构建依赖感知的 DAG 并执行。当前仓库采用一个声明层 + 三个运行时目录的布局：`apple/`（Python DSL）、`pine-go/`（Go 引擎）、`pine-java/`（Java 引擎）、`pine-cpp/`（C++ 引擎，定位为完全 parity 前提下的标杆运行时）。

> 注意：`apple/` 是用 Python 编写的 **DSL 声明层**（编译器），不是运行时引擎。曾经存在的 `pine-python/` Python 运行时引擎已在 v0.9.7 后移除，运行时由 Go/Java/C++ 三引擎组成。本文中提到的 "Python" 若无特别说明均指 Apple DSL，而非已删除的 Python 运行时。

## Pineapple 是什么

Pineapple 包含四个主要部分：

- `apple/` 中的 Python 声明层，用户通过算子链式调用、子流程、控制流和资源声明来描述流水线。
- `pine-go/` 中的 Go 执行层（模块路径 `github.com/Liam0205/pineapple/pine-go`），以 `pine-go/pine.go` 和 `pine-go/internal/` 为核心，加载 JSON 配置、构造算子、推导依赖关系、并行执行流水线。
- `pine-java/` 中的 Java 执行层，功能对等的替代运行时，适用于 JVM 生态部署场景。
- `pine-cpp/` 中的 C++ 执行层，CMake + C++23，包含 `ColumnFrame` 列存、ready-queue DAG 调度器（双隔离线程池）、LuaJIT 集成、`metrics::Provider` 抽象、`resource::Manager` 后台刷新与 HTTP middleware 注入。详见 `architecture/pine-cpp-runtime.md`。

各运行时之间通过 JSON 解耦。Apple DSL 不在运行时调用任一引擎，各引擎侧不知道 Apple DSL 对象。契约是各引擎 `Engine.create()` / `pine.NewEngine()` / `pine::Engine::from_file(...)` 共同消费的配置格式。Go 模块路径为 `github.com/Liam0205/pineapple/pine-go`。

## 为何如此拆分

此拆分服务于以下目标：

- Python 提供简洁的流水线声明、校验和组合体验（Apple DSL）。
- Go 提供面向并发的运行时，用于请求执行、DAG 调度和长期服务部署。
- Java 提供 JVM 生态的对等运行时，共享同一 JSON 配置格式和算子语义。
- C++ 引擎追求实现上限（列存、LuaJIT bridge、COW、arena、并行调度），作为各运行时性能与正确性的标杆参考。
- JSON 创建稳定的边界，支持代码生成、测试和跨语言演进，无需运行时桥接。

这也解释了各运行时各自维护 codegen 入口（`pine-go/cmd/pineapple-codegen/main.go` / `pine-java/Codegen.java` / `pineapple-cpp-codegen`）的存在：算子 Schema 在每个运行时独立注册，CI cross-validate 做最终对齐。

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

### Lua 后端选择（仅 pine-go）

pine-go 的 `transform_by_lua` 算子由 build tag 选择 Lua VM 后端，编译期单一后端零运行时分发，binary 只链一个 VM：

- 默认：**wangshu**（纯 Go Lua 5.1 VM，NaN-boxing + arena GC，v0.2.0+），通过 `CallInto(dst, fn, args...)` 提供零分配边界路径
- Opt-in：`-tags=lua_gopher` → gopher-lua

两后端共享同一 `Backend/Pool/Engine` 抽象（`pine-go/operators/lua/backend.go`）和同一测试套，行为字节级对等。后端对比 benchmark 通过 `make bench-lua-backends` 或 `scripts/bench-lua-backends.sh`（同机串行 + benchstat）。详见 `llmdoc/reference/lua-backend.md`。Lua 后端选择仅 pine-go 适用——pine-java 用 LuaJC 默认后端、pine-cpp 用 LuaJIT，均不暴露 build-tag 切换面。

### 资源

资源独立于算子，由 `pine-go/pkg/resource/`（及各运行时对等实现）管理。资源在流输出 JSON 的 `resource_config` 中声明，由服务端资源管理器加载，后台定时刷新（`interval: -1` 表示永不刷新），注入请求上下文供算子使用。资源分两类：**数据型资源**（值为可序列化纯数据，如 lookup 表 / item 列表，整体快照导出）与**句柄型资源**（值为携带活动连接的不可序列化对象，如 Redis 连接池，由算子按 `resource_name` 借用、用完归还）。详见 `design_doc/11_resource_manager.md` 与 `architecture/dag-engine.md`。

## 入口点与打包边界

### Go 入口点

- `pine-go/cmd/pineapple-server/main.go` — 运行 `pine-go/pkg/server/` 中的 HTTP 服务，提供 `/health`、`/execute`、`/stats`、`/dag` 端点，并允许通过 `server.Config.Middlewares` 注入业务侧 HTTP middleware、通过 `server.Config.Routes` 注册自定义路由（Ingress/Egress 适配器）；`-watch=false`（对应 `Config.Watch`）关闭配置热加载；`-demo-routes` 注册 cross-validate 用的 `POST /api/echo` 演示路由。嵌入场景（Gin 等既有框架）用 `server.NewServer` + `Execute`/`Acquire`/`Close`，不经过内置 net/http 壳。可选通过 `-admin-addr :6060`（对应 `Config.AdminAddr`）启动**独立的 admin server**，仅暴露 `/debug/pprof/*`；admin server 无 `WriteTimeout`，长时 CPU/trace profile（`?seconds=120`）不会被截断。默认空值即不监听 admin 端口，业务端口与 profiling 端口彻底隔离。
- `pine-go/cmd/pineapple-codegen/main.go` — 读取已注册的算子 Schema，生成 Python helper 和可选文档。

### Java 入口点

- `pine-java/PineServer.java` — 基于 `com.sun.net.httpserver` 的 HTTP 服务，提供与 Go 对等的 `/health`、`/execute`、`/stats`、`/dag` 端点，支持 middleware 链、config hot-reload 和 reload metrics。
- `pine-java/Codegen.java` — 读取 Schema JSON，生成 `operators.py`、`resources.py`、`__init__.py` 及算子文档（即 `apple_generated/` 下的 Apple DSL 类型化 helper）。

### C++ 引擎入口点

- `pineapple-cpp-run -config <pipeline.json> -request <request.json> [-static-resources <resources.json>]` — CLI 执行
- `pineapple-cpp-render-dag -config <pipeline.json> -format dot|mermaid [-collapse N]` — DAG 渲染
- `pineapple-cpp-server -config <pipeline.json> [-addr :8080] [-read-header-timeout 10s] [-read-timeout 30s] [-write-timeout 60s] [-idle-timeout 120s] [-max-body-size 10485760] [-dag-pool-size N] [-shard-pool-size N]` — HTTP 服务，支持 graceful shutdown、配置 mtime 热加载、`ServerConfig::middlewares` 注入与 `pine::server::http_metrics_middleware(provider)` 内置指标 middleware
- `pineapple-cpp-codegen -schema-json <out>` — 从 C++ Registry 导出算子 schema JSON
- `pineapple-cpp-codegen -output <dir>` — 发射完整 Apple DSL 产物集（`operators.py` / `__init__.py` / `markers.py` / `resources.py` / `resources_init.py`），与 Go / Java 字节级一致；CI cross-validate `01-codegen-schema.sh` 1d 段对 Go 与 C++ 产物做 `diff -r` 字节级校验

### Python 包

`pyproject.toml` 将 `apple/` 打包为 `pineapple-apple`。已提交的 `apple_generated/` 是开发时生成输出，不包含在发布的 wheel 中。这是有意的，因为 `apple/flow.py` 中的动态分发足以支撑运行时声明；生成类主要改善仓库内的类型化编写体验。

## 关键设计决策

### JSON 是解耦契约

Pineapple 最重要的边界是以 `pine-go/internal/config/types.go` 为根的 JSON 配置 Schema。它解耦了：

- Apple DSL 声明与 Go/Java/C++ 引擎执行
- Go 算子 Schema 与生成的 Python helper
- 测试 fixture 与任一语言实现
- Go 运行时、Java 运行时与 C++ 运行时（三引擎共享同一 JSON 契约）

这就是跨语言测试使用 `pine-go/testdata/e2e_apple_dsl.json` 等文件而非直接桥接的原因。共享 fixture 位于仓库根 `fixtures/` 目录（子目录：`operators/` 单算子、`pipelines/` 端到端管道、`errors/` 错误路径）。Go、Java、C++ 引擎测试均从同一路径读取。

### Go 算子 Schema 是事实参考源，各运行时维护独立 Registry

Go 中注册的算子 Schema 历史上驱动了：

- `pine-go/internal/registry/registry.go` 中的运行时校验
- `pine-go/pkg/codegen/` 中生成的 Python 算子类
- `doc/operators/` 中生成的算子文档

Java / C++ 各自维护独立的 Schema 注册表（`Registry.register` / `pine::register_operator`），均可独立产出 codegen。CI cross-validate 的 codegen-schema section 通过 normalized diff 验证三套 Schema 一致。

Apple DSL 消费这些契约但不重新定义它们。各运行时实现等效语义但不引入新的对外 Schema 事实源。

### 开发者脚本基础设施

`scripts/` 提供一组标准化脚本覆盖完整开发流程（完整清单见 `scripts/` 目录）：

- `apple-compile.sh` — 编译 Apple DSL 为 JSON
- `codegen.sh` — 从 Registry 生成 Python DSL 代码
- `cross-validate.sh` — 各运行时跨验证（具体 section 列表见 `scripts/cross-validate/`）
- `differential-fuzz.sh` / `differential-fuzz.py` — 三引擎（Go/Java/C++）差异模糊测试
- `run-pipeline.sh` — 指定后端执行管道
- `render-dag.sh` — 渲染 DAG 可视化
- `go-test.sh` / `java-test.sh` / `test-all.sh` — 分语言和全量测试
- `go-bench.sh` / `java-bench.sh` — 性能基准
- `go-fuzz.sh` / `java-fuzz.sh` — Fuzz 测试
- `lint.sh` — 各语言统一 lint（ruff + golangci-lint + checkstyle + clang-format + C++ `-Werror` 严格构建）
- `bump-version.sh` — 跨五处同步版本号（含 pine-cpp `kVersion`）
- `tag-release.sh` — 创建双 tag（`vX.Y.Z` + `pine-go/vX.Y.Z`）并推送
- `bench-generate-fixtures.py` — 生成 small/medium/large 三档 benchmark fixture
- `cross-engine-bench.py` — HTTP server 模式跨引擎 benchmark（latency + RPS）
- `cross-engine-bench-cli.sh` — CLI 模式快速端到端延迟对比
- `bench-cross-runtime.sh` — 跨运行时 benchmark（三引擎 × 6 档 DAG 规模，三阶段：顺序延迟 / 固定 QPS / 最大吞吐）
- `bench-dag-scheduler.sh` — C++ DAG 调度器 A/B 对比（master vs 当前分支，基于 hyperfine + hey）
- `bench-compare.py` — 两次 benchmark 运行结果的 delta 报告生成

### 引擎实例构建后不可变

`pine-go/pine.go` 中的 `Engine` 编译一次后跨请求共享。可变执行状态存在于请求本地的 DataFrame 和运行时 trace/stats 中。这保证了公开的引擎对并发 `Execute()` 调用是安全的。

### 校验在边界两侧进行

- Python DSL 校验声明级正确性：字段覆盖、死代码、控制流降级假设。
- Go 运行时校验配置结构、算子注册、算子参数、请求输入、运行时输出方法限制。

两层互补而非冗余。

## 质量与发布形态

Pineapple 的质量策略覆盖以下可见层：

- `pine-go/internal/` 和 `pine-go/pkg/` 中的子系统单元测试
- `pine-go/operators/` 中的逐算子单元测试
- `pine-go/engine_test.go` 和 `pine-go/integration/` 中的引擎/集成测试
- `apple/tests/` 中的 Python DSL 测试，包括生成 JSON 并调用 Go 集成测试的跨语言测试
- `pine-java/src/test/` 中的 Java 引擎测试，包括 fixture 对等验证、ServerTest、IntegrationTest（E2E）、JazzerFuzzTest、Jacoco coverage、Checkstyle lint 和 benchmark
- `pine-cpp/tests/` 中的 C++ doctest 套件 + ASan/UBSan smoke job（CI: `cpp-build` / `cpp-sanitizer` / `cpp-lint` / `cpp-test`）
- 三引擎差异模糊测试（`scripts/differential-fuzz.py`）：生成随机管道 fixture，Go/Java/C++ 三引擎执行并比对输出一致性

CI 在 `.github/workflows/ci.yml` 中运行各运行时测试、codegen 新鲜度检查、cross-validate 与差异模糊测试。Nightly benchmark（`.github/workflows/nightly-benchmark.yml`）每日对比三运行时性能并生成 delta 报告。发布在 `.github/workflows/release.yml` 中在 `v*` 标签上增加 Apple DSL wheel 构建和 PyPI 发布。

## 项目边界

Pineapple 当前不包括：

- 直接的 Apple DSL→任一引擎运行时桥接（始终通过 JSON）
- 多模块 Go workspace（`pine-go/` 是独立 Go module）

这些缺失很重要，因为许多变更应保持现有的 JSON 中介、注册表驱动架构，而非引入更紧密的耦合。
