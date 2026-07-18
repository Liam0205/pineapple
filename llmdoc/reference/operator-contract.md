# 算子开发契约

本参考文档面向新增或修改 Pineapple 算子的开发者。

## 权威文件

以下文件为唯一事实源：

- `pine-go/internal/types/operator.go`
- `pine-go/internal/types/operator_io.go`
- `pine-go/internal/registry/registry.go`
- `pine-go/operator.go`
- `pine-go/operator_io.go`
- `pine-go/registry.go`
- `pine-go/operators/` 下的代表性实现

### Pine-Java 对等实现

Pine-Java 完整实现了全部内置算子（清单见 `pine-java/.../operators/AllOperators.java`），位于 `pine-java/src/.../operators/`。Java 侧的对等文件为：

- `pine-java/src/.../Operator.java` — 算子接口
- `pine-java/src/.../OperatorInput.java` / `OperatorOutput.java` — IO 类型
- `pine-java/src/.../Registry.java` — 注册表（含独立 Schema 注册与校验）
- `pine-java/src/.../ParamSpec.java` — 参数规格声明
- `pine-java/src/.../OperatorSchema.java` — 算子 Schema 定义
- `pine-java/src/.../operators/AllOperators.java` — 全量注册入口（18 算子含完整 ParamSpec 声明）

Java 侧为独立 Schema 源，拥有完整的 schema-based 注册：`Registry.register(OperatorSchema, Supplier<Operator>)`。`Registry.exportSchemaJSON()` 导出与 Go 格式一致的 JSON，供 CI 交叉验证。`validateAndExtractParams()` 执行与 Go 等效的严格校验：过滤保留键、检查必填参数、注入默认值、拒绝未声明参数。

`AllOperators.java` 中的注册清单含两个 benchmark 专用算子（`transform_bench_cpu` / `transform_bench_sleep`）。这两个算子在 pine-go 0.9.7 起常驻于 `pine-go/operators/transform/`（无构建标签门控），用于跨运行时对照 benchmark。

更重的 bench stub 集合（`recall_bench_static` / `transform_bench_random_drop` / `reorder_bench_topn_boost` 等）在各运行时中以构建开关方式对外可见：pine-go 通过 `//go:build pine_bench` 标签的 `all_bench.go` 引入，pine-java 通过 `-Dpine.bench=true` 系统属性，pine-cpp 通过 CMake `-DPINE_BUILD_BENCH_STUBS=ON`。`scripts/bench-cross-runtime.sh` 在调用各 builder 时统一传入对应开关。

## 算子生命周期

算子实例经历两个阶段。

1. `Init(params map[string]any) error`
   - 引擎构建期间调用一次
   - 仅接收业务参数
   - 引擎保留键已被剥离
   - 默认值已被应用

2. `Execute(ctx context.Context, input *OperatorInput, output *OperatorOutput) error`
   - 每次请求执行时调用
   - 从提供的输入快照读取
   - 向提供的输出收集器记录写入
   - 可能跨请求在同一算子实例上并发运行

该并发模型意味着算子结构体上的任何可变状态必须在 `Init()` 后不可变或显式同步。

### Pine-Java 执行签名

Java 侧等效签名为 `void execute(CancellationToken token, OperatorInput input, OperatorOutput output) throws PineErrors.OperatorException`。`CancellationToken` 是 volatile boolean，等效于 Go 的 `context.Context` 取消信号。算子中的长时间循环应检查 `token.isCancelled()` 以支持协作式取消。声明 checked `OperatorException` 使引擎能区分预期算子错误（包装为 `ExecutionError`）与意外运行时异常（包装为 `PanicError`），语义对应 Go 的 error/panic 分界。

## 必需接口

公共算子接口通过 `pine-go/operator.go` 暴露，在 `pine-go/internal/types/operator.go` 中定义：

- `Init(params map[string]any) error`
- `Execute(ctx, input, output) error`

使用 `pine-go/operator_io.go` 中的 `OperatorInput` 和 `OperatorOutput`，而非直接触及运行时内部。

### Pine-C++ 注册与 CRTP traits

C++ 侧提供注册路径：

- **`PINE_REGISTER_OPERATOR_T(Type, schema)`**——通过 `OperatorTraits<T>` 在编译期 `std::is_base_of_v` 检查四个 marker 位（`ConsumesRowSet` / `MutatesRowSet` / `AdditiveWritesRowSet` / `ConcurrentSafe`），调用 `register_operator_typed<T>(schema)` → `register_operator_with_traits(schema, factory, ...)` 直接填充 `OperatorEntry` 的标记字段。重量级构造器（Lua pool、libcurl handle、redis pool seed）只在 per-Engine 实例化时付一次构造成本。

校验逻辑：空 name、空 description、空 param description、null factory、重复 name 均 throw `RegistryError`。

内置算子均使用 `PINE_REGISTER_OPERATOR_T`。新增 C++ 算子使用此宏。

## 注册契约

算子通过 `pine-go/registry.go` 中的 `pine.Register(schema, factory)` 注册，通常在算子源文件的 `init()` 函数中。

### 必需 Schema 字段

`OperatorSchema` 必须提供：

- `Name` — 稳定的算子类型名如 `transform_copy`
- `Type` — 六种算子类型之一
- `Description` — 非空的人类可读描述
- `Params` — 按参数名索引的业务参数 map
- `Factory` 不是 Schema 的一部分；由注册调用单独提供

每个 `ParamSpec` 应提供：

- `Type` — 文档/codegen 类型标记
- `Required` — 调用者是否必须提供
- `Default` — 可选默认值
- `Description` — 非空描述

### 严格参数校验

`pine-go/internal/registry/registry.go` 的 `ValidateAndExtractParams` 现在对传入参数执行严格检查：

- 所有未在 `Schema.Params` 中声明的参数名会被拒绝
- 错误消息会列出全部未声明的参数名

此行为的意义：

- 拼写错误的参数名会立即报错，而不是被静默忽略
- `Schema.Params` 必须声明算子接收的所有业务参数，否则引擎加载会失败
- 引擎保留键（`type_name`、`$metadata` 等）在校验前已被剥离，不受影响

因此新增算子或修改算子参数时，`Schema.Params` 必须与实际使用的参数名保持完全一致。

### 注册失败行为

`pine-go/internal/registry.Register` 有意严格，在无效定义时 panic，包括：

- 空算子名
- 无效算子类型
- 空算子描述
- 任何参数缺少描述
- 重复注册

