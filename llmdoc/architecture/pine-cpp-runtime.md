# Pine-C++ 运行时架构

本文档记录 Pineapple 运行时之一 pine-cpp 的定位、契约目标和当前确定的关键设计决策。

## 适用范围

当任务涉及以下内容时优先阅读本文档：

- `pine-cpp/` 目录内的实现
- Pineapple C++ 运行时的架构、性能与可维护性取舍
- C++ 端的 CLI / render_dag / server 入口
- 多运行时 parity 的 fixture / golden / 错误输出契约

## 定位

pine-cpp 的目标不是“再做一个能跑的运行时”，而是成为 **在完全对等前提下的标杆实现**。

这意味着它必须同时满足两类要求：

1. **工程要求**
   - 与现有运行时在功能、错误处理行为、错误包装方式、最终报错文案上完全对等
   - 最终完整接入 cross-validate
   - 代码组织与测试分层可长期维护，而不是靠大量特判堆出来

2. **实现上限要求**
   - 在列存、字符串存储、Lua bridge、COW、arena、并行调度等热路径上追求更高实现质量
   - 反过来为 Go / Java 运行时提供可借鉴的设计参考

## 对等契约

### 错误处理必须字节级一致

对 pine-cpp 来说，错误输出不是“语义差不多即可”的内部实现细节，而是外部契约的一部分。

需要对齐的维度包括：

- 是否报错
- 在哪个阶段报错（config load / DAG compile / execute）
- 同阶段内多个违反同时发生时，首个报错的判定顺序（如 common 先于 item）
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

如果未来要统一调整规则，应各运行时一起改，并同步更新 fixtures。

## 当前已实现能力

pine-cpp 已超过原计划的 MVP 边界，目前作为完整的运行时存在。已稳定落地的能力：

- **CLI 入口**
  - `pineapple-cpp-run -config ... -request ... [-static-resources ...]`
  - `pineapple-cpp-render-dag -config ... -format dot|mermaid [-collapse N]`
  - `pineapple-cpp-server -config ... [-addr :8080] [-read-timeout 30s] [-write-timeout 60s] [-max-body-size 10485760]`
  - CLI stderr 错误前缀（`error reading config:` / `error creating engine:` / `execution error:` 等）与 pine-go 字节级一致
- **HTTP server**
  - `/health`、`/execute`、`/stats`、`/dag` 端点
  - 配置 mtime 监听 + 原子替换实现热加载
  - graceful shutdown：`Server::stop()` 在停止接受新连接后等待 in-flight 请求计数归零，5s 超时
  - **HTTP/1.1 keep-alive**（R3-L9b）：连接处理 while-loop 复用 socket；`Connection: keep-alive/close` 头由 `MiddlewareContext.keep_alive` 控制（HTTP/1.1 默认 keep-alive，HTTP/1.0 默认 close）。`-idle-timeout` 控制 keep-alive 连接两次请求间的空闲超时
  - **read-header-timeout**（R3-L9a）：`read_http_request` 入参增 `header_timeout` / `body_timeout`；header 阶段 `SO_RCVTIMEO` 短窗口防 Slowloris，`\r\n\r\n` 边界后 swap 为 body_timeout 长窗口
  - **客户端断连取消**（R10-2）：`execute_with_trace` 接收 `client_fd`，spawn watcher 线程 `poll()` 同时监听 client fd（`POLLRDHUP|POLLHUP|POLLERR`）和 `eventfd` wakeup fd，`poll` 超时设为 `-1`（无限等待），实现零延迟唤醒。请求完成后主线程 `eventfd_write` 通知 watcher 退出，替代原 100ms 轮询超时方案
  - 双重边界 body 读取：Content-Length 与 `max_request_body_size+1` hard cap 同时生效；malformed Content-Length 直接拒绝
