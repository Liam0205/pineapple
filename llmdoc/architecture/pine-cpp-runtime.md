# Pine-C++ 运行时架构

本文档记录 Pineapple 第四运行时 pine-cpp 的定位、契约目标、MVP 边界和当前确定的关键设计决策。

## 适用范围

当任务涉及以下内容时优先阅读本文档：

- `pine-cpp/` 目录内的实现
- Pineapple 第四运行时的架构、性能与可维护性取舍
- C++ 端的 CLI / render_dag / server 入口
- 多运行时 parity 的 fixture / golden / 错误输出契约

## 定位

pine-cpp 的目标不是“再做一个能跑的第四运行时”，而是成为 **在完全对等前提下的标杆实现**。

这意味着它必须同时满足两类要求：

1. **工程要求**
   - 与现有运行时在功能、错误处理行为、错误包装方式、最终报错文案上完全对等
   - 最终完整接入 cross-validate
   - 代码组织与测试分层可长期维护，而不是靠大量特判堆出来

2. **实现上限要求**
   - 在列存、字符串存储、Lua bridge、COW、arena、并行调度等热路径上追求更高实现质量
   - 反过来为 Go / Java / Python 运行时提供可借鉴的设计参考

## 对等契约

### 错误处理必须字节级一致

对 pine-cpp 来说，错误输出不是“语义差不多即可”的内部实现细节，而是外部契约的一部分。

需要对齐的维度包括：

- 是否报错
- 在哪个阶段报错（config load / DAG compile / execute）
- 错误类别
- 错误包装方式
- 最终错误消息文本
- Lua 抛错时的外层包装与上下文格式
- JSON number / int / float 边界导致的报错差异

### 标准优先级：fixture 第一，Go 次之，人工裁决用于收敛

pine-cpp 的行为基准按以下顺序确定：

1. **fixtures / golden expectations**
2. **Go 运行时**（历史主参考实现）
3. **人工裁决**（用于发现分歧后的收敛）

理想状态下，这三者不应长期并存多个真相来源，而应持续收敛为同一套标准。

### Schema / 配置解析严格复刻现有行为

pine-cpp 不单独“变聪明”。在以下方面优先复刻现有对外行为：

- 未知字段是否忽略
- 缺省值如何补齐
- 空字符串 / `null` / 缺失字段如何区分
- `int64` / `float64` / JSON number 的边界处理
- 错误发生阶段与报错路径

如果未来要统一调整规则，应四个运行时一起改，并同步更新 fixtures。

## 当前已实现能力

pine-cpp 已超过原计划的 MVP 边界，目前作为完整的第四运行时存在。已稳定落地的能力：

- **CLI 入口**
  - `pineapple-cpp-run -config ... -request ... [-static-resources ...]`
  - `pineapple-cpp-render-dag -config ... -format dot|mermaid [-collapse N]`
  - `pineapple-cpp-server -config ... [-addr :8080] [-read-header-timeout 10s] [-read-timeout 30s] [-write-timeout 60s] [-idle-timeout 120s] [-max-body-size 10485760]`
  - CLI stderr 错误前缀（`error reading config:` / `error creating engine:` / `execution error:` 等）与 pine-go 字节级一致
- **HTTP server**
  - `/health`、`/execute`、`/stats`、`/dag` 端点
  - 配置 mtime 监听 + 原子替换实现热加载
  - graceful shutdown：`Server::stop()` 在停止接受新连接后等待 in-flight 请求计数归零，5s 超时
  - 双重边界 body 读取：Content-Length 与 `max_request_body_size+1` hard cap 同时生效；malformed Content-Length 直接拒绝
- **Engine / 调度**
  - per-node thread DAG 调度，节点完成时 fan-out 后继
  - `Engine::peak_concurrency()` 通过 atomic CAS 追踪累积峰值，序列化到 `/stats.scheduler.peak_concurrency`
  - debug snapshot + `StartTime` 在 `OpTrace` 中可选记录
  - `data_parallel` transform 在固定线程池中分片执行