将 Schema 注册视为启动时校验。缺失元数据被认为是程序员错误，不是可恢复的运行时条件。

### 外部构建算子实例

`pine.BuildOperator(typeName, params)` 是 `pine-go/internal/registry.BuildOperator` 的公共包装器，允许外部消费者（benchmark、测试工具）按类型名构建已注册算子的实例。

流程：查找注册表 → 校验参数 → 创建实例 → 调用 `Init(params)` → 返回 `(Operator, OperatorSchema, error)`。

若算子实现 `MetadataAware`，调用方需在 `BuildOperator` 之后手动调用 `SetMetadata` 注入字段元数据（引擎内部会自动做，外部消费者需显式处理）。

## 资源消费模式

算子可通过 `resource.FromContext(ctx)` 从请求上下文中拉取资源。无需实现特殊接口。

约定：

- 声明 `resource_name` (string) 参数——`ValidateResourceDeps` 依赖此命名约定在启动时校验资源依赖
- `Init` 中存储 `resource_name` 参数值
- `Execute` 中调用 `resource.FromContext(ctx).Get(name)` 拉取资源值
- 处理 `nil` provider（未注入）和 `(nil, false)` 返回（资源不存在）

内置资源消费算子：

- `recall_resource` — 资源值为 `[]map[string]any`，逐个 `AddItem`
- `transform_resource_lookup` — 资源值为 `map[string]any`（lookup table），按 item 字段值查找写入。非 string key 自动 coerce（float64 整数 → string）。Apple 编译器校验 `lookup_key` ∈ `item_input`、`output_field` ∈ `item_output`
- `transform_redis_get` / `transform_redis_set` — **句柄型资源消费者**，借用 `redis_connection` 资源（见下文）

### Redis 算子契约（句柄型资源借用）

`transform_redis_get` / `transform_redis_set` 不再内联 `redis_addr` / `redis_db` / `redis_password` 等连接参数，而是按 `resource_name` 借用一个内置的 `redis_connection` **句柄型资源**。连接参数（addr / password / db / interval / 超时与连接池 / metrics_name）在统一 JSON 的 `resource_config` 中声明，其中 `interval: -1` 表示该句柄永不刷新。多个 Redis 算子引用同一 `resource_name` 时共享同一连接池；连接池由 ResourceManager 拥有，客户端按请求借用、`Execute` 返回时释放。

`redis_connection` 资源参数：

- `addr` (string, required) — Redis 地址 `host:port`
- `password` (string, optional, default `""`) — 认证密码
- `db` (int, optional, default `0`) — 选择的 DB 编号
- `dial_timeout_ms` (int, optional, default `2000`) — TCP dial 超时，毫秒
- `read_timeout_ms` (int, optional, default `2000`) — 单次命令读超时，毫秒。**雪崩防护核心**：单次 Redis 调用超此值将以 `ctx.DeadlineExceeded` 失败，`fail_on_error=false`（默认）路径降级为 cache miss + warning，避免请求 goroutine 在 Redis 慢期堆积
- `write_timeout_ms` (int, optional, default `2000`) — 单次命令写超时，毫秒。**pine-java 注**：Jedis 仅暴露单一 `socketTimeoutMillis`，本引擎下生效值为 `max(read_timeout_ms, write_timeout_ms)`；建议保持 `read_timeout_ms >= write_timeout_ms` 以避免意外。pine-go 与 pine-cpp 独立处理读/写两个方向
- `pool_timeout_ms` (int, optional, default `2000`) — 等待空闲连接的超时，毫秒。**pine-cpp 注**：当前 C++ 池的 `acquire` 不阻塞（idle 队列空时直接构造新 client），故此参数在 pine-cpp 下为 no-op；schema 保留以便跨引擎配置块对称
- `pool_size` (int, optional, default `0`) — 连接池上限。`0` 走引擎默认（pine-go: `10*GOMAXPROCS`；pine-java: commons-pool2 默认 `8`；pine-cpp: 仅作 idle 队列每 host 上限 `16`）。**pine-cpp 语义注**：C++ 池没有总连接数硬上限，仅控制 per-host idle 队列大小；如部署依赖 `pool_size` 做并发控制，pine-cpp 在故障期可能无界构造新 client，需结合上层并发控制
- `interval` (int) — 句柄刷新间隔；`-1` 表示永不刷新
- `metrics_name` (string, optional, default `""`) — 资源级指标的 `name` 标签值。**非空时**资源发出资源级指标（4 个连接池/探针指标 + 2 个 per-command 指标 `pine_redis_command_duration_seconds` / `pine_redis_command_total`，详见 `metrics-observability.md` 的"资源级指标 fan-out 路由"）并启动 15s PING 探针线程（`Start()` 时立即跑一次）；**为空（默认）时**不发任何指标、不启探针。指标经 fan-out（Tee）同时进入注入的 Provider 和 `/stats.resources`。

**Cascade-safety 背景**：`{dial,read,write,pool}_timeout_ms` + `pool_size` 五参数自 0.10.10 暴露，由 2026-06-22 tipsy-recsys 故障驱动。该故障中 Redis PING p99 短期飙至 ~970ms，pre-fix 资源沿用 client 默认值（go-redis v9: read/write 3s, dial 5s, PoolSize 20）使每个 /execute 请求阻塞 in-flight、heap_inuse 单 pod 飙至 3.87 GiB、运行时 OOM。生产部署应显式配置（典型：`read_timeout_ms=500, write_timeout_ms=500, dial_timeout_ms=1000, pool_timeout_ms=1000, pool_size=50`）以缩短雪崩窗口。

参数契约：

- `resource_name` (string, required) — 要借用的 `redis_connection` 资源名
- `key_prefix` (string, required) — 与 common_input 字段拼出的 key 后缀组合成最终 key（`key_prefix + join(common_input values, ":")`）
- `data_type` (string, optional, default `"string"`) — `"set"` / `"string"` / `"list"`
- `fail_on_error` (bool, optional, default `false`) — 基础设施错误是否升级为致命错误

降级语义（三运行时 Go/Java/C++ 字节级对齐）：

- **借用失败 = 静默降级**：未注入 provider、资源不存在、或值类型不符时，不连接 Redis；`transform_redis_get` 输出 `cache_hit=false` 且不写 value，`transform_redis_set` 为 no-op
- **借用成功但命令/连接出错**：记 warning 日志（`output.SetWarning`）；若配置 `fail_on_error`，则进一步抛出致命错误使算子失败，否则 `get` 视为 cache miss（`cache_hit=false`）、`set` 跳过

