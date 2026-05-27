# 关键约定

以下约定贯穿 Pineapple 大部分工作，应视为稳定默认行为。

## JSON 配置是 Python 与各运行时引擎之间的契约

`apple/` 中的 Apple DSL 声明流水线，但各运行时引擎仅消费符合 `pine-go/internal/config/types.go` 结构的 JSON。该 JSON 是以下场景的解耦边界：

- Python DSL 编译
- Go 引擎加载
- Java 引擎加载
- Python 引擎加载
- C++ 引擎加载
- `testdata/` 中的测试数据（位于 `pine-go/testdata/`）
- 生成产物和跨语言集成测试

跨 Apple DSL / Go / Java / Python / C++ 边界的变更应优先保持或有意演进该 JSON 契约，而非引入运行时桥接。

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
- C++ 侧通过 **`PINE_REGISTER_OPERATOR_T(Type, schema)` 宏**（首选）在每个 `operators/<category>/<name>.cpp` 中 static init 注册——编译期 `OperatorTraits<T>` 解析标记位，跳过 `dynamic_cast` probe。旧 `PINE_REGISTER_OPERATOR(SCHEMA, FACTORY)` 宏仍可用但非首选。资源 fetcher 通过 `pine::resource::register_fetcher_factory(type, factory)` 注册

Go 的 blank import 是标准的聚合机制。`pine-go/operators/all.go` 使得 `pine-go/cmd/pineapple-server/main.go` 和 `pine-go/cmd/pineapple-codegen/main.go` 等入口点可通过 import 副作用注册全部内置算子。Java 侧通过 `AllOperators.ensureRegistered()` 触发类加载。C++ 侧的内置算子被 CMake 链接进 `pine_operators` 静态库，所有可执行入口都依赖该库以触发 static init 注册。新增 C++ 算子时应使用 `PINE_REGISTER_OPERATOR_T(Type, schema)` 而非 `PINE_REGISTER_OPERATOR(schema, factory)`。

当二进制文件或测试依赖内置算子时，先检查 blank import、`ensureRegistered()` 调用、Python 包导入或 C++ 静态库链接。

## 版本同步跨五组文件

Pineapple 版本号在以下位置有意同步：

- `pine-go/version.go`
- `pine-java/pom.xml`
- `pine-python/pyproject.toml`
- `apple/_version.py`
- `pine-cpp/include/pine/pine.hpp`（`kVersion` 常量）
- 包含 `_PINEAPPLE_VERSION` 的 JSON fixture，包括 `pipeline.json` 和 `pine-go/testdata/` 中的文件

`scripts/bump-version.sh` 是保持对齐的标准路径，已覆盖第五处 C++ 版本常量。仅修改一侧语言常量的版本升级是不完整的。

`scripts/tag-release.sh` 是创建双 tag 的标准路径，自动校验五处版本源一致后创建 `vX.Y.Z` + `pine-go/vX.Y.Z` tag 并推送。

## 生成代码必须保持最新

生成产物已提交到仓库，必须与当前 Schema 一致。关键生成输出：

- `apple_generated/`
- `doc/operators/`

CI 通过 `.github/workflows/ci.yml` 强制检查新鲜度：运行 codegen 二进制并在 `git diff --exit-code` 时失败。若变更涉及算子 Schema、codegen 模板或资源 Schema，在认为工作完成前必须重新生成产物。

Python lint 使用 `ruff`，配置位于 `pyproject.toml` 的 `[tool.ruff]`。其中 `apple_generated/` 是 codegen 产物，已通过 `extend-exclude` 排除；若生成代码不符合 lint 规则，应修复 codegen 或其输入，而不是手工修改产物。

## 现有运行时的独立 Schema 源 + CI 多层交叉验证

Go、Java、Python 与 C++ 各自维护独立的算子 Schema 注册表（Go: `pine-go/internal/registry`；Java: `Registry`；Python: `pine.registry`；C++: `pine::register_operator`），互不依赖。各运行时在 CI 中通过 `scripts/cross-validate.sh` 的多层交叉验证保持对齐。