- **数据表示**
  - 强类型 `Column` 抽象（Int64/Double/String/Bool）+ JsonColumn 退路、validity bitmap
  - `ColumnStore` 接口（默认 `TypedColumnStore`），保留 Arrow-backed 实现的扩展点
  - `ColumnFrame` 为请求级 DataFrame，内部使用 `shared_mutex` 自治并发；canonical 5-stage write log（common writes / item writes / removals / reorder / additions）
  - `OperatorOutput` write-log 模式：算子只声明写入意图，由 frame 应用，便于 trace/debug
- **算子框架**
  - `pine::Operator` 基类（`init` + `execute(const ColumnFrame&, OperatorOutput&)`）
  - marker 类型 `ConsumesRowSet` / `MutatesRowSet` / `AdditiveWritesRowSet` / `ConcurrentSafe`
  - `register_operator(schema, factory)` API + `PINE_REGISTER_OPERATOR` 宏（static init 注册）
  - 17 个内置算子按 category 拆分到 `operators/<category>/<name>.cpp`，统一通过宏注册
  - warning operator-name 前缀（`{op_name}: ...`）由框架在 `apply_output` 阶段统一加上
- **错误体系**
  - `ConfigError` / `ValidationError` / `RegistryError` 在构造时自动加 `pine: <kind> error: ...` 前缀，与 pine-go `types/errors.go` 字节级一致
  - `RegistryError` 支持 `(operator_name, msg)` 双参形态对齐 Go 的 `RegistryError.Operator` 字段
  - `PanicError` + dispatch recovery 把意外异常映射为可观察的算子错误
  - **ExecutionError 双参/单参 + engine 层 promote**(P1-D1):`ExecutionError(op, inner)` 双参构造直接拼 `pine: execution error in operator "X": <inner>`；算子级 throw 站点可以用单参 `ExecutionError(msg)`(`operator_name()` 返回空,`inner()` 即 msg),由 `dispatch_with_recovery` (engine.cpp) 捕获后用 `std::throw_with_nested(ExecutionError(op.name, e.inner()))` 重抛,前缀在 engine 层统一加,所以最终 `what()` 仍与 pine-go 字节级一致。**算子不需要重复拼前缀**。
  - **Cause chain 支持**:`ExecutionError` 与 `PanicError` 多继承 `std::nested_exception`,`dispatch_with_recovery` 用 `std::throw_with_nested` 重抛保留 inner cause。`include/pine/error_chain.hpp` 提供 `pine::error_as<T>(const std::exception&)` / `pine::error_is<T>(const std::exception&)` 模板 helper,沿 nested 链下钻,对偶 Go `errors.As` / Java `Throwable.getCause()` / Python `__cause__`。注意:helper 内部显式检查 `nested_ptr() != nullptr` 后才调用 `rethrow_nested()`,避开标准 `std::rethrow_if_nested` 在 null 时 `std::terminate` 的 footgun
- **可插拔观测与扩展**
  - `pine::metrics::Provider` / `Counter` / `Gauge` / `Histogram` / `NopProvider`（对齐 pine-go `pkg/metrics`）
  - `MetricsAware` 接口（`Engine` 预创建后自动注入）与 `StatsProvider` 接口（向 `/stats` 暴露算子内部计数）
  - `EngineOptions::metrics_provider` 注入；未设置时回退到 `metrics::nop_provider()`
  - `ServerConfig::middlewares`：outer-to-inner 调用链 + `MiddlewareContext`（method / path / normalized_path / request_bytes / status）
  - `pine::server::http_metrics_middleware(provider)` 工厂返回内置 HTTP 指标 middleware，指标名与桶与 pine-go `pkg/server/http_metrics.go` 一致。`Server::run()` 现在**无条件** `push_back(http_metrics_middleware(provider, http_stats_.get()))`，不再需要用户显式注入。当 `ServerConfig::metrics_provider` 为 nullptr 时自动 tie-off 至 `metrics::nop_provider()`。`HttpStats` 累加器（`src/server/http_stats.{hpp,cpp}`）是 middleware 第二写入路径，数据由 `handle_stats()` 序列化为 `"http"` 子树
  - `pine::resource::Manager` + `FetcherFactory` + `register_fetcher_factory(type, factory)` 与 pine-go `pkg/resource` 等价：后台刷新线程、`snapshot()`、可重入 `stop()`
  - `transform_by_remote_pineapple` 算子：基于 `libcurl` 实现 SSRF 安全保护（拦截 loopback/private IP 配置）、HTTP POST 超时与最大体积限制