- **Engine / 调度**
  - **Ready-queue DAG 调度器**（v0.9.2）：替代原 per-node `std::thread` 方案，使用双隔离线程池架构：
    - **DAG pool**（默认 `nproc * 4`）：负责 DAG 节点调度，只有前驱全部完成的节点才被提交到池中，池中不存在阻塞任务
    - **Shard pool**（默认 `nproc * 2`）：负责 `data_parallel` 分片执行
    - **In-degree 追踪**：`std::atomic<int>[]` 数组，每个节点初始值为前驱数量；前驱完成时 `fetch_sub(1)` 递减后继 in-degree，归零即提交
    - **Completion latch**：`std::atomic<size_t> remaining` + `condition_variable` 等待所有节点完成
    - **Seed loop 安全**：根节点识别使用不可变的 `graph.nodes[i].preds.empty()`（而非可变的 `in_degree[i]`），避免 pool worker 已递减非根节点 in-degree 导致的竞态
    - 可通过 `EngineOptions::dag_pool_size` / `EngineOptions::shard_pool_size` 配置，CLI 对应 `-dag-pool-size` / `-shard-pool-size`
  - C++20 `std::stop_token` + `std::stop_callback` 协作取消
  - **外部取消 API**（R3-H3）：`Engine::execute` / `execute_traced_into` 接受可选 `std::stop_token external_cancel`；run_dag 通过 `std::stop_callback` 桥接到内部 `cancel_source`
  - **shard 级取消**（R3-M3）：parallel_execute 首个 shard 失败后 `atomic<bool> shard_cancel` 阻止后续 shard 启动
  - `Engine::peak_concurrency()` 通过 atomic CAS 追踪累积峰值，序列化到 `/stats.scheduler.peak_concurrency`
  - debug snapshot + `StartTime` 在 `OpTrace` 中可选记录
  - `data_parallel` transform 在固定线程池中分片执行；分片通过 `ColumnFrame::make_window_view` 零拷贝窗口视图实现——shard 共享 parent frame 的 ColumnStore，以 `(offset, count)` 做行级翻译，避免逐行物化。window view 是只读投影，所有写方法（`set_common` / `push_warning` / `apply_output` / `to_result`）均 throw `Error`
- **数据表示**
  - **`Variant`**（原名 `JsonValue`，PERF-18 重命名）：通用值类型，`Variant::object_t` = `FlatMap<std::string, Variant>`（sorted vector，PERF-9 从 `std::map` → `unordered_map` → `FlatMap` 演进）。`FlatMap` 在小 N（典型 operator 字段数 < 20）下比 hash map 更快（cache-friendly linear scan + 无 hash 开销）
  - 强类型 `Column` 抽象（Int64/Double/String/Bool）+ JsonColumn 退路、validity bitmap
  - `ColumnStore` 接口（默认 `TypedColumnStore`，内部 `FlatMap<string, unique_ptr<Column>>`），保留 Arrow-backed 实现的扩展点
  - **`Frame` 抽象基类**（`include/pine/frame.hpp`，R3-L3）：`ColumnFrame` 和 `RowFrame` 两个物理实现。`Operator::execute` 签名为 `const Frame&`（不绑定具体实现）。`pine::make_frame(storage_mode, common, items)` 工厂按 `Config.storage_mode` 路由（`"row"` → RowFrame，其他 → ColumnFrame）
  - `ColumnFrame` 为请求级 DataFrame，内部使用 `shared_mutex` 自治并发；canonical 5-stage write log（common writes / item writes / removals / reorder / additions）。NaN/Inf 在 apply_output 三阶段校验（R3-H2）。**行删除使用 bitmap 查找**（PERF-16）替代线性扫描
  - `RowFrame` 为行存 DataFrame，items 以 `vector<FlatMap<string, Variant>>` 存储，同样内部 `shared_mutex` + 5-stage write log + NaN/Inf 校验。适用于逐行访问密集场景（Lua snapshot、remote request、observe logging）
  - **Frame 锁形态：per-call `std::shared_mutex`**——每次 `item()` / `common()` 内部自取锁，与 pine-go（`sync.RWMutex` per-call）和 pine-java（`ReentrantReadWriteLock` per-call）完全镜像。这是 2026-06 锁优化战役的最终决策：曾实现 dispatch 级 hoist（单 op 一次锁窗口，calibrated +4%）但为跨运行时锁形态对齐而 revert（commit `3c87bd6`）；历史方案与数据见 git log `9f7db78` / `a7d3b31` 与 `.code-review/sharedmutex-deep-dive/analysis.md`
  - `include/pine/shared_mutex.hpp`：`pine::SharedMutex`，Go `sync.RWMutex` 协议的 C++ port（fetch_add + 负数宣告 + semaphore），单次拿放 10.14ns 超 Go 的 13.75ns（该组件的设计验收标准），10 doctest + TSan 验证。**当前 Frame 不使用它**：锁在 calibrated 负载上仅占 ~2% CPU，切换收益（0.4-0.9 个百分点）低于二进制布局噪声。它是已验证备件——当出现锁占比 >5% 的负载时，一行替换 typedef 即可启用
  - `OperatorOutput` write-log 模式：算子只声明写入意图，由 frame 应用，便于 trace/debug。`item_writes_` 内部为 `vector<ItemWrite>`（`struct ItemWrite { int index; std::string field; Variant value; }`），`set_item` 为 O(1) 摊销 push_back；`apply_output` 按顺序重放（last-write-wins 语义），`snapshot_output` 显式 group 到 map 保持最终状态视图
  - **ValidateOutput 类型约束**（R3-H1）：每个算子 execute 后、apply_output 前，按 operator_type 检查输出方法是否合法（如 Recall 不能 SetItem/RemoveItem/SetItemOrder，只能 AddItem；但 **Recall 可以 SetCommon**——写请求级 common 状态是正常的 mutating hazard，DAG 已按 CommonOutput 构建 RAW/WAW/WAR 边），违规抛 ExecutionError `"type violation: operator type X must not call [Method1 Method2]"`