层级以 `scripts/cross-validate/` 目录下的脚本为准（codegen-schema、render-dag、execution、column-store、error、server-http、cancellation、concurrent、raw-byte、hot-reload、redis-integration、extensibility-parity、metrics-parity 等）。**禁止在文档中硬编码层数或运行时数量**——如需引用具体层级，请直接指向 `scripts/cross-validate/<NN>-*.sh`，避免引入新的硬编码失效点。

各 section 是否包含 C++ 比对取决于脚本中对 `CPP_RUN` / `CPP_DAG` / `CPP_SERVER` / `CPP_CODEGEN` 环境变量的引用；C++ 二进制由 `scripts/cross-validate/_prebuild.sh` 检测并条件性构建。

实践中意味着：

- 新增/修改算子 Schema 需各运行时同步更新
- CI schema diff gate 是对齐的最终仲裁
- 生成的 Python 类和 Markdown 文档仍为派生输出
- 任一侧可独立生成 codegen 产物（Go 从自身 Registry，Java 通过 `--schema-from-registry`，Python 通过 `pine.cli.codegen`，C++ 通过 `pineapple-cpp-codegen -schema-json`）
- 共享 fixture 位于仓库根 `fixtures/` 目录（三子目录：`operators/`、`pipelines/`、`errors/`），各运行时通过相对路径访问

## 跨引擎对等性必须覆盖能力等价

跨引擎 parity 不仅要求"已有功能的输入输出一致"（函数等价），还要求"下游可用的集成模式一致"（能力等价）。

Pineapple 是基础设施。它的正确性不仅是 API 的输出，还包括它对构建于其上的业务施加的开发范式约束。验证维度：

- **函数等价**：已知端点、已知参数 → 相同输出（cross-validate 已覆盖）
- **能力等价**：下游能否用相同模式扩展功能（middleware 拦截自定义路径、handler 注册、回调注入）
- **负空间行为**：未注册路径、未知参数、边界条件在各引擎间表现一致
- **开发范式对等**：下游项目的典型使用方式（如通过 middleware 添加 /metrics 端点）在三引擎间可行

教训来源：Java PineServer 缺少根 fallback context 导致 middleware 无法拦截自定义路径，Go 侧自然支持。19 轮审计未覆盖此维度。

## 测试变更应遵循已有测试结构

Pineapple 的持久测试模式分层为：

1. `pine-go/internal/` 和 `pine-go/pkg/` 中运行时/配置/注册表/资源子系统的单元测试
2. 每个内置算子包的单元测试（Go / Java / Python / C++）
3. 使用真实或仅测试用算子的 Go 引擎和集成测试
4. Python DSL 测试，包括跨语言 JSON→Go 执行测试
5. `scripts/cross-validate.sh` 多 section 跨运行时校验（具体覆盖以 `scripts/cross-validate/` 为准）

优先扩展最近的已有层，而非创建一次性测试风格。

工程质量基线默认包含 lint：

- Go 使用 `golangci-lint`，配置位于 `pine-go/.golangci.yml`
- Python（apple + pine-python）使用 `ruff`，配置位于 `pyproject.toml` 的 `[tool.ruff]`
- Java 使用 `checkstyle`（自定义 `pine-java/checkstyle.xml`，4-space indent，`failOnViolation=true`），包含 `OneStatementPerLine` 规则强制每行最多一条语句
- C++ 使用 `-Werror` 严格构建作为 lint 等价（CI 中的 `cpp-lint` job）

关键输入边界应补 Go native fuzz 测试，优先覆盖 JSON/配置解析、DAG 构建等高扇出入口。C++ 端的内存与并发错误由 ASan/UBSan 与 doctest 测试套件兜底（`cpp-sanitizer` / `cpp-test` job）。

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

### 跨运行时 dedup key 类型标识