`transform_redis_get` 的 `common_output[0]` 为结果值、`common_output[1]` 为 cache-hit 布尔标志。

### Failed-path 静默降级审计契约

新增任何 Redis client / 资源失败路径（AUTH 失败 / SELECT 失败 / dial 超时 / pool 等待超时 / 命令执行错误等）都必须走完`fail_on_error=false → connected()==false → 借用层视为不可用 → 算子静默降级`链路。审计 review 时强制核对：

- client 抛错路径必须 close fd 并把 `connected()` 翻成 false（pine-cpp Client 的 AUTH/SELECT 失败 try/catch 是 reference 实现，详见 `pine-cpp/src/runtime/redis_client.cpp`）
- 错误必须 surface 为 `output.SetWarning(...)` 而不是 panic 或 fatal
- `fail_on_error=true` 路径继续抛 `ExecutionError`，与默认路径分支独立

历史教训：pine-cpp 0.10.10 之前 AUTH/SELECT 失败留下半连接（fd 未关、`connected()` 仍为 true），后续借用此 client 时直接发命令失败、绕过静默降级；新增 client 失败路径时必须按本契约 close fd + 翻 `connected()`。

## 保留 JSON/配置键

这些键为引擎所有，在 `Init(params)` 接收其 map 之前被过滤：

- `type_name`
- `$metadata`
- `$code_info`
- `skip`（运行时配置层为字符串列表；旧版单字符串 JSON 会在加载期兼容归一化）
- `recall`
- `sources`
- `debug`
- `row_dependency`
- `common_defaults`
- `item_defaults`
- `strict_common`
- `strict_item`
- `for_branch_control`
- `data_parallel`

不要定义依赖这些名称的业务参数。

## 可选接口

### `ConcurrentSafe`

若算子希望在 `data_parallel > 1` 时进入并发分片执行路径，必须实现 `pine-go/internal/types/operator.go` 中的 `ConcurrentSafe` 可选接口。

推荐模式：

- 直接嵌入 `pine.ConcurrentSafeMarker`
- 只在确认同一算子实例可被多个 shard 并发重入调用时才声明该能力

职责边界：

- Apple/编译期只做结构性校验：仅 Transform 可启用 `data_parallel`，且 `$metadata.common_output` 必须为空
- Go/引擎加载期在 `pine.NewEngine()` 的 `validateDataParallel` 中做最终能力检查：`data_parallel > 1` 时实例必须实现 `ConcurrentSafe`

未实现 `ConcurrentSafe` 的 Transform 默认不允许启用 `data_parallel > 1`。这替代了旧的名称 blocklist 模式，把事实源收敛到真实持有算子实例的 Go 层。

当前内置 Transform 中，8 个逐 item 独立实现已标记 `ConcurrentSafe`；`transform_normalize` 保持未标记，因为它依赖完整 item 集合语义。

### `MetadataAware`

若算子实现 `pine-go/internal/types/operator.go` 中的 metadata-aware 接口，引擎将在 `Init()` 后注入字段元数据。

典型模式：

- 嵌入 `MetadataHolder`
- 在 `Execute()` 中读取 `CommonInput`、`CommonOutput`、`ItemInput`、`ItemOutput`

这是算子获知应读写哪些字段的标准方式。

### `DebugAware`

若算子实现 debug-aware 接口，引擎在 `Init()` 后注入逐算子调试设置。

典型模式：

- 嵌入 `DebugHolder`
- 当算子需要超出标准运行时 trace 的专用调试行为时查阅 debug 信息

大多数算子仅需运行时 trace 捕获，但 Lua 是同时嵌入 metadata 和 debug holder 的示例。

`DebugHolder.OperatorName()` 会返回引擎注入的算子实例名。它不仅用于 debug log，也可被后续 `MetricsAware` 注入阶段复用，例如把 operator 名作为外部 metric label 值。

### `LoggerAware`（算子日志规范）

算子的诊断日志必须走引擎注入的 logger 通道，输出自动携带所属引擎的 `log_prefix`（引擎实例级作用域，issue #172——多引擎同进程时各自的日志保持归属正确）。禁止直接用进程全局 `log.Printf` / `System.err` / `std::cerr` 输出算子诊断行。三运行时入口：

| 运行时 | 接口 | 使用方式 |
|---|---|---|
| pine-go | `types.LoggerAware`（`SetEngineLogger(*log.Logger)`） | 嵌入 `pine.LoggerHolder` 自动满足，调用 `Logf(format, args...)`；`DebugHolder` 已内嵌 `LoggerHolder`，DebugAware 算子免费获得 |
| pine-java | `LoggerAware`（`setEngineLogPrefix(String)`） | 继承 `AbstractOperator` 后调用 `logf(format, args...)` |
| pine-cpp | `LoggerAware`（`set_engine_log_prefix(const std::string&)`） | 实现接口存下 prefix，输出行自行前置（参考 `pine-cpp/operators/observe/observe_log.cpp`） |

安全约束：**用户可控字符串（如配置来的 log_prefix）绝不拼进 printf 格式串**——要么作为 `%s` 实参、要么单独 print。Java 的 printf 家族有 `%` 注入坑：`printf(prefix + format, args)` 会让含 `%` 的前缀（如 `"[100%] "`）运行时抛 `UnknownFormatConversionException`（`AbstractOperator.logf` 的 prefix 单独 print 是 reference 实现）；Go 的 `log.New` 把 prefix 当字面量、C++ 用 `<<` 拼接，天然安全，但新增格式化路径时同样遵守此隔离。

Go 侧包装 `log.Logger.Output(calldepth, ...)` 时注意 calldepth 是 wrapper 层数的函数而非常量，逐层 +1 并在注释写清推导（详见 `memory/reflections/per-engine-log-prefix.md`）。

### `StatsProvider`

若算子实现 `StatsProvider`，引擎会在 `Engine.OperatorCustomStats()` 中收集该算子的自定义原子统计，并由 `pine-go/pkg/server/server.go` 挂载到 `/stats` 响应中的 `operator_detail` 字段。

该接口适合暴露零配置排障所需的进程内累计计数，例如 Lua state pool 的 borrow / return / create / active 计数。

### `MetricsAware`

若算子实现 `MetricsAware`，引擎会在 `Init()`、`SetMetadata(...)`、`SetDebugInfo(...)` 之后调用 `SetMetricsProvider(provider)`。

稳定注入顺序为：

1. `MetadataAware`
2. `DebugAware`
3. `LoggerAware`
4. `MetricsAware`