- **算子输入投影层**
  - **`OperatorInput`**（`include/pine/operator_input.hpp`）：Frame + InputFieldSpec 之上的 lazy read-only proxy。算子签名为 `execute(const OperatorInput&, OperatorOutput&)`，通过 `common(field)` / `item(i, field)` 按需读取，自动替换 defaulted 字段。避免旧实现的 O(N×M) eager reify（逐 item×field 预复制到 `vector<map>`）。**`item_count` 在构造时缓存**，避免每次调用获取 shared_lock
  - `build_operator_input(frame, op_name, spec)` 工厂：按 `strict_common → nullable_common → strict_item → nullable_item`（先 common 后 item）顺序校验输入字段，通过后构造 lazy proxy。校验分两个 `with_read_lock` 窗口完成——窗口1 做 strict + nullable common；`validate_strict_items` 因 `shared_mutex` 非递归（不能嵌套取锁）必须在窗口外独立取锁，排在 common 之后、nullable item 之前；窗口2 做 nullable item（热路径 N×M 仍折叠进单个 lock 窗口）。**该顺序是字节级错误对等契约的一部分**：当一个 config 同时违反 common 与 strict item 时，三运行时必须抛出同一个"首个错误"（common 先于 item），不可被锁/批量化优化隐式改写（参见 `memory/reflections/review-driven-build-input-error-ordering.md`）
  - `Frame::validate_strict_items(fields)` 虚方法：返回 `pair<bad_field, bad_row>`（无 op_name 参数）；ColumnFrame/RowFrame 各自实现最优路径的批量 strict item 检查，避免逐字段逐行的 O(N×M×F) 调用