`merge_dedup` 算子的去重键必须使用 **type-prefixed string key** 格式（`"<type>:<canonical_value>"`），避免不同 JSON 类型产生相同字符串表示导致误判重复：

- Go: `fmt.Sprintf("%T:%v", v, v)` + 特殊处理 `-0.0 → +0.0`、composite types → `json.Marshal`
- Java: `GoFormat.sprint` 输出带类型前缀的规范化字符串
- Python: `f"{type(v).__name__}:{v}"` + unhashable types 特殊路径
- C++: `type_tag + ":" + dump_json(v)` 对 composite types

关键边角：`true`/`1`/`1.0` 在弱类型 JSON 中有相同 string 表示，必须通过类型前缀区分。`-0.0` 必须规范化为 `+0.0`。

### 跨运行时 shuffle anyToString 一致性

`reorder_shuffle_by_salt` 算子对 salt 字段值做字符串转换时，各运行时必须遵循：

- 所有数值类型（含整数）使用 `%g` / `formatG` 格式化，而非 `%d` 或 `fmt.Sprint`
- bool 类型特殊处理（Python 的 `isinstance(v, bool)` 检查必须在 `int` 之前）
- composite types（map/list）使用 JSON 序列化
- shuffle 使用 original index 作为最终 tiebreaker，保证同 hash 值时排序确定性

### 有界读取

读取外部响应时必须使用 `io.LimitReader(body, limit+1)`，禁止裸 `io.ReadAll`。读取后若 `len(data) > limit` 则视为溢出错误。`max_response_size` 类参数的默认值为 10MB。

### 全局副作用保护

进程级 side effect（如 `log.SetPrefix`）在热加载场景下可能被多次触发。使用 `sync.Once` 保证仅执行一次。

### Goroutine 生命周期

后台 goroutine 必须接受 `context.Context`，并在 `select` 中监听 `ctx.Done()` 以实现干净的取消传播。禁止依赖永不退出的 goroutine 存活模式。C++ 端等价约束：HTTP server 的 `Server::stop()` 在关闭监听后等待 `in_flight_` 归零（5s 超时）后返回；`resource::Manager::stop()` 通过 `stopping_` + condition variable 解除阻塞、join 所有刷新线程。

## 禁止硬编码定量描述

下列定量描述容易在版本演进中失效，必须通过引用脚本/表格而非数字字面量来表达：

- 跨运行时数量（"三运行时"、"四运行时"）→ 改为"各运行时"或显式列出
- cross-validate 层数 → 直接引用 `scripts/cross-validate/` 目录
- CI job 数量 → 引用 `.github/workflows/ci.yml` 而非硬编码

定量数字若必须出现，应放在有维护责任人的表格内并与代码处于同一文档目录，便于一同更新。

## http_metrics middleware 必须 default-on

各运行时（pine-go / pine-java / pine-python / pine-cpp）的 HTTP server 必须**无条件**注入 `http_metrics_middleware`（含 `HttpStats` 累加器作为第二写入路径），不得要求用户显式 opt-in。`metrics_provider` 为 null 时自动 tie-off 至 `NopProvider`，middleware 链与外部观测语义保持各方字节一致。

理由：R2 后续审计（2026-05-23）发现 pine-java 此前是 conditional 接入，pine-python 完全缺失，与 R2 时 pine-cpp 的 conditional 状态同型。本约定收口"各方装配条件统一"，由 Section 13 schema shape 检查长期监管。

## InputFieldSpec 三态模型（默认 Nullable 字段模式）

各运行时的 `InputFieldSpec`（或等价的字段访问策略）遵循三态模型，控制算子输入构建时对字段缺失/nil 的处理行为：

- **Nullable**（默认）：字段缺失 → 返回 error（字段必须存在于 frame 中）；字段存在但值为 nil → 透传 nil 给算子。这是所有字段的默认行为。
- **Strict**：字段缺失或值为 nil → 均返回 error。通过 JSON 配置中的 `strict_common` / `strict_item` 字段列表 opt-in。
- **Defaulted**：字段缺失或值为 nil → 替换为默认值。通过 `common_defaults` / `item_defaults` 配置（不变）。