这使得像 Lua 算子这样的实现可以在 `SetMetricsProvider` 内安全读取 `DebugHolder.OperatorName()`，把 operator 名绑定为 label 值。

设计边界：

- `MetricsAware` 面向外部指标系统，不替代 `/stats`
- provider 可能是 `metrics.Nop()`，实现必须把 no-op provider 视为正常路径
- Pineapple core 不依赖具体 Prometheus SDK；算子只依赖 `pine-go/pkg/metrics` 抽象

### `Closer`

若算子持有跨请求资源（Lua state pool、长连接、后台 goroutine、并发 worker 等），可选实现 `Closer` 接口在引擎退役时显式释放。各运行时签名一致：

| 运行时 | 接口位置 | 方法签名 |
|---|---|---|
| pine-go | `pine-go/internal/types/operator.go` | `type Closer interface { Close() error }` |
| pine-java | `pine-java/src/.../Closer.java` | `void close() throws Exception` |
| pine-cpp | `pine-cpp/include/pine/operator.hpp` | `class Closer { virtual void close() = 0; }` |

引擎退役（hot-reload swap、graceful shutdown）时调用 `Engine.Close()` / `Engine.close()`，依次对每个 `CompiledOperator.Instance` 做 `instanceof Closer` 判定并触发 `Close()`。各运行时把单算子 close 失败聚合上报（pine-go 用 `errors.Join`，pine-java 收集 list，pine-cpp 收集 vector），不阻断后续算子的 close。

调用约定：

- `Close()` 在每个引擎实例上**至多调用一次**；幂等不是契约要求
- 调用时引擎已不再接收新请求，但**未必所有 in-flight 请求都已结束**——server 层需先 drain，再 retire 旧引擎
- 调用方必须在锁外触发 `Close()`，避免阻塞配置 reload 路径
- 不实现 `Closer` 的算子由 GC / RAII 回收（Lua pool 等已主动放弃 state retention，依赖 GC 即可）

典型实现：`pine-go/operators/lua/lua.go` 的 `LuaOp.Close()` 调用 pool 的 `Close()`，仅翻转 `closed` 标志阻止后续 `Borrow()` 返回新 state；池内已借出的 state 由 GC 回收。pine-cpp 对应 `TransformByLuaOp::close()`，通过 `LuaPool::close()` 翻转原子标志。

## 输入/输出 API 契约

### 从 `OperatorInput` 读取

使用只读访问器：

- `Common(field)`
- `Item(index, field)`
- `ItemColumn(field)` — 批量列访问（见下）
- `ItemCount()`
- `CommonKeys()`
- `ItemKeys(index)`

不要假设完整 frame 或任意未声明字段存在；输入从声明的元数据投影。

#### 批量列访问（`ItemColumn` / `itemColumn` / `item_column`）

一次调用返回某 item 字段的整列值（一次锁 + 一次列解析，替代逐元素 `Item()` 循环的 per-element 锁税 + map 查找税——这是列存连续布局优势的兑现点，详见 `memory/reflections/column-vs-row-parity-investigation.md`）。三引擎方法名与返回类型：

| 引擎 | 方法 | 返回 | Frame 层扩展点 |
|---|---|---|---|
| pine-go | `OperatorInput.ItemColumn(field)` | `[]any` | `types.ColumnReader` 可选接口（未实现则降级逐元素 gather） |
| pine-java | `OperatorInput.itemColumn(field)` | `Object[]` | `Frame.itemColumnView` default method（返回 null = 降级） |
| pine-cpp | `OperatorInput::item_column(field)` | `std::vector<Variant>` | `Frame::item_column` 纯虚方法 |

语义契约（三引擎一致）：

- 元素 i 与逐元素 `Item(i, field)` **完全一致**，含 item_defaults 对 nil 槽位的替换
- 返回值**只读**，仅在当次 Execute 内有效——Go/Java 下 ColumnFrame 无 defaults 时返回零拷贝视图（逃逸出锁的安全性依赖 DAG 冒险排序：写同字段的算子与行集变异算子已与读者串行化）；**禁止变异或在 Execute 结束后滞留引用**
- 缺失字段返回全 nil 列（与 `Item()` 的 nil-on-absent 一致）
- 兼容 data_parallel 分片窗口（offset/count 平移）

算子作者指引：扫描型热循环（对同一字段遍历全部 item）优先用批量 API；单点随机访问仍用 `Item(i, field)`。内置算子已全部改写为批量访问，可作参考实现。

字段访问遵循 InputFieldSpec 三态模型：

- **Nullable**（默认）：字段缺失 → error；值为 nil → 透传 nil 给算子。大多数字段的默认行为。
- **Strict**（通过 `strict_common` / `strict_item` opt-in）：字段缺失或值为 nil → error。适用于算子逻辑无法处理 nil 的必需字段。
- **Defaulted**（通过 `common_defaults` / `item_defaults`）：字段缺失或值为 nil → 替换为默认值。

#### 字段模式 JSON 键 ↔ 各层字段映射

| JSON 键 | Apple DSL（OpCall 字段） | pine-go（OperatorConfig） | pine-java | pine-cpp |
|---|---|---|---|---|
| `strict_common` | `strict_common` | `StrictCommon` | `strictCommon` | `strict_common` |
| `strict_item` | `strict_item` | `StrictItem` | `strictItem` | `strict_item` |
| `common_defaults` | `common_defaults` | `CommonDefaults` | `commonDefaults` | `common_defaults` |
| `item_defaults` | `item_defaults` | `ItemDefaults` | `itemDefaults` | `item_defaults` |

当涉及字段模式相关的 JSON 键名变更时，必须同步检查此表中所有列。历史教训：v0.9.0 翻转默认模式时运行时完成迁移但 Apple DSL 侧遗漏，导致声明能力丧失（详见 `memory/reflections/v090-nullable-strict-apple-desync.md`）。

### Pine-C++ OperatorInput 投影层

C++ 侧 `OperatorInput`（`include/pine/operator_input.hpp`）是 Frame + InputFieldSpec 之上的 lazy read-only proxy。与 Go/Java 的 eager map 构建不同，C++ 采用按需读取策略：

- `build_operator_input(frame, op_name, spec)` 校验顺序为先 common（strict→nullable）后 item（strict→nullable）——common 先于 item 是跨运行时报错对等契约（多违反时三运行时须抛同一首错）；strict item 走 `Frame::validate_strict_items` 虚方法（ColumnFrame/RowFrame 各自实现最优路径），校验通过后构造 proxy
- `common(field)` / `item(i, field)` 在调用时才从 Frame 读取，自动替换 defaulted 字段的 nil 值为默认值
- 算子签名为 `execute(const OperatorInput&, OperatorOutput&)`，与 Go `Execute(ctx, *OperatorInput, *OperatorOutput)` 语义等价