- **算子框架**
  - `pine::Operator` 基类（`init` + `execute(const OperatorInput&, OperatorOutput&)`）
  - marker 类型 `ConsumesRowSet` / `MutatesRowSet` / `AdditiveWritesRowSet` / `ConcurrentSafe`
  - `OperatorTraits<T>` 编译期 `std::is_base_of_v` 标记检查
  - `register_operator_with_traits(schema, factory, ...)` 底层 API + `register_operator_typed<T>(schema)` 模板 + **`PINE_REGISTER_OPERATOR_T(Type, schema)` 宏**——通过 `OperatorTraits<T>` 在编译期解析标记位，注册时不调用 factory，重量级构造器（Lua pool、libcurl handle、redis pool seed）只在 per-Engine 实例化时付成本
  - 内置算子均使用 `PINE_REGISTER_OPERATOR_T`，按 category 拆分到 `operators/<category>/<name>.cpp`（具体清单见 `pine-cpp/CMakeLists.txt`）。Benchmark stub 算子（`transform_bench_cpu` / `transform_bench_sleep` 等 9 个）仅供性能测试。Bench stub 使用 iteration-based 校准模式 + 正态分布延迟模拟，确保跨运行时可比性
  - `merge_dedup`：与 Go（map）/ Java（LinkedHashSet）一致用 hash set 去重（`unordered_set`）。历史教训：曾是 vector 线性扫描 O(N²)，large_5000（5000 行）上慢 Go 10 倍，`bd3fb75` 修复
  - `observe_log` 完整实现（R3-L8）：init 读取 metadata 字段列表和 `log_prefix`，execute 构造 `{common, items}` snapshot 后 **RapidJSON Writer** 紧凑输出到 stderr（PERF-15 从手写 `dump_json` 迁移到 RapidJSON，消除 22.4% 序列化热点）。紧凑模式抑制所有 `\n` / 缩进 / 冒号后空格，与 Go `json.Marshal` 格式对齐
  - `[pine-debug]` stderr 日志（R3-L6）：debug=true 的算子在 execute 后、apply_output 前输出 `operator/duration/input_size/output_size/input/output` 单行日志
  - `inline constexpr const char* kVersion`（R3-L2）：编译期版本常量，对齐 Go `const Version`，由 `scripts/bump-version.sh` 同步
  - warning operator-name 前缀（`{op_name}: ...`）由框架在 `apply_output` 阶段统一加上
- **错误体系**
  - `ConfigError` / `ValidationError` / `RegistryError` 在构造时自动加 `pine: <kind> error: ...` 前缀，与 pine-go `types/errors.go` 字节级一致
  - `RegistryError` 支持 `(operator_name, msg)` 双参形态对齐 Go 的 `RegistryError.Operator` 字段
  - `PanicError` + dispatch recovery 把意外异常映射为可观察的算子错误
  - **PanicError.stack() + detailed_error()**（R3-L1）：C++23 `std::stacktrace` 在 PanicError 构造时捕获当前线程帧栈，`detailed_error()` 返回 `"pine: panic in operator \"X\": Y\nstack trace:\n<frames>"` 格式，对偶 Go `PanicError.DetailedError()`。CMake 探测 `libstdc++exp` / `libstdc++_libbacktrace`，缺失时退化为空 stack。需 `-g` 才能解析文件/行号
  - **ExecutionError 双参/单参 + engine 层 promote**(P1-D1):`ExecutionError(op, inner)` 双参构造直接拼 `pine: execution error in operator "X": <inner>`；算子级 throw 站点可以用单参 `ExecutionError(msg)`(`operator_name()` 返回空,`inner()` 即 msg),由 `dispatch_with_recovery` (engine.cpp) 捕获后用 `std::throw_with_nested(ExecutionError(op.name, e.inner()))` 重抛,前缀在 engine 层统一加,所以最终 `what()` 仍与 pine-go 字节级一致。**算子不需要重复拼前缀**。
  - **Cause chain 支持**:`ExecutionError` 与 `PanicError` 多继承 `std::nested_exception`,`dispatch_with_recovery` 用 `std::throw_with_nested` 重抛保留 inner cause。`include/pine/error_chain.hpp` 提供 `pine::error_as<T>(const std::exception&)` / `pine::error_is<T>(const std::exception&)` 模板 helper,沿 nested 链下钻,对偶 Go `errors.As` / Java `Throwable.getCause()`。注意:helper 内部显式检查 `nested_ptr() != nullptr` 后才调用 `rethrow_nested()`,避开标准 `std::rethrow_if_nested` 在 null 时 `std::terminate` 的 footgun
