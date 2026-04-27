# 项目概述

Pineapple 是一个面向请求时数据处理的高性能流水线引擎。流水线通过 Python Apple DSL 声明，编译为 JSON 配置，由 Go 运行时构建依赖感知的 DAG 并执行。

## Pineapple 是什么

Pineapple 包含两个主要部分：

- `apple/` 中的 Python 声明层，用户通过算子链式调用、子流程、控制流和资源声明来描述流水线。
- 以 `pine.go` 和 `internal/` 为核心的 Go 执行层，加载 JSON 配置、构造算子、推导依赖关系、并行执行流水线。

两层通过 JSON 解耦。Python 侧不在运行时调用 Go，Go 侧不知道 Python 对象。契约是 `pine.NewEngine()` 消费的配置格式。

## 为何如此拆分

此拆分服务于三个目标：

- Python 提供简洁的流水线声明、校验和组合体验。
- Go 提供面向并发的运行时，用于请求执行、DAG 调度和长期服务部署。
- JSON 创建稳定的边界，支持代码生成、测试和跨语言演进，无需运行时桥接。

这也解释了 `cmd/pineapple-codegen/main.go` 的存在：Go 算子 Schema 是类型化 helper 和生成算子文档的唯一事实源，但执行仍通过 JSON 配置而非 FFI 或嵌入式解释器。

## 核心执行模型

Pineapple 流水线是一系列命名算子，每个算子声明其读写的字段元数据。引擎构建时：

1. 通过 `internal/config/` 解析 JSON 配置。
2. 从 `pipeline_group` 和 `pipeline_map` 展开声明的算子序列。
3. 通过 `internal/registry/` 中的注册表构建算子实例。
4. 在 `internal/dag/` 中根据屏障、数据冒险和显式 merge source 构建 DAG。

请求时，引擎创建请求本地的 `internal/dataframe.Frame`，按 DAG 在 `internal/runtime/` 中执行算子，最后通过 flow contract 投影最终结果。

## 核心概念

### 算子

算子是业务逻辑的基本单元。它们实现 `pine.Operator` 接口，通过 Schema 注册，声明：

- 稳定的类型名如 `transform_copy`
- 六种算子类型之一：Recall、Transform、Filter、Merge、Reorder、Observe
- 用于校验和代码生成的参数规格

内置算子位于 `operators/` 下，通过 `init()` + `pine.Register(...)` 自注册。Blank import `operators/all.go` 注册全部内置集合。

### Apple DSL

`apple/flow.py` 中的 Apple DSL 记录算子调用、将控制流降级为普通算子 + skip 字段、校验字段使用、并输出 Go 消费的 JSON 配置。它支持动态分发（`flow.some_op(...)`）和 `apple_generated/` 中生成的类型化 helper，但运行时契约始终是 JSON。

### DAG 运行时

Go 引擎不按声明顺序执行算子。它从字段读写推导依赖关系，使独立算子并行运行，同时在冒险或屏障算子需要时保持顺序。

### 资源

资源独立于算子，由 `pkg/resource/` 管理。资源在流输出 JSON 中声明，由服务端资源管理器加载，后台定时刷新，注入请求上下文供算子使用。

## 入口点与打包边界

### Go 入口点

- `cmd/pineapple-server/main.go` — 运行 `pkg/server/` 中的 HTTP 服务，提供 `/health`、`/execute`、`/stats`、`/dag` 端点，并允许通过 `server.Config.Middlewares` 注入业务侧 HTTP middleware。
- `cmd/pineapple-codegen/main.go` — 读取已注册的算子 Schema，生成 Python helper 和可选文档。

### Python 包

`pyproject.toml` 将 `apple/` 打包为 `pineapple-apple`。已提交的 `apple_generated/` 是开发时生成输出，不包含在发布的 wheel 中。这是有意的，因为 `apple/flow.py` 中的动态分发足以支撑运行时声明；生成类主要改善仓库内的类型化编写体验。

## 关键设计决策

### JSON 是解耦契约

Pineapple 最重要的边界是以 `internal/config/types.go` 为根的 JSON 配置 Schema。它解耦了：

- Python 声明与 Go 执行
- Go 算子 Schema 与生成的 Python helper
- 测试 fixture 与任一语言实现

这就是跨语言测试使用 `testdata/e2e_apple_dsl.json` 等文件而非直接桥接的原因。

### Go 算子 Schema 是唯一事实源

Go 中注册的算子 Schema 驱动：

- `internal/registry/registry.go` 中的运行时校验
- `pkg/codegen/` 中生成的 Python 算子类
- `doc/operators/` 中生成的算子文档

Python DSL 消费这些契约但不重新定义它们。

### 引擎实例构建后不可变

`pine.go` 中的 `Engine` 编译一次后跨请求共享。可变执行状态存在于请求本地的 DataFrame 和运行时 trace/stats 中。这保证了公开的引擎对并发 `Execute()` 调用是安全的。

### 校验在边界两侧进行

- Python DSL 校验声明级正确性：字段覆盖、死代码、控制流降级假设。
- Go 运行时校验配置结构、算子注册、算子参数、请求输入、运行时输出方法限制。

两层互补而非冗余。

## 质量与发布形态

Pineapple 的质量策略有四个可见层：

- `internal/` 和 `pkg/` 中的子系统单元测试
- `operators/` 中的逐算子单元测试
- `engine_test.go` 和 `integration/` 中的引擎/集成测试
- `apple/tests/` 中的 Python DSL 测试，包括生成 JSON 并调用 Go 集成测试的跨语言测试

CI 在 `.github/workflows/ci.yml` 中运行 Go 测试、Python 测试和 codegen 新鲜度检查。发布在 `.github/workflows/release.yml` 中在 `v*` 标签上增加 wheel 构建和 PyPI 发布。

## 项目边界

Pineapple 当前不包括：

- 直接的 Python→Go 运行时桥接
- 多模块 Go workspace
- CI 中内置的 lint 或覆盖率门控
- 服务器中的自动资源热加载

这些缺失很重要，因为许多变更应保持现有的 JSON 中介、注册表驱动架构，而非引入更紧密的耦合。