性能收益：避免 O(N×M) eager reify（N items × M fields），大管道中节省显著分配与拷贝开销。

### 写入 `OperatorOutput`

仅使用算子类型允许的输出方法：

- `SetCommon`
- `SetItem`
- `SetItemColumnFloat64` — 批量列写（见下）
- `AddItem`
- `RemoveItem`
- `SetItemOrder`
- `SetWarning`

`SetWarning` 与算子类型正交，用于非致命 warning。

#### 批量列写（`SetItemColumnFloat64` / `setItemColumnDouble` / `set_item_column_double`）

批量列访问的写侧对偶（issue #157 / PR #163）：算子把整列 float64/double 一次性交给 frame，替代 N 条逐元素 `SetItem` 记录（消除 per-element 装箱 + 写日志分配——profiling 归因写侧占 transform-heavy 分配 ~24%）。三引擎方法名：

| 引擎 | 方法 | 参数 |
|---|---|---|
| pine-go | `OperatorOutput.SetItemColumnFloat64(field, vals)` | `[]float64` |
| pine-java | `OperatorOutput.setItemColumnDouble(field, vals)` | `double[]` |
| pine-cpp | `OperatorOutput::set_item_column_double(field, vals)` | `std::vector<double>`（值传递，move 交接） |

语义契约（三引擎一致，由各引擎 column_write 测试钉死）：

- **应用时机 stage 2b**：在逐元素 item writes 之后、removals 之前；同字段"列写覆盖逐元素写"是确定性顺序语义
- **整列或全无**：`len(vals)` 必须等于 frame 当时的 item 数，不匹配报 `SetItemColumnFloat64 "f" length N does not match item count M`（跨引擎字节一致）
- **NaN/Inf 批量校验**先于任何写入（单次列写全有或全无），首错消息与逐元素路径相同：`item[i] write: field "f": NaN/Inf is not a valid JSON value`
- **所有权转移**：apply 时列存 frame 直接 adopt 底层数组为列存储（零拷贝，全槽位 present，typed 读快路径可命中）；行存 frame 在一个锁窗口内逐行 scatter（装箱不可避免）。算子交出后**不得再读写该数组**
- 对 `ValidateOutput` 计为 `SetItem`（Transform 可用，Recall/Filter/Observe 等非 item-write 类型违规）；debug 快照折叠进 `item_writes` 视图；data_parallel 分片的列写由 merge 折叠为带 offset 的逐元素写（分片是窗口，无法 adopt）

算子作者指引：整列计算型算子（normalize 等）优先用批量写；稀疏/条件写仍用 `SetItem`。Go 侧校验注意：不要用 `validateValue(field, any(v))` 逐元素校验——`any` 转换每元素装箱一次，吃掉批量写的收益；直接内联 `math.IsNaN/IsInf`。

## 算子类型表

`pine-go/internal/types/operator.go` 定义六种算子类型。运行时校验检查每次执行使用的输出方法。

| 类型 | 预期角色 | 允许的输出方法 |
|---|---|---|
| Recall | 产生新行/item，可选写 common | `AddItem`、`SetCommon` |
| Transform | 变异 common 或 item 字段值 | `SetCommon`、`SetItem` |
| Filter | 移除行/item | `RemoveItem` |
| Merge | 合并/去重行集 | `SetItem`、`RemoveItem` |
| Reorder | 改变 item 顺序 | `SetItemOrder` |
| Observe | 只读副作用 | 无 |

`data_parallel` 仅支持 Transform。启用时，`$metadata.common_output` 必须为空；其他算子类型会在 Apple 编译期与 Go 引擎加载期的结构校验中被拒绝。

当算子被用于 `data_parallel > 1` 时，还必须满足额外契约：同一个算子实例会在单次请求内被多个 shard 并发调用，因此实例必须显式实现 `ConcurrentSafe`，才能通过 `pine.NewEngine()` 的加载期校验并进入并发路径。未实现 `ConcurrentSafe` 的 Transform 默认不允许这样使用。

需记住的附加语义：

- Filter、Merge 和 Reorder 是 DAG 构建中的屏障类型。
- Recall 在 item 字段上作为追加型写者。
- Observe 算子不在 DAG 中创建阻塞性 WAR 读者行为。
- 有 item 字段（`item_input` 或 `item_output` 非空）的 Transform/Observe 通过 auto-inject 自动获得 `_row_set_` 读依赖，无需显式标记。

### ConsumesRowSet 与 auto-inject

引擎的 DAG 构建器会自动为具有非空 `item_input` 或 `item_output` 的算子注入 `_row_set_` 读依赖，确保这些算子在行集稳定后才执行。因此：

- **不需要显式 ConsumesRowSet 的情况**：算子在 metadata 中声明了 item 字段。auto-inject 机制会自动处理依赖。大多数 Transform 和 Observe 算子属于此类。
- **需要显式 ConsumesRowSet 的情况**：算子的 metadata 中 item 字段为空，但仍然结构性地访问行集。典型例子是 `transform_size`（调用 `ItemCount()` 但 metadata 无 item 字段）和 `transform_remote_pineapple`（无条件读取并转发 items）。这些算子的行集依赖无法从 metadata 推断，必须通过标记显式声明。

## 命名规范

算子名应用稳定前缀编码其分类：

- `recall_*`
- `transform_*`
- `filter_*`
- `merge_*`
- `reorder_*`
- `observe_*`

原因：

- 读者可快速推断语义
- Apple DSL 从 `recall_` 前缀推断 recall 行为
- 生成文档和类型化 helper 按这些稳定族分组

不要使用隐藏算子类型的模糊名称。

## 推荐实现模式

`pine-go/operators/` 中的内置算子通常遵循此结构：

1. 包级文档注释描述算子名、类型、参数和元数据契约
2. `init()` 函数调用 `pine.Register(...)`
3. 结构体嵌入 `pine.MetadataHolder`（当需要元数据时）
4. 可选嵌入 `pine.DebugHolder`（当需要调试信息时）
5. 可选实现 `pine.StatsProvider`（当需要把进程内累计统计暴露到 `/stats` 时）
6. 可选实现 `pine.MetricsAware`（当需要向外部 provider 记录指标时）
7. `Init()` 用于参数解析和一次性初始化
8. `Execute()` 用于请求时逻辑