- **根级配置扩展**
  - `log_prefix`（同时支持 `EngineOptions::log_prefix` 覆盖），最终通过 `log::SetPrefix` + `Ldate|Ltime|Lshortfile` 应用
  - `_PINEAPPLE_VERSION` / `_PINEAPPLE_CREATE_TIME` 解析并通过 `Config` 暴露，供下游工具读取
  - `resource_config` 解析为 `ResourceEntry`（type / interval / params），由 `Manager::load_from_config(...)` 解析为活跃 fetcher
- **校验**
  - skip 字段必须出现在 `$metadata.common_input` 中，否则在配置加载期报 ValidationError

cross-validate 接入范围以 `scripts/cross-validate/` 目录为准，目前 codegen-schema / render-dag / execution / column-store / error / server-http / cancellation / concurrent / raw-byte / hot-reload / extensibility-parity / metrics-parity 等多个 section 通过 `CPP_RUN` / `CPP_DAG` / `CPP_SERVER` / `CPP_CODEGEN` 环境变量条件性包含 pine-cpp。具体覆盖以脚本为准，避免在文档中硬编码层数。

## 关键设计决策

### 1. 对外接口

- 先提供 **C++ API**
- public API 保持窄而简单，为未来增加 **C ABI** 留出空间
- 不把复杂模板与 STL 容器直接暴露为长期稳定的 ABI 契约

### 2. 构建与语言标准

- **CMake**
- 目标标准：**C++23**
- 错误返回采用 `std::expected<T, E>`；若目标编译器过旧，可用 `tl::expected` 作为兼容层
- JSON 库选择 **nlohmann/json**（边界层读写一体，JSON 不在主要热路径）

### 3. Lua 集成

- 选择 **LuaJIT**
- 保持 Lua 5.1 语义，与现有脚本公共子集兼容
- 重点收益来自 tracing JIT 与更低解释器分发成本
- `StatePool` 提供按需 `LuaVM` 分配与借用，维护 baseline globals 快照并在释放时清理变异；实现 `StatsProvider`（`/stats` 接口暴露 pool 计数）和 `MetricsAware`（注入外部提供商）

### 4. 内存与对象生命周期

- **Arena + RAII 分层共存**
- 长生命周期对象（Engine、配置、LuaJIT state）由 RAII 管理
- 请求执行期间的中间对象以 arena 分配为主
- 对象 API 与分配策略解耦，采用接近 Protobuf Arena 的模式

### 5. DataFrame 与数据表示

- DataFrame 内部采用 **强类型列**，而不是 cell-level 动态 variant
- 动态值 `Value` 只存在于边界层：JSON 解析、Lua 交互、operator config、最终 JSON 序列化
- nullable 表示使用 **`Column<T> + validity bitmap`**
- 字符串底层采用 **arena/string pool** 持有，读取接口暴露 `std::string_view`

### 6. 存储模式语义

- JSON / DSL 层继续接受 `storage_mode: row | column`
- 默认值与现有运行时保持一致（`"row"`）
- **MVP 阶段内部统一采用列式执行表示**
- 也就是说：逻辑上支持 row/column，**物理 RowStorage 在 MVP 阶段尚未实现**

### 7. COW 与 Operator 交互

- 初期采用 **列级 COW**
- `shared_ptr<const Column>` 共享列，写时 clone 单列
- C++ 侧依靠 `const` 约束避免绕过 COW 直接写共享列
- 后续如需要进一步压榨性能，可在算子注册层补 `InputMode`（ReadOnly / Mutating）声明