- **Codegen 入口**
  - `pineapple-cpp-codegen -schema-json <out>`：从 C++ Registry 导出算子 schema JSON（与 Go/Java schema JSON 在结构维度对齐，CI cross-validate `01-codegen-schema.sh` 1b 校验）
  - `pineapple-cpp-codegen -output <dir>`：发射完整 Apple DSL 产物集（`operators.py` / `__init__.py` / `markers.py` / `resources.py` / `resources_init.py`），与 Go / Java 的 `apple_generated/` 输出 **byte-equal**。Python 字面量格式化由 `python_escape` / `format_g` / `python_literal` / `python_type` / `python_default_for_type` / `camel_case` 一组本地 helper 完成，`format_g` 复刻 Go `fmt.Sprintf("%g")` 的 shortest-round-trippable 语义并对齐 Java `GoFormat.formatG`（PERF：对 |d| > LLONG_MAX 增加 isfinite + 范围 guard，避免 `static_cast<long long>` UB，scope 注释明确小整数 / -0.0 / NaN 边界已覆盖，未来若引入非平凡 double 默认值需路由到 Ryu / Grisu）
  - `pineapple-cpp-codegen -doc-dir <dir>`（0.10.10 起）：发射算子文档 markdown（`doc/operators/<operator_name>.md` 三段：Parameters / Metadata Contract / DSL Usage + 顶层 `README.md`），与 pine-go 的 `pkg/codegen` doc 输出**字节级一致**。pine-cpp 没有源码注释解析能力（无类似 Go go/parser、Java JavaDoc parser），metadata contract 通过 `OperatorSchema.metadata` 字段以 designated initializer 显式声明（每个内置算子均已 inline 声明），由 cross-validate `01-codegen-schema.sh` 1e 节 Go-vs-cpp markdown byte-equal gate 长期锁定
  - `markers.py` 通过对每个已注册算子调用 factory 一次、`dynamic_cast` 探测 `ConsumesRowSet` / `MutatesRowSet` / `AdditiveWritesRowSet` 标记基类生成；与 Go / Java 一样**不调用 `init` 或 `configure`**，重量级构造器（Lua pool seed、libcurl handle、redis pool）在 codegen 时不应付出运行成本，也禁止在构造函数中做 I/O 或起线程
  - `ResourceSchema` 全局注册表（`include/pine/resource.hpp` / `src/resource/resource.cpp`）：`register_resource_schema(...)` / `all_resource_schemas()` 由算子自注册时调用，供 codegen 发射 `resources.py` / `resources_init.py`。`redis_connection` 等句柄型资源算子在 static init 中同时注册 fetcher factory 与 ResourceSchema（含 name / default_interval / 多个 ParamSchema），对齐 Go `types.ResourceSchema` 与 Java `Registry`
  - **registry reset API 拆分**：`reset_resource_schema_registry()` 现在只清 schema 表（命名一致），`reset_all_resource_registries()` 是同时清 schema + fetcher factory 的新组合 API。测试若只想重置 schema 不应误清 fetcher factory；想统一拆除两个表用 `reset_all_resource_registries`
