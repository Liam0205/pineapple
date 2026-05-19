# 关键约定

以下约定贯穿 Pineapple 大部分工作，应视为稳定默认行为。

## JSON 配置是 Python 与 Go/Java/Python 引擎之间的契约

`apple/` 中的 Apple DSL 声明流水线，但 Go 引擎、Java 引擎和 Python 引擎仅消费符合 `pine-go/internal/config/types.go` 结构的 JSON。该 JSON 是以下场景的解耦边界：

- Python DSL 编译
- Go 引擎加载
- Java 引擎加载
- Python 引擎加载
- `testdata/` 中的测试数据（位于 `pine-go/testdata/`）
- 生成产物和跨语言集成测试

跨 Python/Go/Java 边界的变更应优先保持或有意演进该 JSON 契约，而非引入运行时桥接。

## 算子名称编码算子类型

内置算子采用类型前缀命名：

- `recall_*`
- `transform_*`
- `filter_*`
- `merge_*`
- `reorder_*`
- `observe_*`

该命名在多处有意义：

- 开发者从前缀即可推断运行时语义
- Apple DSL 在 `apple/flow.py` 中通过 `recall_` 前缀推断 `recall=true`
- 生成文档和 helper 类保持这些稳定名称

不要引入隐藏类型分类的算子名称。

## 注册基于副作用

算子和资源通过 `init()` 函数和公共包装器自注册：

- Go 侧算子调用 `pine.Register(...)`，资源调用 `pine.RegisterResource(...)`
- Java 侧在 `AllOperators.java` 的 static initializer 中调用 `Registry.register(...)`
- Python 引擎侧在 `pine-python/pine/operators/__init__.py` 中注册全部内置算子

Go 的 blank import 是标准的聚合机制。`pine-go/operators/all.go` 使得 `pine-go/cmd/pineapple-server/main.go` 和 `pine-go/cmd/pineapple-codegen/main.go` 等入口点可通过 import 副作用注册全部内置算子。Java 侧通过 `AllOperators.ensureRegistered()` 触发类加载。

当二进制文件或测试依赖内置算子时，先检查 blank import 或 `ensureRegistered()` 调用。

## 版本同步跨四组文件

Pineapple 版本号在以下位置有意同步：

- `pine-go/version.go`
- `pine-java/pom.xml`
- `pine-python/pyproject.toml`
- `apple/_version.py`
- 包含 `_PINEAPPLE_VERSION` 的 JSON fixture，包括 `pipeline.json` 和 `pine-go/testdata/` 中的文件

`scripts/bump-version.sh` 是保持对齐的标准路径。仅修改一侧语言常量的版本升级是不完整的。

## 生成代码必须保持最新

生成产物已提交到仓库，必须与当前 Schema 一致。关键生成输出：

- `apple_generated/`
- `doc/operators/`

CI 通过 `.github/workflows/ci.yml` 强制检查新鲜度：运行 codegen 二进制并在 `git diff --exit-code` 时失败。若变更涉及算子 Schema、codegen 模板或资源 Schema，在认为工作完成前必须重新生成产物。

Python lint 使用 `ruff`，配置位于 `pyproject.toml` 的 `[tool.ruff]`。其中 `apple_generated/` 是 codegen 产物，已通过 `extend-exclude` 排除；若生成代码不符合 lint 规则，应修复 codegen 或其输入，而不是手工修改产物。

## Go/Java/Python 三独立 Schema 源 + CI 十一层交叉验证

Go、Java 和 Python 各自维护独立的算子 Schema 注册表，互不依赖。三者在 CI 中通过 `scripts/cross-validate.sh` 的十一层交叉验证保持对齐：

1. **Codegen-Schema 层**：三侧各自导出 JSON（Go: `pineapple-codegen -schema-json`; Java: `Codegen --export-schema`; Python: `pine.cli.codegen`），CI 执行 normalized diff；同时比对生成的 Python DSL 产物字节级一致性
2. **Render-DAG 层**：共享 `fixtures/pipelines/`，三侧渲染 DOT/Mermaid/Collapsed 输出并做字节级 diff
3. **Execution 层**：共享 `fixtures/pipelines/`，三侧执行同一 fixture 并比对 JSON 输出
4. **Column-store 层**：相同 fixture 在 `storage_mode=column` 下三侧执行并比对
5. **Error 层**：共享 `fixtures/errors/`，三侧执行错误路径 fixture 并比对错误输出格式
6. **Server HTTP 层**：端点行为 parity（health、execute、stats、dag、413 body limit、cancellation 等）
7. **Cancellation 层**：超时和取消信号传播行为三侧对比
8. **Concurrent 层**：并发调度语义三侧对比
9. **Raw-byte 层**：二进制数据处理三侧对比
10. **Hot-reload 层**：配置热加载行为三侧对比
11. **Redis-integration 层**：Redis 算子行为三侧对比