### 8. 并行执行模型

- 固定大小线程池
- DAG 分支并行与算子内数据分片共用同一个 pool
- 不依赖 C++20 coroutine runtime；当前阶段优先成熟、可控、易调试的线程池方案

### 9. 扩展机制

- 与现有运行时一致，采用 **编译时注册**（schema + factory）
- C++ 侧通过 `pine::register_operator(schema, factory)` 与 `PINE_REGISTER_OPERATOR(SCHEMA, FACTORY)` 宏完成 static init 注册；每个内置算子位于 `operators/<category>/<name>.cpp`
- 业务侧资源 fetcher 通过 `pine::resource::register_fetcher_factory(type_name, factory)` 注册，`Manager::load_from_config(...)` 据此实例化
- Lua 作为轻量级脚本扩展口
- 不在第一阶段引入 `.so/.dll` 动态插件 ABI

### 10. 代码组织

推荐结构贴近 pine-go 的模块边界，但采用 C++ 生态更自然的工程壳子：

- `include/pine/` — 对外 API
- `src/config/`
- `src/dag/`
- `src/dataframe/`
- `src/registry/`
- `src/runtime/`
- `src/render/`
- `src/lua/`
- `operators/<category>/`
- `server/`
- `cmd/pineapple-run/`、`cmd/pineapple-render-dag/`、`cmd/pineapple-server/`

核心原则：按 **解析 → 编译 → 执行 → 渲染 → 对外入口** 的生命周期拆模块，而不是一开始就过度抽象出大量“接口层”。

## 测试策略

pine-cpp 采用 **三层测试 + sanitizer**，在 CI 中由四个独立 job 覆盖：

- `cpp-build` — Release 构建，产出 4 个可执行文件
- `cpp-sanitizer` — ASan/UBSan smoke 用例
- `cpp-lint` — `-Werror` 严格构建 + 基础卫生检查
- `cpp-test` — doctest 单测套件

测试分层：

1. **单元测试**
   - config 解析
   - sequence expansion / DAG build
   - Column / ColumnStore / ColumnFrame / validity bitmap
   - render_dag 输出
   - registry 注册与查找
   - metrics nop provider 与 HTTP middleware
   - resource Manager 后台刷新

2. **集成测试**
   - engine execute 全链路
   - 内置 operator 组合
   - DAG 分支并行 / 算子内分片
   - row/column 语义一致性
   - `/execute`、`/stats`、`/dag`、`/health` HTTP 端点

3. **cross-validate / E2E**
   - 最终权威标准
   - 接入范围以 `scripts/cross-validate/` 中各 section 对 `CPP_RUN` / `CPP_DAG` / `CPP_SERVER` / `CPP_CODEGEN` 的引用为准
   - 判定 pine-cpp 的外部可观察行为是否已与其他运行时完全一致

4. **sanitizer**
   - ASan
   - UBSan
   - TSan

C++ 端的内存与并发错误不属于”锦上添花”的测试维度，而属于正确性本身。

## 性能工作优先级

性能优化的顺序应保持务实：

1. **先保证 parity**
2. **再优化执行热路径**
   - typed columns
   - LuaJIT bridge
   - string storage / string pool
   - COW
   - validity bitmap
   - arena
3. **再优化并行调度**
4. **最后才看 JSON / render / server 边界层**

原因是 Pineapple 的主要成本几乎肯定在 operator 执行、Lua 表达式求值和 DataFrame 操作上，而不在配置解析和 HTTP 壳子上。

## 实施时的推荐阅读顺序

开始实现 pine-cpp 前，建议结合以下文档一起读：

1. 本文档
2. `llmdoc/architecture/dag-engine.md`
3. `llmdoc/architecture/apple-compiler.md`
4. `llmdoc/guides/cross-layer-validation.md`
5. `llmdoc/reference/operator-contract.md`

pine-cpp 不应成为脱离现有契约的“新项目”；它是对现有 JSON 契约、多运行时 parity 体系和 fixture 财富的延续。