- **可插拔观测与扩展**
  - `pine::metrics::Provider` / `Counter` / `Gauge` / `Histogram` / `NopProvider`（对齐 pine-go `pkg/metrics`）
  - `MetricsAware` 接口（`Engine` 预创建后自动注入）与 `StatsProvider` 接口（向 `/stats` 暴露算子内部计数）
  - `Closer` 接口（`include/pine/operator.hpp`，0.9.7）：算子可选实现 `void close()`，由 `Engine::close()` 在引擎退役时遍历触发；`Server::stop()` 与 hot-reload 的旧 engine swap 都在锁外调用，对齐 pine-go/pine-java 的 teardown 契约。`LuaPool::close()` 翻转 `closed_` 原子标志，已发出的 `LuaVM` 由 RAII 销毁链回收
  - `EngineOptions::metrics_provider` 注入；未设置时回退到 `metrics::nop_provider()`
  - `ServerConfig::middlewares`：outer-to-inner 调用链 + `MiddlewareContext`（method / path / normalized_path / request_bytes / status）
  - `pine::server::http_metrics_middleware(provider)` 工厂返回内置 HTTP 指标 middleware，指标名与桶与 pine-go `pkg/server/http_metrics.go` 一致。`Server::run()` 现在**无条件** `push_back(http_metrics_middleware(provider, http_stats_.get()))`，不再需要用户显式注入。当 `ServerConfig::metrics_provider` 为 nullptr 时自动 tie-off 至 `metrics::nop_provider()`。`HttpStats` 累加器（`src/server/http_stats.{hpp,cpp}`）是 middleware 第二写入路径，数据由 `handle_stats()` 序列化为 `"http"` 子树
  - `pine::resource::Manager` + `FetcherFactory` + `register_fetcher_factory(type, factory)` 与 pine-go `pkg/resource` 等价：后台刷新线程、`snapshot()`、可重入 `stop()`。`ResourceValue` 是**数据 `Variant` 与句柄 `shared_ptr<void>` 二选一**（XOR）的载体：数据型资源走 `snapshot()` 整体导出活值，句柄型资源走 `ResourceProvider::borrow(name)` 借出长生命周期句柄（如 `redis_connection`）。退休时 `shared_ptr` RAII 析构 + `engine_mu_` 锁序保证零 use-after-close。详见 `design_doc/11_resource_manager.md`
  - Redis 连接现为句柄型资源 `redis_connection`：`transform_redis_get` / `transform_redis_set` 不再内联 `redis_addr`/`redis_db`/`redis_password`，改为按 `resource_name` 借用句柄；连接参数（addr/password/db/interval，`interval: -1` 永不刷新）在统一 JSON 的 `resource_config` 中声明
  - **Redis client 失败收敛与 SIGPIPE 守卫**（0.10.10）：`Client::send_command` 用 `send(..., MSG_NOSIGNAL)` 替代裸 `write()`，远端中段断连不会 SIGPIPE 整死进程；AUTH / SELECT 失败路径用 try/catch 包裹后 `close(fd_)` + `fd_=-1`，落入 `connected()==false` 静默降级路径，与 dial-failure 路径对称。详见 `must/conventions.md` 的"pine-cpp 网络客户端必须 MSG_NOSIGNAL"与 `reference/operator-contract.md` 的"Failed-path 静默降级审计契约"
  - **Redis per-command 指标**（0.10.10）：`Client::run_command<T>(name, fn)` 模板包裹业务命令调用，使用 `Counter` / `Histogram` 发射 `pine_redis_command_total` / `pine_redis_command_duration_seconds`（labels: name / command / status）。**已知缺口**：cpp client 当前抛单一 `std::runtime_error` 类型，timeout / pool_timeout 当前都落 `error` 桶，需后续在 client 层引入错误类型分层；详见 `reference/metrics-observability.md` 的"per-command 指标 status taxonomy"
  - `transform_by_remote_pineapple` 算子：基于 `libcurl` 实现 SSRF 安全保护（拦截 loopback/private IP 配置）、HTTP POST 超时与最大体积限制
  - Redis `ConnectionPool`：per-key idle 上限（`kMaxIdlePerKey=16`）+ idle timeout（`kIdleTimeout=60s`）+ acquire 时 LIFO stale discard + `connected()` 健康检查。`ScopedClient` RAII handle 自动 release back to pool，替代算子内 inline PoolGuard。**`pool_timeout_ms` 当前 no-op**：cpp 池 acquire 不阻塞（idle 队列空时直接构造新 client），schema 仍保留以便跨引擎配置块对称
- **根级配置扩展**
  - `log_prefix`（同时支持 `EngineOptions::log_prefix` 覆盖），最终通过 `log::SetPrefix` + `Ldate|Ltime|Lshortfile` 应用
  - `_PINEAPPLE_VERSION` / `_PINEAPPLE_CREATE_TIME` 解析并通过 `Config` 暴露，供下游工具读取
  - `resource_config` 解析为 `ResourceEntry`（type / interval / params），由 `Manager::load_from_config(...)` 解析为活跃 fetcher
- **校验**
  - skip 字段必须出现在 `$metadata.common_input` 中，否则在配置加载期报 ValidationError