代表性示例：

- recall：`pine-go/operators/recall/static.go`
- transform：`pine-go/operators/transform/copy.go`
- filter：`pine-go/operators/filter/condition.go`
- merge：`pine-go/operators/merge/dedup.go`
- reorder：`pine-go/operators/reorder/sort.go`
- observe：`pine-go/operators/observe/log.go`
- 跨服务 transform：`pine-go/operators/transform/remote_pineapple.go`
- debug-aware transform：`pine-go/operators/lua/lua.go`
- stats + metrics aware transform：`pine-go/operators/lua/lua.go`、`pine-go/operators/lua/pool.go`

## Lua 沙箱与安全模型

`pine-go/operators/lua/pool.go` 创建的 Lua VM 实施严格的沙箱隔离：

- 使用 `glua.Options{SkipOpenLibs: true}` 创建裸 state，默认不加载任何库
- 仅显式加载安全子集：`base`、`table`、`string`、`math`
- `dofile` 和 `loadfile` 被显式设为 `LNil`，阻止文件系统访问
- 未加载 `os`、`io`、`debug` 等危险库

此沙箱模型确保用户编写的 Lua 脚本无法：

- 访问宿主文件系统
- 执行系统命令
- 操作进程环境

### Context 取消传播

`pine-go/operators/lua/lua.go` 的 `Execute` 方法通过 `L.SetContext(ctx)` / `defer L.RemoveContext()` 把请求 context 注入 Lua VM。GopherLua 在指令边界检查 context cancellation，因此长时间运行的 Lua 脚本会在 context 超时或取消时被中断。

这是算子尊重 context 的典型模式：即使执行逻辑在外部 VM 中运行，仍然必须传播 Go context 以保证请求取消的及时性。

### Lua Pool 关闭保护

`pine-go/operators/lua/pool.go` 的 `newState()` 在 mutex 内检查 `sp.closed` 状态。若 pool 已关闭：

- 立即 `L.Close()` 释放刚创建的 state
- 返回 `errPoolClosed` 错误
- `Borrow()` 对 nil state 优雅处理

此保护避免 hot-reload teardown 过程中创建的 state 泄漏到已关闭的 pool。

### Lua Bridge 跨运行时数据转换约定

`transform_by_lua` 算子在 Go/Java/C++ 三运行时之间通过 Lua bridge 完成 host ↔ Lua 的双向转换。统一的命名与语义：

- **`toLua(host)` ↔ `fromLua(lua)`**：三运行时使用对称命名（pine-go `toLua`/`fromLua`、pine-java `toLua`/`fromLua`、pine-cpp `to_lua`/`from_lua`）。历史命名（`goToLua`/`luaToGo`、`toJava`、`push_value`/`to_value`）已统一收敛。
- **复合类型双向支持**：host slice/list/array → Lua array table（1-indexed），host map/dict → Lua hash table。Lua table 在 `fromLua` 时按"数字键 1..N 连续"判定为 array，否则为 map。
- **非字符串 key 拒绝**：Lua table 含非 string key（如 `{[10]=1}` 或 `{[true]='bad'}`）时，`fromLua` 返回 `lua: table has non-string key of type "<type>"` 错误。各运行时的报错文案在字节级一致，由 `fixtures/errors/runtime_lua_non_string_table_key.json` 锁定。
- **空表约定**：Lua 空 table 在三运行时一致编码为空 array `[]`（而非空 map `{}`），避免跨运行时 JSON 序列化产生差异。
- **Sequence 检测严格性**（pine-go）：`fromLua` 要求 `1..N` 严格连续才识别为 array，遇到 `nil` 中断即降级为 map（避免误判稀疏数组）。
- **错误前缀去重**（pine-go）：`fromLua` 的内部错误已带 `lua:` 前缀，外层 `executeForItem` / `executeForCommon` 不再二次包裹。

`fixtures/operators/transform_by_lua_tables.json` 与 `scripts/differential-fuzz.py` 的 `LUA_ITEM_FUNCTIONS` table-aware 用例（`#item_tags`、`for i=1,#item_vals`、return `{a, b}`）覆盖该转换路径，由 differential fuzz 与 cross-validate 持续验证。

### Lua 语言版本契约：脚本必须停留在 5.1 核心交集

三运行时的 Lua 实现各不相同，可用语言特性的交集 ≈ **Lua 5.1 核心**：

| 运行时 | 实现 | 语言版本 |
|---|---|---|
| pine-go | gopher-lua（纯 Go 解释器） | 5.1 + 少量 5.2 库函数 |
| pine-java | LuaJ 3.0.1（默认 luajc 编译到 JVM bytecode,`pine.lua.compiler=luac` 可切回解释;不可编译脚本自动 fallback luac） | 5.2 子集 |
| pine-cpp | LuaJIT 2.1（汇编解释器 + trace JIT） | 5.1 + 自选 5.2/5.3 回移（拒绝 `_ENV` 等语义变更） |

**用户脚本（`lua_script` 参数）禁止使用 Lua 5.2+ 特性**，否则只有部分运行时能执行，打破 byte-equal 契约。典型禁用项：

- `goto` / `::label::`（5.2；LuaJIT 支持但 LuaJ/gopher-lua 行为不一）
- `_ENV`、`setfenv` 替代语义（5.2 语义变更）
- 原生位运算符 `& | ~ << >>`、整除 `//`（5.3）
- 整数子类型 `math.type` / `math.tointeger` / `math.maxinteger`（5.3）——三引擎 number 均为 double 语义，与 `transform_by_lua` 的 JSON 边界一致，>2^53 整数本就会丢精度
- `utf8` 标准库（5.3）、`<close>` / `<const>`（5.4）

该交集是长期约束而非暂时状态：gopher-lua 无 5.3+ 跟进计划，LuaJIT 明确拒绝 5.2+ 语义。现状校验（2026-06）：仓库 262 个 Lua 脚本均在 5.1 核心内。

性能特征（隔离算子级，非契约，随环境波动）：LuaJIT 对循环密集脚本惩罚最小（trace JIT），gopher-lua 最大（纯解释 + 装箱分配），计算密集型热路径在 pine-go 上应写原生算子。三运行时倍率数据与复现 harness 见 `pine-go/benchmarks/bench_isolated_test.go`、`pine-java .../IsolatedLuaBench.java`、`pine-cpp/tests/bench_lua_isolated.cpp`。

## 元数据契约注释与生成文档