该模型的核心变更是默认行为从 Strict 改为 Nullable：算子在未声明 strict 的情况下，收到 nil 值时不再报错，而是将 nil 透传。这使得"字段存在但值为空"成为合法的业务语义，减少了不必要的运行时错误。

各运行时的 strict opt-in 字段在 JSON 配置中的位置：
- `strict_common`：列在此列表中的 common 字段走 Strict 模式
- `strict_item`：列在此列表中的 item 字段走 Strict 模式

## Operator-level debug 三态继承

各运行时的逐算子 `debug` 配置采用 nullable 三态语义：

- Go: `*bool`
- Java: `Boolean`（装箱类型）
- Python: `Optional[bool]`
- C++: `std::optional<bool>`

未设置时（nil/null/None/nullopt），算子继承 flow-level 全局 debug 设置；显式设置 `true` 或 `false` 时，覆盖全局值。这替代了旧版"全局 debug 单向传播覆写所有算子"的行为，使单个算子可以在全局 debug 开启时显式关闭自身的 debug，或在全局 debug 关闭时单独开启。

## ExecutionError/PanicError 必须保留 cause chain

各运行时的 `ExecutionError` / `PanicError` 在包装内层异常时，必须保留 inner exception 的对象引用以支持沿链下钻：

- pine-go: `Err error` 字段 + `Unwrap() error`，通过 `errors.Is/As` 解包
- pine-java: 构造时 `super(msg, cause)`，通过 `Throwable.getCause()` + `instanceof` 解包
- pine-python: 显式 `self.__cause__ = cause`，通过 `isinstance(e.__cause__, T)` 解包
- pine-cpp: 多继承 `std::nested_exception` + `std::throw_with_nested` 重抛，通过 `pine::error_as<T>()` helper 走链（注意 helper 须先检查 `nested_ptr() != nullptr`，避开标准 `std::rethrow_if_nested` 的 footgun）

理由：cause chain 属于"语言层 API 形态对等"的一部分，虽然不在 HTTP /execute 返回 JSON 中可见（已被 string flatten），但下游用户对该能力有预期。Section 15（`15-error-cause-chain.sh`）通过四方 probe binary stdout 字节级一致验证（`PASS:key=user:42 not found`）。

## 错误类型分类约定(ConfigError / ValidationError / RegistryError)

各运行时在 init / build / dispatch 阶段抛出的错误类型必须按以下边界统一:

- **ConfigError**:配置文件结构错(缺字段、空 operators、JSON parse、name 冲突)。`pine: config error: ...` 前缀。
- **ValidationError**:语义错(`data_parallel < 0`、`skip` 字段非 `_` 开头、`skip` 字段未出现在 `common_input`、forward source reference、resource_name 引用未注册资源)。`pine: validation error: ...` 前缀。
- **RegistryError**:算子注册系统错(未知算子类型、参数 schema 校验失败、Init 失败 wrap、参数语义违反如 `top_n` 必须为数字 / `unsupported order`)。`pine: registry error [<op>]: ...` 前缀。

**Section 5 (error-parity) 不强制 enforce type 字段**(只用 `message_contains` 子串匹配),所以历史上各运行时分类略有 ad-hoc。本约定的目的是给未来贡献者一个边界判断标准,避免再扩大漂移。reviewer P1-O1 审计现状大致已符合此分类(C++/Java/Python 都把 init-time wrap 走 RegistryError,跟 Go `BuildOperator` 一致;ValidationError 用于明确"语义不变量")。

边角案例 — `additive_writes_row_set + mutates_row_set` 冲突:Go 端原是 `fmt.Errorf` plain error;C++/Java/Python 当前用 RegistryError。后续如需统一,选 ValidationError(语义边界冲突)。