cross-validate 接入范围以 `scripts/cross-validate/` 目录为准，目前 codegen-schema / render-dag / execution / column-store / error / server-http / cancellation / concurrent / raw-byte / hot-reload / extensibility-parity / metrics-parity 等多个 section 通过 `CPP_RUN` / `CPP_DAG` / `CPP_SERVER` / `CPP_CODEGEN` 环境变量条件性包含 pine-cpp。其中 `01-codegen-schema.sh` 提供两条 C++ 接入：1b（Go vs C++ schema JSON 结构对比，参数类型/默认值/必需性维度）与 1d（Go vs C++ `apple_generated/` Python 产物字节级 `diff -r`）。两段都按 "未设置 `CPP_CODEGEN` 则跳过 / 设置但产物分歧则 fail" 的语义工作，避免回归到 byte parity 时被静默跳过。具体覆盖以脚本为准，避免在文档中硬编码层数。

## 关键设计决策

### 1. 对外接口

- 先提供 **C++ API**
- public API 保持窄而简单，为未来增加 **C ABI** 留出空间
- 不把复杂模板与 STL 容器直接暴露为长期稳定的 ABI 契约

### 2. 构建与语言标准

- **CMake**
- 目标标准：**C++23**
- 错误返回采用 `std::expected<T, E>`；若目标编译器过旧，可用 `tl::expected` 作为兼容层
- JSON 序列化使用 **RapidJSON**（PERF-15 替代手写 `dump_json`，消除序列化热点）；JSON 解析仍使用 **nlohmann/json**（边界层读入，非热路径）

### 3. Lua 集成

- 选择 **LuaJIT**
- 保持 Lua 5.1 语义，与现有脚本公共子集兼容
- 重点收益来自 tracing JIT 与更低解释器分发成本
- `StatePool` 提供按需 `LuaVM` 分配与借用，维护 baseline globals 快照并在释放时清理变异；实现 `StatsProvider`（`/stats` 接口暴露 pool 计数）和 `MetricsAware`（注入外部提供商）。`Releaser::operator()` 是 `unique_ptr` deleter，在 noexcept `~unique_ptr` 边界内用 `try/catch(...)` 包裹 `release_vm`，防止 `reset_to_baseline` 异常触发 `std::terminate`。`kSkipBuiltins` 为 `constexpr string_view[]` + `binary_search`（消除每进程首次调用的堆分配）

### 4. 内存与对象生命周期

- **Arena + RAII 分层共存**
- 长生命周期对象（Engine、配置、LuaJIT state）由 RAII 管理
- 请求执行期间的中间对象以 arena 分配为主
- **per-thread bump arena**（PERF-14）：每个线程持有 thread_local bump allocator，请求级 Variant/FlatMap 分配走 arena 路径，减少 malloc 竞争
- 对象 API 与分配策略解耦，采用接近 Protobuf Arena 的模式
- **jemalloc**（默认启用）：CMake `PINE_USE_JEMALLOC=ON`（默认），通过 `target_link_libraries(jemalloc)` 链接，减少多线程 malloc 锁竞争和内存碎片。sanitizer 构建需显式 `-DPINE_USE_JEMALLOC=OFF`

### 5. DataFrame 与数据表示

- DataFrame 内部采用 **强类型列**，而不是 cell-level 动态 variant
- 动态值 `Variant`（原名 `JsonValue`）只存在于边界层：JSON 解析、Lua 交互、operator config、最终 JSON 序列化
- `Variant::object_t` = `FlatMap<std::string, Variant>`（sorted vector of pairs），在典型字段数（< 20）下比 hash map 更快
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

- **双隔离线程池**（v0.9.2 ready-queue scheduler）
- DAG pool（默认 `nproc * 4`）：调度 DAG 节点，只有 in-degree 归零的节点才被提交
- Shard pool（默认 `nproc * 2`）：`data_parallel` 分片执行专用
- 不依赖 C++20 coroutine runtime；当前阶段优先成熟、可控、易调试的线程池方案
- CLI 可通过 `-dag-pool-size` / `-shard-pool-size` 调整

### 9. 扩展机制

- 与现有运行时一致，采用 **编译时注册**（schema + factory）
- C++ 侧通过 `PINE_REGISTER_OPERATOR_T(Type, schema)` 宏完成 static init 注册；每个内置算子位于 `operators/<category>/<name>.cpp`
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