算子源文件中的文档注释由语言特定的解析器提取 metadata contract：
- Go 侧：`pine-go/pkg/codegen/docparse.go` 解析包级文档注释中的 `// Operator:` + `Metadata contract` 块
- Java 侧：`Codegen.java` 的 `parseJavadocMetadata()` 解析 Javadoc `/** Operator: ... */` 块

两侧使用相同的标注协议：

    Operator: <operator_name>
    Metadata contract
      CommonInput:  [<fields>]
      CommonOutput: [<fields>]
      ItemInput:    [<fields>]
      ItemOutput:   [<fields>]

重要边界：

- Schema 注册对名称、类型、参数和描述具有权威性
- 注释解析为生成文档补充元数据契约部分

保持注释与实际元数据用法一致，但优先在 Schema 和代码中修复运行时事实。

## Codegen 影响

算子 Schema 变更影响生成产物：

- `apple_generated/operators.py`
- `apple_generated/__init__.py`
- `doc/operators/`

Pine-Java 的 `Codegen.java` 支持双模式生成：

- `--export-schema <path>` — 从内部 Registry 导出 Schema JSON（供 CI 交叉验证）
- `--schema-from-registry` — 从内部 Registry 直接生成 Apple DSL 产物（`apple_generated/`）（供 CI 交叉验证）
- `-schema <path>` — legacy 模式（读取外部 JSON，保留兼容性）

两侧 codegen 输出保持一致（`operators.py`、`resources.py`、`__init__.py`、`doc/operators/`）。

任何对 Schema 形状、参数类型/默认值或注册表内容的变更都应随后重新生成。CI 通过 generated-diff 门控检查新鲜度。

此外，`pine-go/pkg/codegen/template.go` 中的 `pythonType()` 会把 Schema 参数定义里的 `Type` 字段映射为 Python 类型注解。当前支持的映射为：`"string"` → `str`、`"int"` / `"int64"` → `int`、`"float64"` → `float`、`"bool"` → `bool`；未识别的类型会回退为 `Any`。新增算子参数类型时，需要同时确认 codegen 映射已覆盖，否则生成的 Python helper 会退化为宽泛类型。

Codegen 模板对参数序列化采用分类策略（`alwaysParams` / `conditionalParams`）：required 或有 Default 的参数总是写入 `_params` dict；optional 且无 Default 的参数（如 `default_value`）仅在 `is not None` 时条件写入。生成 helper 的 Python 默认参数必须使用 Schema 的 `Default` 值；不要用类型零值覆盖 Go 注册表默认语义。这避免了 Python `None` 被序列化为 JSON `null` 导致 Go 侧误判参数存在，也避免 `order=""`、`timeout=0.0` 等值覆盖 Schema 默认值。

## 元数据契约期望

元数据字段描述算子声明的字段契约，而非偶然的实现细节。

用它们表达：

- 读取哪些 common 字段
- 读取哪些 item 字段
- 写入哪些 common 字段
- 写入哪些 item 字段

这些声明被多个系统消费：

- Apple 校验器（`apple/validator.py`）
- 运行时输入投影（`pine-go/internal/dataframe/dataframe.go`）
- DAG 依赖推导（`pine-go/internal/dag/dag.go`）
- 生成的算子文档（`pine-go/pkg/codegen/`）

不正确的元数据因此可同时导致编译时和运行时的错误行为。

## 跨运行时格式化（GoFormat）

Java 算子需要生成与 Go 运行时一致的字符串表示时（如 Redis key 拼接、lookup table key coerce、条件比较值格式化），必须使用 `GoFormat` 工具类：

- `GoFormat.sprint(v)` — 替代 `String.valueOf(v)`
- `GoFormat.formatFloatF(d)` — 替代 `Double.toString(d)` 用于十进制表示
- `GoFormat.formatG(d)` — 替代 `String.format("%g", d)`

所有需要跨运行时格式一致性的算子均应通过 GoFormat 实现。已完成统一的消费者：

- `TransformResourceLookup` — key coerce
- `TransformRedisGet` — key 拼接
- `FilterCondition` — 条件比较值（原 `formatValue` 已移除）
- `ReorderShuffle` — salt 格式化（原 `formatFloatG` 已移除）

新增算子若对用户值做字符串转换，且该结果参与跨运行时比较（Redis key、fixture 断言），应使用 GoFormat。

模板参数（`ParamSpec.Templatable`，参见 [apple-compiler.md 模板参数 `{{field}}` 插值](../architecture/apple-compiler.md)）的运行时插值同样走 GoFormat：pine-go `fmt.Sprint(v)`、pine-java `GoFormat.sprint(v)`（**模板路径必须显式走 sprint，不要落回 `Double.toString()`**——历史上 float 字段直接走 `Double.toString` 会产生 `12.5` vs `12.500000` 跨运行时漂移）、pine-cpp `go_format_g(v)`。当前已声明为 `Templatable=true` 的参数：`transform_redis_get.key_prefix`、`transform_redis_set.key_prefix`、`transform_redis_set.ttl`、`filter_truncate.top_n`；新增可模板化参数时，需要同步在 cross-validate `scripts/cross-validate/17-templated-params.sh` 加 stringify parity probe。

## 参数模板化（`Templatable` / `templatable`）

算子 Schema 可在参数级别选 opt-in `{{field}}` 运行时插值能力。各运行时入口：

- Go: `pine-go/internal/types/operator.go::ParamSpec.Templatable bool`
- Java: `page.liam.pine.ParamSpec.templatable`
- C++: `pine::ParamSchema.templatable`

约束（与 [apple-compiler.md 模板参数 `{{field}}` 插值](../architecture/apple-compiler.md) 的契约相同，从算子作者视角概括）：