实践中意味着：

- 新增/修改算子 Schema 需三侧同步更新
- CI schema diff gate 是对齐的最终仲裁
- 生成的 Python 类和 Markdown 文档仍为派生输出
- 任一侧可独立生成 codegen 产物（Go 从自身 Registry，Java 通过 `--schema-from-registry`）
- 共享 fixture 位于仓库根 `fixtures/` 目录（三子目录：`operators/`、`pipelines/`、`errors/`），三侧通过相对路径访问

## 测试变更应遵循已有测试结构

Pineapple 的持久测试模式有五层：

1. `pine-go/internal/` 和 `pine-go/pkg/` 中运行时/配置/注册表/资源子系统的单元测试
2. 每个内置算子包的单元测试
3. 使用真实或仅测试用算子的 Go 引擎和集成测试
4. Python DSL 测试，包括跨语言 JSON→Go 执行测试
5. Go/Java/Python 十一层跨验证测试（`scripts/cross-validate.sh`），覆盖 codegen-schema、render、execution、column-store、error、server HTTP、cancellation、concurrent、raw-byte、hot-reload、redis-integration

优先扩展最近的已有层，而非创建一次性测试风格。

工程质量基线默认包含三类 lint：

- Go 使用 `golangci-lint`，配置位于 `pine-go/.golangci.yml`
- Python（apple + pine-python）使用 `ruff`，配置位于 `pyproject.toml` 的 `[tool.ruff]`
- Java 使用 `checkstyle`

关键输入边界应补 Go native fuzz 测试，优先覆盖 JSON/配置解析、DAG 构建等高扇出入口。

## pre-1.0 兼容性立场

Pineapple 仍处于 1.0 之前阶段，API 与行为语义可以随版本演进而调整，不承诺为了保留历史错误语义而维持向后兼容。

当任务是在修复语义性 bug 时，应优先选择正确语义，而不是继续兼容错误行为；只有任务明确要求保兼容时，才应把历史行为视为约束。

## 并发假设是刻意的

引擎构建一次后并发复用。算子初始化一次，然后为多个请求执行。任何算子实现都应假设 `Execute` 可能跨请求并发运行，除非显式同步或在 `Init()` 后不可变，否则不应依赖存储在算子结构体上的请求本地可变状态。

## Codegen 是构建时桥梁，而非运行时桥梁

Go 和 Java 各自拥有独立的 codegen 路径，均为构建时工具，不创建运行时集成路径。保持当前架构：

- Python 声明
- JSON 承载契约
- Go/Java/Python 执行

Go 侧 `pine-go/cmd/pineapple-codegen/main.go` 从自身注册表生成；Java 侧 `Codegen.java` 支持双模式（`--schema-from-registry` 从内部 Registry 生成，`-schema <path>` 从外部 JSON 生成）；Python 引擎侧 `pine.cli.codegen` 从自身 Registry 生成。

## 外部 I/O 与并发安全默认值

### 跨运行时格式一致性

Go 的格式化行为是跨运行时的规范参考。Java 侧通过 `GoFormat` 工具类（`sprint`、`formatFloatF`、`formatG`）复制 Go 标准库行为。不应依赖 Java 原生 `Double.toString()` 或 `String.format("%g",...)` 的默认行为。

已统一使用 GoFormat 的算子：`TransformResourceLookup`、`TransformRedisGet`、`FilterCondition`、`ReorderShuffle`。各算子不应再自行实现 format helper（如 `formatValue`、`formatFloatG`），GoFormat 是单一事实源。

### 有界读取

读取外部响应时必须使用 `io.LimitReader(body, limit+1)`，禁止裸 `io.ReadAll`。读取后若 `len(data) > limit` 则视为溢出错误。`max_response_size` 类参数的默认值为 10MB。

### 全局副作用保护

进程级 side effect（如 `log.SetPrefix`）在热加载场景下可能被多次触发。使用 `sync.Once` 保证仅执行一次。

### Goroutine 生命周期

后台 goroutine 必须接受 `context.Context`，并在 `select` 中监听 `ctx.Done()` 以实现干净的取消传播。禁止依赖永不退出的 goroutine 存活模式。