- 参数 `Type` 必须为以下标量类型之一：`string` / `int` / `int64` / `float` / `float64` / `bool`（权威清单见 `apple/validator.py::_TEMPLATABLE_SCALAR_TYPES` 与 `pine-go/internal/runtime/template.go::templatableScalarTypes`）
- DSL 端整值必须正好等于 `^\{\{(\w+)\}\}$`，违规在 Apple 编译期 fail-fast
- 模板字段名会被 Apple 编译器自动追加到 `common_input_template` 桶，不进入算子 `OperatorInput`
- Build-time 形状违规 → `ConfigError`；runtime 解析失败 → `ExecutionError` 由引擎包装算子名
- 算子 `Execute` 中通过 `input.templated_param("param_name")` 读取已解析值；返回类型与声明的 scalar type 对应（string→`string` / int·int64→`int64` / float·float64→`float64` / bool→`bool`）；若 build plan 未命中该参数（未声明 Templatable / DSL 未写模板），返回默认空值或非匹配类型 —— 作者应保留类型断言作为 defense-in-depth（参见 `transform_redis_get` 三引擎 `unreachable` 注释）
- **非 string scalar（int/int64/float/float64/bool）模板化参数的 Init 校验必须只接受 bare marker**：为让 `^\{\{(\w+)\}\}$` 标记能穿过 Init 的类型检查、交给 `BuildTemplatedParamPlan` 在运行时按请求覆写，Init 的 string 分支必须仅放行 bare marker，其余字符串（如手写的 `"top_n": "not_a_number"`）必须以 `<op>: <param> must be numeric` 报错——否则会静默 coerce 到 0，破坏 T3 前的错误契约。判定走各运行时的 canonical helper：Go `runtime.IsBareMarker`、Java `TemplateResolver.isBareMarker`、C++ `is_bare_marker`（`pine/template.hpp`）。错误文案三引擎须 byte-exact 一致（参见 `filter_truncate.top_n`、`transform_redis_set.ttl`）。

### Templatable 适用性判据

算子作者新增/审视参数时，先按下列六类判据排除"不能模板化"的情况；剩余的 scalar string/int/float/bool 参数才考虑开放。当前默认 opt-in，不主动声明 Templatable 即视为禁止。

**§1 init-time 拓扑 / 资源绑定型** — 决定算子在 build 期与谁对话。运行时变更 → borrow 失败 / 连接错路 / 绕过预校验。
代表：`resource_name`、`host`/`port`/`endpoint`、`*_pool_name`、`*_address`。

**§2 源代码 / 脚本 / 查询模板型** — 参数本身是另一门 DSL 的源码；`{{...}}` 在那门 DSL 里有自己的语法或属于字面量；build 期通常已被预编译/状态池绑定。
代表：`lua_script`、`function_for_item` / `function_for_common`、未来的 SQL/JSONPath/正则参数。

**§3 行为分支决定型** — 取值改变算子的代码路径，每条分支在 build 期可能被预校验或被 DAG 利用。运行时变 → build 期"我们不会走这条分支"的假设作废。
代表：`data_type`（"string"/"set"/"list"）、`direction`（"common_to_item"/...）、`method`、`order`（"asc"/"desc"）、`strategy`。

**§4 行为开关 / 模式标志型** — 表达算子的工作模式；跨请求变化通常是配置错配而非数据驱动，且会让观测/日志/告警语义糊。
代表：`fail_on_error`、`debug`、各种 `*_mode` / `*_policy`、`log_prefix`。

**§5 build 期校验前提型** — 参数本身参与 Apple 编译期或 runtime build 期的不变量检查（DAG 依赖推导、metadata 一致性、ConcurrentSafe 校验等）。模板化等于把"已校验"重新打开成"未知"。
代表：`lookup_key` / `output_field`（Apple validator 校验"必须在 item_input/item_output"）、`data_parallel`、参与 `merge_dedup` 字段选择的参数。

**§6 安全敏感参数** — 凭证 / 密钥 / SSRF 边界开关 / 响应体上限等。模板化让攻击者可通过控制请求字段拼凑系统凭证或绕过安全边界。
代表：`allow_private`、`max_response_size`、`password`、未来的 `*_token` / `*_secret`。

**共性判据（一句话）**：如果参数取值变化会让 build 期的某条假设作废，就不能模板化。

**适用画像**：剩下的"业务数据型 scalar param"才是 templatable 的天然受益者——取值变化只改业务数据流，不改算子语义/拓扑/分支。典型代表是 ID/key 类业务标识、字符串拼接位、业务驱动的阈值或限额。

参考 review checklist（添加 `Templatable: true` 前问自己）：

1. 这个参数取值变化是否会让任何 build 期校验作废？（DAG 排序、字段元数据、SSRF 检查、ConcurrentSafe 校验等）
2. 这个参数是否承载凭证或安全边界？
3. 这个参数是否决定算子走哪条代码路径？
4. 这个参数变化是否会让监控指标/日志检索/告警规则脱离原契约？
5. 这个参数是否是另一门 DSL 的源码或函数名/标识符？
6. 这个参数是否参与跨算子的 init-time 拓扑绑定（资源句柄、连接池、远端地址）？

任意一条回答"是"则不应开放 templatable；六条全否，才考虑加 `Templatable: true`。

## 常见陷阱

- 忘记参数描述导致注册 panic。
- 使用保留键作为业务参数意味着它永远不会到达 `Init()`。
- 通过错误的输出方法写入将使运行时校验失败。
- 在算子结构体上存储请求本地可变状态可能破坏并发执行。
- 更改 Schema 但不重新生成 `apple_generated/` 和 `doc/operators/` 将导致 CI 失败。

## 网络调用安全约束

执行外部网络调用的算子（如 `transform_by_remote_pineapple`）必须遵循以下安全契约：

### SSRF 防护

- Init 阶段对目标地址做 DNS 解析校验，拒绝解析到 private/loopback 范围的地址
- 运行时通过自定义 `http.Transport` 的 `DialContext` 在拨号时再次检查解析后的 IP，防止 DNS rebinding
- 提供 `allow_private` 参数，允许在合法内网调用场景下显式跳过 SSRF 检查

### 有界响应读取

- 禁止裸 `io.ReadAll`；必须使用 `io.LimitReader(body, maxSize+1)` 读取外部响应
- 读取后检查实际长度是否超过 `maxSize`，超过则返回错误
- 通过 `max_response_size` 参数暴露上限配置，默认 10MB

### 基础设施错误处理模式

- 非致命基础设施错误（如 Redis 连接失败）应通过 `output.SetWarning()` 透传，而非静默吞没
- 提供 `fail_on_error` 参数，允许调用方切换为严格模式（基础设施错误 → 算子失败）
- 这是跨算子可复用的错误处理模式，适用于任何依赖外部服务的算子

## 检索指针

- 接口和类型约束：`pine-go/internal/types/operator.go`
- IO helper：`pine-go/internal/types/operator_io.go`
- 注册表校验和保留键：`pine-go/internal/registry/registry.go`
- 公共包装器：`pine-go/operator.go`、`pine-go/operator_io.go`、`pine-go/registry.go`
- 内置示例：`pine-go/operators/`
- Codegen 消费路径：`pine-go/pkg/codegen/codegen.go`、`pine-go/pkg/codegen/template.go`、`pine-go/pkg/codegen/docparse.go`
