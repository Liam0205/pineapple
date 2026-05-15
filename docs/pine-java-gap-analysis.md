# Pine-Java vs Pine-Go 功能差异分析

## 概述
Pine-Java 是 Pine-Go (Pineapple) 引擎的 Java 移植，用于 MaxCompute UDF 场景。当前已实现全部 18 个算子和核心引擎功能。以下记录截至 2026-05-15 的细粒度行为差异。

## 已对齐的功能
- 并发 DAG 调度 (ForkJoinPool + CountDownLatch)
- data_parallel 分片执行 (ParallelExecutor)
- 原子统计 (Stats) + 可插拔 Metrics (Provider 接口)
- OpTrace + Debug 快照
- Warning 收集
- DAG 可视化 (DOT/Mermaid, 全视图/折叠视图, 6色着色)
- SubFlow 展开 + 环检测
- DataFrame RWLock 线程安全
- Codegen Python DSL 生成 + Markdown 文档
- PineServer 四端点 (/health, /execute, /stats, /dag) + 热加载
- ResourceManager + FetcherFactory 注册 + 后台刷新
- 全部 6 种 OperatorType (RECALL/TRANSFORM/FILTER/MERGE/REORDER/OBSERVE)

## 差异清单 (20项)

### 缺失算子 (5项)
| # | 算子 | 类型 | 复杂度 | 说明 |
|---|------|------|--------|------|
| 1 | transform_redis_get | Transform | 中 | Redis 读取，需 Jedis/Lettuce |
| 2 | transform_redis_set | Transform | 中 | Redis 写入 |
| 3 | transform_by_remote_pineapple | Transform | 高 | 远程调用 + SSRF 防护 + 字段映射 |
| 4 | reorder_shuffle_by_salt | Reorder | 低 | 基于 salt 的随机洗牌 |
| 5 | observe_log | Observe | 低 | 日志打印，唯一 OBSERVE 实现 |

### Engine 差异 (7项)
| # | 功能 | 复杂度 | 说明 |
|---|------|--------|------|
| 6 | ColumnFrame 列存 | 高 | presence bitmap + 稀疏语义 |
| 7 | StorageMode 配置 | 低 | 解析 storage_mode + 工厂切换 |
| 8 | 结构化错误类型 | 中 | ConfigError/RegistryError/ValidationError/ExecutionError/PanicError |
| 9 | PanicError 恢复 | 中 | try-catch Error + stack capture |
| 10 | Option pattern | 低 | WithMetrics/WithLogPrefix/WithDebug |
| 11 | Global debug + log_prefix | 低 | RootConfig 解析 + 传递 |
| 12 | sources 前向校验 | 低 | validateSourcesOrder |

### Server 差异 (5项)
| # | 功能 | 复杂度 | 说明 |
|---|------|--------|------|
| 13 | Middleware 注入 | 中 | HttpHandler 包装链 |
| 14 | HTTP metrics middleware | 中 | requests_total + duration |
| 15 | 超时控制 + MaxBody | 低 | JDK HttpServer 限制较大 |
| 16 | _return_trace 支持 | 低 | 条件返回 trace |
| 17 | Reload metrics (Provider) | 低 | 接入已有 metrics.Provider |

### 其他差异 (3项)
| # | 功能 | 复杂度 | 说明 |
|---|------|--------|------|
| 18 | ValidateResourceDeps | 低 | 遍历 operators 检查 resource_name |
| 19 | Lua VM 池化 + sandbox | 中 | LuaJ state pooling + 安全限制 |
| 20 | Codegen resources.py | 低 | 资源类模板输出 |

## 第二轮细粒度审计 (2026-05-15)

首轮 20 项差异已全部修复并提交 (c50166e)。以下为第二轮逐行对比发现的行为差异。

### 🔴 HIGH 严重度（影响正确性/安全性）

| # | 差异点 | Go 行为 | Java 现状 | 修复状态 |
|---|--------|---------|-----------|----------|
| 1 | ValidateOutput 类型约束 | 执行后校验输出字段是否超出 metadata 声明，违反时返回 error | 完全缺失，算子可写入任意字段 | ✅ |
| 2 | 热加载 Stop 旧资源 | `watchConfig` reload 时先 `oldRM.Stop()` 释放连接池 | 直接替换 AtomicReference，旧 ResourceManager 泄漏 | ✅ |
| 3 | 错误取消传播 | 任何算子 fatal 后通过 `context.Cancel()` 让后继算子立即退出 | 只设 fatalError 原子引用，`applied[pred].await()` 不会被中断 | ✅ |

### 🟡 MEDIUM 严重度（影响功能完整性）

| # | 差异点 | Go 行为 | Java 现状 | 修复状态 |
|---|--------|---------|-----------|----------|
| 4 | /execute 响应缺 warnings | 返回 `{"common":…, "items":…, "warnings":[…]}` | 只返回 common + items，warnings 丢弃 | ✅ |
| 5 | Stats.recordError 缺 duration | `recordError(name, duration)` 累加到 totalNs | 签名存在但内部未累加 duration | ✅ |
| 6 | ParallelExecutor 分片取消+warnings合并 | 一个 shard panic 后 cancel context，其余 shard 尽快退出；多 shard warning 合并 | 无取消机制，只取最后一个 shard 的 warning | ✅ |
| 7 | Config skip 字段校验 | 校验 skip 字段必须以 `_` 开头且在 common_input 中存在 | 无此校验 | ✅ |
| 8 | _return_trace 读取位置 | 从 common map 读取 | 从 request body JSON 根级读取（位置错误） | ✅ |

### 🟢 LOW 严重度（行为细微差异）

| # | 差异点 | Go 行为 | Java 现状 | 修复状态 |
|---|--------|---------|-----------|----------|
| 9 | ReorderShuffle 哈希公式 | `fnv64a([]byte(s))` → `float64(hash) / float64(MaxUint64+1)` | `(hash >>> 1) / Long.MAX_VALUE`，非 ASCII 和边界值不同 | ✅ |
| 10 | TransformRemotePineapple SSRF | DNS 校验 + net.Dialer 回调检查解析后 IP | 仅 DNS 名称校验，无 dial-time IP 检查（DNS rebinding 风险） | ✅ |
| 11 | TransformRedisSet fail_on_error | 支持 `fail_on_error` 参数，出错时返回 ExecutionError | 错误仅 stderr 打印，不会中止流水线 | ✅ |
| 12 | TransformByLua 沙箱模型 | `sync.Pool` + 每次仅执行用户函数调用 | `ConcurrentLinkedQueue` + 每次重新 `exec(script)` 含顶层定义 | ✅ |
| 13 | ReorderSort 稳定性 | `sort.Slice` 不稳定排序 | `Collections.sort` (TimSort) 稳定排序 | ⬜ 不修复 |
| 14 | FilterCondition 数值格式 | `fmt.Sprintf("%v", 1.0)` → `"1"` | `String.valueOf(1.0)` → `"1.0"`，条件匹配行为不同 | ✅ |

### 修复优先级排序

1. #1 ValidateOutput — 类型安全基石
2. #3 错误取消传播 — 影响 DAG 执行效率
3. #2 热加载 Stop — 资源泄漏
4. #6 ParallelExecutor 取消+warnings
5. #4 /execute warnings
6. #8 _return_trace
7. #5 Stats.recordError duration
8. #7 Config skip 校验
9. #9 ReorderShuffle 哈希
10. #14 FilterCondition 数值格式
11. #11 TransformRedisSet fail_on_error
12. #10 TransformRemotePineapple SSRF
13. #12 TransformByLua 沙箱
14. #13 ReorderSort 稳定性 — **不修复**（引擎不依赖排序稳定性假设）

## 第三轮深度审计 (2026-05-15)

前两轮差异已全部修复验证（10/10 verified）。以下为第三轮逐文件对比发现的新差异。

### 🔴 HIGH 严重度（wire 不兼容或正确性影响）

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| 1 | merge_dedup key 比较语义 | `map[any]struct{}` 原生类型相等（type+value） | `String.valueOf(key)` 字符串化比较 — int(1) 和 float(1.0) 可能不同 | ✅ normalizeKey → Double |
| 2 | /execute trace duration 字段 | `duration_ms` (float64, 毫秒) | `duration_ns` (long, 纳秒) — wire 不兼容 | ✅ |
| 3 | /execute warnings 格式 | `[]string` 平铺字符串 | `[{operator, message}]` 结构化对象 — wire 不兼容 | ✅ |
| 4 | HTTP body size 无上限 | `MaxBytesReader` 10MB 默认 | 无限制 readAllBytes() — OOM 风险 | ✅ 10MB 限制 |
| 5 | HTTP 超时缺失 | Read/Write/Idle timeout 可配 | com.sun.net.httpserver 无超时 — slowloris | ⬜ 平台限制 |
| 6 | Item input 契约校验缺失 | Execute 前校验 item 字段满足 contract.ItemInput | 不校验，缺失字段静默通过 | ✅ |

### 🟡 MEDIUM 严重度（功能/安全差异）

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| 7 | remote_pineapple null 传播 | key 存在即写入（含 null 值） | `val != null` 才写入 — 显式 null 丢失 | ✅ containsKey |
| 8 | redis_get null key suffix | `fmt.Sprint(nil)` → `"<nil>"` | `v != null ? v : ""` → 空串 — 命中不同 key | ✅ sprintValue |
| 9 | reorder_shuffle float 格式 | `%g` (如 `1e+08`) | `Double.toString` (如 `1.0E8`) — 大数哈希不同 | ✅ formatFloatG |
| 10 | Lua sandbox 松散 | SkipOpenLibs+仅 base/table/string/math | standardGlobals() 减法 — coroutine 等残留 | ✅ 白名单模式 |
| 11 | Lua context 取消 | `L.SetContext(ctx)` 指令边界中断 | LuaJ 无 context — 死循环无法取消 | ⬜ 平台限制 |
| 12 | ParallelExecutor panic 恢复 | 每 shard 有 panic recovery | 不 catch Error，shard Error 崩溃 ForkJoinPool | ✅ catch Throwable |
| 13 | ColumnFrame validateValue | 写入时校验值类型合法性 | 无校验 — 可存不可序列化 Object | ✅ |
| 14 | trace 缺 input/output 快照 | 包含 input_snapshot + output_snapshot | 只有 name/duration/skipped | ✅ |
| 15 | graceful shutdown | Shutdown(ctx) 等待 in-flight 完成 | shutdownNow() 直接杀线程 | ✅ stop(5) 已等待 |
| 16 | /dag collapse 参数校验 | 非法值返回 400 | parseInt 失败静默 collapse=0 | ✅ |
| 17 | reload 失败资源泄漏 | 新 RM 失败时 Stop() | exception 后已 start 的 RM 无 finally stop | ✅ try-finally |
| 18 | reloadCount 语义 | 首次加载不计入 reload | 首次 loadConfig 使 reloadCount=1 | ✅ |
| 19 | cancelLatch 5ms 延迟 | context.Cancel() 立即唤醒 | polling awaitWithCancel 5ms 周期 | ⬜ 平台限制 |

### 🟢 LOW 严重度（次要差异）

| # | 差异点 | 说明 | 修复状态 |
|---|--------|------|----------|
| 20 | recall_static init 校验 | Go Init 时验证 items 类型；Java 延迟到 Execute | ✅ |
| 21 | filter_truncate topN 宽度 | Go int64；Java int (2^31) | ✅ long |
| 22 | filter_condition null 格式 | Go `<nil>` vs Java `"null"` — 内部一致但跨运行时不同 | ✅ `<nil>` |
| 23 | redis_set 多余参数 | Java 有 fail_on_error 但 Go Schema 无 | ⬜ 有意超前，待 Go 补齐 |
| 24 | Schema type 字符串 | redis_db/ttl: Go `"int"` vs Java `"int64"` | ✅ |
| 25 | exportSchemaJSON key case | Go PascalCase, Java lowercase | ✅ PascalCase |
| 26 | Codegen 模板差异 | `_apply` + item_defaults/common_defaults/row_dependency/debug/name | `_build` + 仅基本 kwargs | ✅ |
| 27 | buildOperator 空 Schema bypass | Java 对无 params 算子跳过校验 | ✅ 始终校验 |
| 28 | observe_log 输出目标 | Go log.Printf；Java System.out.printf | ✅ stderr |
| 29 | EngineMetrics nil 风格 | Go Nop provider；Java null+条件检查 | ⬜ 风格差异 |
| 30 | logOnce 缺失 | Go sync.Once；Java volatile+synchronized 幂等保护 | ✅ |
| 31 | ResourceProvider.Get 语义 | Go (any,bool)；Java GetResult(value, exists) | ✅ |

## 第四轮审计 (2026-05-15)

前三轮修复后全面重审。发现 34 项差异，修复 22 项，7 项为平台限制/设计差异。

### 🔴 HIGH 严重度

| # | 差异点 | 说明 | 修复状态 |
|---|--------|------|----------|
| 1 | Codegen operators.py 返回类型双重 "Op" | `-> "XxxOpOp":` 应为 `-> "XxxOp":` | ✅ |
| 2 | Codegen resources.py __call__ → __init__ | Go 用 `__init__`，Java 错用 `__call__` | ✅ |
| 3 | Codegen resources.py import 路径 | `apple.base` 应为 `apple.resource` | ✅ |
| 4 | Codegen resources.py return 方式 | `self._build` 应为 `super().__init__()` | ✅ |
| 5 | Codegen toPythonLiteral 不转义 | 含引号的 default 产出非法 Python | ✅ |
| 6 | Server body size 先全量读入 | readAllBytes 可 OOM；改为流式限读 | ✅ |
| 7 | Server 从不调用 validateResourceDeps | Go 在 load 和 reload 时都调用 | ✅ |
| 8 | Engine.Execute throws 无 partial result | Go 返回 (result, err)；Java 改为 Result 含 error 字段 | ✅ |
| 9 | Lua pool 无状态清理 | returnState 后全局变量泄漏到下次 | ✅ 清理 operator 设置的 key |
| 10 | Lua pool 无 Close/shutdown | 热加载时泄漏 | ✅ |
| 11 | Lua context 取消传播缺失 | LuaJ 无 SetContext 等效机制 | ⬜ 平台限制 |
| 12 | RemotePineapple 响应体全量读入 | BodyHandlers.ofByteArray 可 OOM | ✅ ofInputStream + readLimited |

### 🟡 MEDIUM 严重度

| # | 差异点 | 说明 | 修复状态 |
|---|--------|------|----------|
| 13 | DebugAware/MetricsAware 缺失 | 算子无法接收引擎注入的 debug/metrics | ✅ |
| 14 | DataFrame row 模式无 validateValue | ColumnFrame 有但 DataFrame 没有 | ✅ |
| 15 | applyOutput error 不记录 stats/metrics | Go 在 ApplyOutput 错误时调用 RecordError | ✅ |
| 16 | /stats 无 snapshot 返回 200 | Go 返回 503 | ✅ |
| 17 | trace skipped 字段始终输出 | Go 用 omitempty 不输出 false | ✅ |
| 18 | /dag collapse 负值无校验 | Go 返回 400 | ✅ |
| 19 | /dag renderDAG 错误无 try-catch | 可能 500 无格式化 | ✅ |
| 20 | validateDeps 遇第一个就 throw | Go 收集所有缺失再报错 | ✅ |
| 21 | Fetcher 接口无 context 参数 | 无法配合超时/取消 | ⬜ 接口设计差异 |
| 22 | Lua 沙箱 PackageLib | Go 不加载 PackageLib | ⬜ LuaJ 依赖 PackageLib 编译 |
| 23 | SSRF DNS rebinding TOCTOU | 应用层 DNS 检查后 HttpClient 重新解析 | ⬜ 平台限制 |
| 24 | filter_condition 大数字科学计数法 | `1.0E20` vs `1e+20` | ✅ formatFloatG |
| 25 | merge_dedup normalizeKey vs map[any] | Number→Double 在非 JSON 场景仍有差异 | ⬜ JSON 输入下行为一致 |
| 26 | Codegen recall 暴露 recall 参数 | Go 不在签名中暴露 | ✅ |
| 27 | Codegen resources.py 缺 _params_schema | 缺失 + interval 默认值硬编码 0 | ✅ |

### 🟢 LOW 严重度

| # | 差异点 | 说明 | 修复状态 |
|---|--------|------|----------|
| 28 | Operator.Execute 缺 context 参数 | CancellationToken 注入 | ✅ |
| 29 | EngineMetrics null vs Nop | NopProvider 消除 null 检查 | ✅ |
| 30 | /health 方法校验 | Java 限 GET，Go 不限 | ⬜ Java 更严格 |
| 31 | body size 不可配置 | JSON 配置 max_request_body_size | ✅ |
| 32 | recall_static Init 引用拷贝 | Go 拷贝 slice，Java 直接存引用 | ✅ |
| 33 | redis_get Set 返回顺序 | Go 保序，Java HashSet 无序 | ⬜ Redis SMEMBERS 本身无序 |
| 34 | reorder_sort 排序稳定性 | Go 不稳定，Java TimSort 稳定 | ⬜ 已接受设计差异 |

## 第五轮审计 (2026-05-15)

全量独立重审。不依赖前轮结论，从源码逐行对比。发现 34 项差异。

### 🔴 HIGH 严重度（正确性/wire 不兼容）

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| 1 | OperatorOutput.setWarning 语义 | "first wins"：后续调用被忽略 (`operator_io.go:109`) | "last wins"：每次调用覆盖 (`OperatorOutput.java:14`) | ✅ |
| 2 | filter_condition 数值格式精度 | `%v` 完整精度 `1.23456789`→`"1.23456789"` | `%g` 6 位有效数字 `1.23456789`→`"1.23457"` | ✅ |
| 3 | Codegen operators.py 本地变量名 | `_params`（避免遮蔽 kwarg） | `params`（遮蔽同名参数） | ✅ |
| 4 | Server 错误响应格式 | 405/503 用 plain text | 全部 JSON `{"error":"..."}` | ⬜ 修 Go 侧 |
| 5 | HTTP metrics status label | `statusBucket()` → `"2xx"/"5xx"` | `String.valueOf(code)` → `"200"/"500"` | ✅ |
| 6 | TransformByLua 缺 MetricsAware | Go LuaOp 实现 SetMetricsProvider | Java 未实现此接口 | ✅ |
| 7 | transform_redis_set Schema 不对称 | Go 无 `fail_on_error` | Java 有 `fail_on_error` | ⬜ 有意超前 |

### 🟡 MEDIUM 严重度

| # | 差异点 | 说明 | 修复状态 |
|---|--------|------|----------|
| 8 | Body 超限 HTTP 状态码 | Go 400 vs Java 413 | ⬜ 登记 Go 侧 |
| 9 | Body size 配置来源 | Go 仅 programmatic API；Java JSON config | ⬜ 已接受 |
| 10 | HTTP histogram 桶定义 | Go 12 个显式桶；Java null（无桶） | ✅ |
| 11 | 热加载首次 spurious reload | 两侧都有 lastMod=0 导致首 tick 必触发 | ✅ Java + 登记 Go |
| 12 | lastReloadDurationNs 失败路径 | Go 仅成功时记录；Java 失败时也被覆写 | ✅ |
| 13 | `__init__.py` 生成格式 | Go 逐行 import；Java 分组 import | ✅ |
| 14 | merge_dedup key 等价性 | Go raw `any` 相等；Java normalizeKey→Double | ⬜ 已接受 |
| 15 | DataFrame AddItem 无 validateValue | Go 在 additions 调 validateValue；Java 跳过 | ✅ |
| 16 | Lua pool 重置策略 | Go baseline 快照/恢复；Java 仅 nil usedKeys | ✅ |
| 17 | resource_lookup 大浮点格式 | Go `FormatFloat('f')` vs Java `Double.toString()` | ✅ |
| 18 | redis_get key suffix 大浮点 | Go `fmt.Sprint`→`"1e+20"` vs Java→`"1.0E20"` | ✅ |
| 19 | reorder_shuffle tie-breaking | Go uint64 无符号；Java Long.compare 有符号 | ✅ |
| 20 | Codegen 缺 Metadata Contract 文档节 | Go 从源码注释解析生成；Java 完全缺失 | ✅ |
| 21 | Codegen string escape | Go `%q` vs Java 手动 5 种转义 | ✅ |
| 22 | 无 caller-driven timeout/cancel | Go `Execute(ctx,req)` vs Java `execute(common,items)` | ⬜ 平台限制 |
| 23 | Cancel 传播 5ms 延迟 | Go channel select 即时；Java polling | ⬜ 平台限制 |
| 24 | HTTP 超时缺失 | Go 可配置 Read/Write/Idle；Java 无 | ⬜ 平台限制 |
| 25 | SSRF dial-time TOCTOU | Go TCP dial 级拦截；Java DNS check→connect 时间窗 | ⬜ 平台限制 |
| 26 | Fetcher 接口无 context | Go `func(ctx)(any,error)`；Java `Object fetch()` | ⬜ 平台限制 |

### 🟢 LOW 严重度

| # | 差异点 | 说明 | 修复状态 |
|---|--------|------|----------|
| 27 | Health endpoint 方法限制 | Go 不限方法；Java 仅 GET | ⬜ 已接受 |
| 28 | reorder_sort 稳定性 | Go 不稳定；Java TimSort 稳定 | ⬜ 已接受 |
| 29 | Lua pool Close 不关闭 Globals | Go 遍历关闭所有 state；Java 仅 clear queue | ⬜ 已接受 |
| 30 | Codegen `__init__.py` header 注释 | 措辞微差 | ✅ |
| 31 | Body size int vs int64 | Go int64；Java int（2GB 上限） | ✅ |
| 32 | redis_get SMEMBERS 返回顺序 | Go 保序；Java HashSet 无序 | ⬜ 已接受 |
| 33 | /dag collapse=0 传递方式 | Go 不传 option；Java 始终传 0 | ⬜ 已接受 |
| 34 | Resource injection 模式 | Go per-request context；Java per-engine field | ⬜ 已接受 |

### 上轮修复复验

| 上轮项 | 声称 ✅ | 本轮判定 |
|--------|---------|----------|
| 第四轮 #9 Lua pool 状态清理 | ✅ | ⚠️ usedKeys 方案仅清理新增 key，不恢复被覆写的 baseline（如 `math = 1`） |
| 第四轮 #13 DebugAware/MetricsAware | ✅ | ⚠️ 接口+引擎注入已实现，但 TransformByLua 未实现 MetricsAware 接口 |
| 第四轮 #24 filter_condition 科学计数法 | ✅ | ⚠️ 科学计数法对齐了，但 `%g` 的 6 位精度截断问题未解决 |
| 第四轮 #2 filter_condition `<nil>` | ✅ | ✓ 确认修复 |
| 第四轮 #1 Codegen 双重 "Op" | ✅ | ✓ 确认修复 |
| 第四轮 #6 Server body streaming | ✅ | ✓ 确认修复 |
| 第四轮 #7 validateResourceDeps | ✅ | ✓ 确认修复 |
| 第四轮 #8 Engine partial result | ✅ | ✓ 确认修复 |

### 讨论决策

| # | 决策 | 备注 |
|---|------|------|
| 1 | 修 Java | setWarning 改为 first-wins |
| 2 | 修 Java | 改用 `Double.toString()` + 整数检测 |
| 3 | 修 Java | `params` → `_params` |
| 4 | 修 Go | Go `http.Error` plain text 是疏忽。已记录 `.llmdoc-tmp/go-server-pending-fixes.md` |
| 5 | 修 Java | 加 `statusBucket()` 桶化 |
| 6 | 修 Java | TransformByLua 补 MetricsAware 接口 |
| 7 | 已接受 | Java 有意超前实现 `fail_on_error`，待 Go 侧补齐 |
| 8 | 接受 + 登记 Go | Java 413 更合理。Go 侧待修，已记录 `.llmdoc-tmp/go-server-pending-fixes.md` |
| 9 | 已接受 | 配置来源差异不冲突 |
| 10 | 修 Java | 补与 Go 相同的显式 histogram 桶 |
| 11 | 修 Java + 登记 Go | 两侧都有 spurious reload。Go 侧已记录 `.llmdoc-tmp/go-server-pending-fixes.md` |
| 12 | 修 Java | lastReloadDurationNs 仅成功路径记录 |
| 13 | 修 Java | `__init__.py` 改为逐行 import 对齐 Go |
| 14 | 已接受 | JSON 输入场景两侧行为一致 |
| 15 | 修 Java | AddItem 补 validateValue |
| 16 | 修 Java | Lua pool 补 baseline 快照/恢复 |
| 17/18 | 修 Java | 写工具库复刻 Go `fmt.Sprint`/`FormatFloat` 格式化 |
| 19 | 修 Java | `Long.compareUnsigned` |
| 20 | 修 Java | 算子加注释 + 写注释解析器 + Codegen 集成 |
| 21 | 修 Java | 对齐 string escape |
| 22-26 | 暂不修 | 平台限制 |
| 27 | 已接受 | Java 仅 GET 更严格，无害 |
| 28 | 已接受 | 排序稳定性差异不影响业务 |
| 29 | 已接受 | LuaJ Globals 无显式 close，GC 回收 |
| 30 | 修 Java | 顺手对齐 header 注释措辞 |
| 31 | 修 Java | `int` → `long` |
| 32 | 已接受 | Redis SMEMBERS 本身无序 |
| 33 | 已接受 | collapse=0 功能等价 |
| 34 | 已接受 | 设计差异：Go per-request context vs Java per-engine field |

### 待修项汇总

**修 Java 侧（20 项）：**
- #1 setWarning first-wins（1 行）
- #2 filter_condition 精度 → `Double.toString()`（~5 行）
- #3 Codegen `_params`（3 处替换）
- #5 HTTP metrics label 桶化（~15 行）
- #6 TransformByLua 补 MetricsAware（~20 行）
- #10 HTTP histogram 桶（低）
- #11 热加载 spurious reload（低）
- #12 lastReloadDurationNs 失败路径（1 行）
- #13 `__init__.py` import 格式（~10 行）
- #15 AddItem validateValue（~10 行）
- #16 Lua pool baseline 恢复（中等）
- #17/18 Go 格式化工具库 + resource_lookup/redis_get 对齐（中等）
- #19 reorder_shuffle `Long.compareUnsigned`（1 行）
- #20 Codegen Metadata Contract 文档节：算子注释 + 解析器（中等）
- #21 Codegen string escape（低）
- #30 `__init__.py` header 注释（1 行）
- #31 Body size `int` → `long`（低）

**修 Go 侧（3 项，已记录 `.llmdoc-tmp/go-server-pending-fixes.md`）：**
- #4 Server 错误响应格式 plain text → JSON
- #8 Body 超限状态码 400 → 413
- #11 热加载 spurious reload

**已接受（9 项）：** #7, #9, #14, #27, #28, #29, #32, #33, #34

**平台限制（5 项）：** #22-26

## 第六轮审计 (2026-05-15)

第五轮修复后全面独立重审。三个维度并行审计（Engine/DataFrame/Runtime、18 算子、Server/Codegen/Registry/Resource）。发现 19 项差异（含 5 项 HIGH）。

### 🔴 HIGH 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| 1 | globalDebug 单向覆盖 | `WithDebug(false)` 覆盖所有算子为 false | `if (globalDebug && !opCfg.debug)` 仅 false→true | ⬜ 登记 Go（Java 正确）|
| 2 | filter_condition 大浮点格式 (>=1e15) | `%v` → `"1e+20"` | `Double.toString(1e20)` → `"1.0E20"` | ✅ GoFormat.sprint |
| 3 | Registry.all() 排序 | 按名称字母序 | 按注册插入序 (LinkedHashMap) | ✅ |
| 4 | Codegen `__init__.py` 格式 | `—` (em dash) + 尾逗号 | `--` (double dash) + 无尾逗号 | ✅ |
| 5 | statusBucket >=600 | `code >= 500` → `"5xx"` 含 600+ | `[500,600)` → `"5xx"`，>=600 → `"other"` | ✅ |

### 🟡 MEDIUM 严重度

| # | 差异点 | 说明 | 修复状态 |
|---|--------|------|----------|
| 6 | MetricsAware 注入条件 | Java `provider != null` 才注入；Go 始终注入 (NopProvider) | ✅ |
| 7 | applyOutput error duration | Go 不含 apply 时间；Java 含 apply 时间 | ✅ |
| 8 | TransformByLua Init 校验函数 | Go 延迟到 Execute；Java 在 Init 校验函数存在性 | ⬜ 登记 Go（Java 更好）|
| 9 | Codegen float 默认值格式化 | Go `%g`（6 位）；Java `Double.toString`（完整精度）| ✅ GoFormat.formatG |
| 10 | reorder_shuffle anyToString | Go `%g`；Java 自实现 formatFloatG 基于 Double.toString 转换 | ✅ GoFormat.formatG |
| 11 | Registry.Register 类型校验 | Go 校验 IsValidOperatorType 且 panic；Java 仅检查 null | ✅ enum 字段已足够 |
| 12 | Codegen string escape 覆盖 | Go `%q` 全 Unicode；Java 仅 8 种显式转义 | ✅ \uXXXX |
| 13 | reload Histogram 桶 | Go 8 桶 [0.001..5.0]；Java reload histogram 传 null | ✅ |
| 14 | DAGVisualizer sanitizeMermaidID | Go 仅替换 `-.` 和空格；Java 替换全部非字母数字字符 | ⬜ 登记 Go（Java 更好）|

### 🟢 LOW 严重度

| # | 差异点 | 说明 | 修复状态 |
|---|--------|------|----------|
| 15 | OpTrace startTime 类型 | Go `time.Time` 绝对时间 vs Java `nanoTime` 相对 | ⬜ 已接受 |
| 16 | validateSourcesOrder 异常类型 | Java 用 IllegalArgumentException 非 ConfigError | ✅ |
| 17 | Codegen operators.py 空行/注释措辞 | 微差 | ✅ |
| 18 | Codegen resources.py 生成条件 | Go 当有资源时生成；Java 需 CLI 参数指定 | ✅ ResourceRegistry |
| 19 | ResourceManager.Stop 可重用 | Java 重置 started=false（可 restart）；Go 不重置 | ⬜ 已接受 |

### 之前已登记/已接受项复验

| 项 | 原状态 | 复验结果 |
|---|--------|---------|
| Server 错误格式 text→JSON | 登记 Go 待修 | ✓ 仍存在 |
| Body 超限 400→413 | 登记 Go 待修 | ✓ 仍存在 |
| watchConfig spurious reload | 登记 Go 待修 | ✓ Go 仍有 |
| redis_set fail_on_error | Go 侧待补 | ✓ 仍存在 |
| Fetcher 缺 context | 平台限制 | ✓ |
| Cancel 5ms 延迟 | 平台限制 | ✓ |
| HTTP 超时缺失 | 平台限制 | ✓ |
| merge_dedup key 归一化 | 已接受 | ✓ JSON 输入等价 |
| reorder_sort 稳定性 | 已接受 | ✓ |
| Partial result 设计 | 已接受 | ✓ |

### 第五轮修复复验

| 第五轮项 | 声称 ✅ | 本轮判定 |
|----------|---------|----------|
| #2 filter_condition 精度 | ✅ | ⚠️ `Double.toString(1e20)` = `"1.0E20"` 而非 Go 的 `"1e+20"`，>=1e15 仍有差异 |
| #4 `__init__.py` 格式 | ✅ | ⚠️ header 字符 `--` vs `—`，`__all__` 缺尾逗号 |
| #5 statusBucket | ✅ | ⚠️ >=600 分类不同 |
| #10 reorder_shuffle formatFloatG | ✅ | ⚠️ 自实现 formatFloatG 与 Go %g 可能在边界值不同 |
| #1 setWarning first-wins | ✅ | ✓ 确认修复 |
| #3 Codegen _params | ✅ | ✓ 确认修复 |
| #6 TransformByLua MetricsAware | ✅ | ✓ 确认修复 |
| #15 DataFrame validateValue | ✅ | ✓ 确认修复 |
| #16 Lua pool baseline | ✅ | ✓ 确认修复 |
| #17/18 GoFormat | ✅ | ✓ 确认修复 |
| #19 Long.compareUnsigned | ✅ | ✓ 确认修复 |
| #20 Metadata Contract | ✅ | ✓ 确认修复 |
| #31 maxRequestBodyBytes long | ✅ | ✓ 确认修复 |

### 讨论决策

| # | 决策 | 备注 |
|---|------|------|
| 1 | 登记 Go | Java 逻辑正确：全局 debug 只做提升不做抑制 |
| 2 | 修 Java | 用 GoFormat.sprint(v) 替代 formatValue |
| 3 | 修 Java | all() 按名称排序 |
| 4 | 修 Java | em dash + 尾逗号对齐 Go |
| 5 | 修 Java | code >= 500 → "5xx" |
| 6 | 修 Java | 始终注入 MetricsAware（NopProvider 兜底）|
| 7 | 修 Java | 用 execute 阶段 duration |
| 8 | 登记 Go | Java Init 校验更好，Go 应对齐 |
| 9 | 修 Java | 用 GoFormat.formatG |
| 10 | 修 Java | 用 GoFormat.formatG 替代自实现 |
| 11 | 修 Java | 补枚举有效性校验 |
| 12 | 修 Java | 补 Unicode 控制字符 \uXXXX 转义 |
| 13 | 修 Java | 传入与 Go 相同的 8 桶 |
| 14 | 登记 Go | Java 更健壮，Go 应对齐 |
| 15 | 已接受 | wire 层面无影响 |
| 16 | 修 Java | 改为 PineErrors.ConfigError |
| 17 | 修 Java | 对齐措辞和空行 |
| 18 | 修 Java | 从 Registry 自动生成 resources.py |
| 19 | 已接受 | Java 更灵活，不影响实际使用 |

### 待修项汇总

**修 Java 侧（14 项）：**
- #2 filter_condition → GoFormat.sprint（~5 行）
- #3 Registry.all() 排序（~3 行）
- #4 __init__.py em dash + 尾逗号（~3 行）
- #5 statusBucket code>=500（1 行）
- #6 MetricsAware 始终注入（~3 行）
- #7 applyOutput error duration（~5 行）
- #9 Codegen float → GoFormat.formatG（~3 行）
- #10 reorder_shuffle → GoFormat.formatG（~10 行）
- #11 Registry 类型校验（~5 行）
- #12 Codegen string escape Unicode（~10 行）
- #13 reload Histogram 桶（~3 行）
- #16 validateSourcesOrder → ConfigError（1 行）
- #17 Codegen doc 措辞/空行（~5 行）
- #18 Codegen resources.py 从 Registry 生成（中等）

**登记 Go 侧（3 项）：**
- #1 globalDebug 只做提升
- #8 TransformByLua Init 校验函数存在性
- #14 sanitizeMermaidID 替换全部非安全字符

**已接受（2 项）：** #15, #19

## 第七轮审计 (2026-05-15)

### 第六轮修复复验

全部 ✅ 项确认有效。之前登记 Go 待修的 3 项（TransformByLua Init 校验、sanitizeMermaidID、Server JSON 错误格式）已在 PR #12 / commit b2100f7 中修复完毕。globalDebug 经验证 Go 原本就正确。

### 新发现

#### 🔴 HIGH 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| H1 | ParallelExecutor shard 取消粒度 | `context.WithCancel` 创建 shard 级子 context，一 shard 失败 → cancel 传播至所有 running shard | 只有 `AtomicBoolean` 阻止尚未启动的 shard，已运行 shard 无取消信号 | ✅ shardToken |
| H2 | Panic/Error 恢复包装 | `executeWithRecovery` → `PanicError{stack}`；`fatalOnce` 包装为 `ExecutionError` | `RuntimeException("panic in shard", t)` 无结构化包装 | ✅ PineErrors.ExecutionError |
| H3 | Server: ValidationError → HTTP 状态码 | Engine 返回 `*ValidationError` 但 server 统一映射 500 | Engine 抛 `ValidationError extends IAE` → 映射 400 | ⬜ 登记 Go（Java 正确）|

#### 🟡 MEDIUM 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| M1 | GoFormat.sprint 整数值 float ≥ 1e6 | `fmt.Sprint(float64(1e6))` → `"1e+06"` | `GoFormat.sprint(1000000.0)` → `"1000000"` (floor 快速路径上限 1e18) | ✅ 上限改 1e6 + formatG 补科学记数转换 |
| M2 | Trace: error 路径 inputSnapshot | 不包含 inputSnapshot | 包含 inputSnapshot（更利于调试）| ⬜ 已接受（Java 更好）|
| M3 | redis_set: 错误透传 | 仅 log.Printf，无 SetWarning，无 fail_on_error | 支持 fail_on_error + stderr 回退 | ⬜ 登记 Go |
| M4 | redis_set Schema 不对称 | Schema 无 fail_on_error | Java 已有 fail_on_error | ⬜ 登记 Go |

#### 🟢 LOW 严重度

| # | 差异点 | 说明 | 修复状态 |
|---|--------|------|----------|
| L1 | ParallelExecutor 线程池选择 | Go goroutine vs Java ForkJoinPool.commonPool() | ⬜ 已接受 |
| L2 | Trace inputSnapshot on error | Java 包含更多调试信息 | ⬜ 已接受（M2 同项）|
| L3 | Engine debug log 输出 | Go 有 `[pine-debug]` JSON log，Java 无 | ⬜ 已接受（低优先级）|

### 第七轮决策

| # | 决策 | 备注 |
|---|------|------|
| H1 | 修 Java | 创建 shard 级 CancellationToken，对齐 Go 的 context.WithCancel 语义 |
| H2 | 修 Java | 用 PineErrors.ExecutionError 包装，对齐 Go 的结构化错误 |
| H3 | 登记 Go | Java 已正确（400），Go 应对齐 |
| M1 | 修 Java | sprint 上限 1e18→1e6，formatG 补 [1e6,1e7) 科学记数转换 |
| M2 | 已接受 | Java 行为更好，不改 |
| M3/M4 | 登记 Go | Java 有意超前，Go 待补 fail_on_error + SetWarning |
| L1-L3 | 已接受 | 平台差异或低优先级 |

### Go 待修项更新

| 项 | 状态 | 来源 |
|---|------|------|
| Server ValidationError → 400 | ⬜ 待修 | 第七轮 H3 |
| redis_set fail_on_error + SetWarning | ⬜ 待修 | 第六轮 + 第七轮 M3/M4 |


- Go 用 GopherLua + sync.Pool 做 Lua VM 池化和沙箱；Java 用 LuaJ
- Go Server 基于 net/http 支持完整 middleware 链和超时；Java 用 JDK 内置 com.sun.net.httpserver
- ColumnFrame 的 presence bitmap 用于区分 "字段不存在" vs "字段 = nil"
- transform_by_remote_pineapple 包含完整 SSRF 防护 (私有 IP 检测, 安全拨号)
- 结构化错误中 PanicError 区分 public Error() 和 DetailedError() (含 stack)
- merge_dedup 在 Go 中利用 map[any] 的原生类型相等性；Java 需特殊处理以保持语义一致
- /execute 响应格式（duration 单位、warnings 结构）是跨运行时客户端兼容性的关键契约点
- filter_condition: Go `%v` 对 float64 使用最短完整精度表示；Java `%g` 固定 6 位有效数字会截断
- OperatorOutput.SetWarning 的 first-wins/last-wins 影响 ParallelExecutor 多 shard warning 合并结果
- Codegen `_params` vs `params` 不会导致运行时错误（Python 允许局部变量与参数同名），但产出代码不同导致 CI diff 失败

## 第八轮审计 (2026-05-15)

### 第七轮修复复验

| 项 | 结论 |
|---|------|
| ParallelExecutor shardToken 取消传播 | ✓ 已实现，但发现 shardToken 不继承 parent token 取消（新 H2）|
| PanicError 包装改 ExecutionError | ✓ 已实现，但应为 PanicError（新 M4），且调度器层缺 PanicError 保留（新 H1）|
| GoFormat.sprint ≥1e6 → formatG | ✓ 验证正确 |
| Go PR #13 redis_set fail_on_error | ⚠️ Java 缺 SetWarning + 缺错误前缀包装（新 H5/H6）|

### 新发现

#### 🔴 HIGH 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| H1 | 调度器 PanicError 身份保留 | `scheduler.go:197` — PanicError 直接作为 fatalErr，不包装 ExecutionError | `Engine.java:322` — 所有 exception 统一包装 ExecutionError，PanicError 身份丢失 | ⬜ |
| H2 | ParallelExecutor shardToken 不继承 parent 取消 | `parallel.go:82` — `context.WithCancel(ctx)` 子级自动继承 | `ParallelExecutor.java:44,51` — shardToken 独立，已运行 shard 无法感知 parent 取消 | ⬜ |
| H3 | Execute API 无外部取消能力 | `pine.go:180` — `Execute(ctx, req)` 接受调用方 context | `Engine.java:174` — `execute(Map, List)` 无 token 参数 | ⬜ |
| H4 | ResourceAware 并发 data race | 资源通过 `context.Context` 传递，无共享可变状态 | `Engine.java:274-279` — 每次请求在共享实例上 setResourceProvider()，非 volatile 字段并发写 | ⬜ |
| H5 | redis_set 非致命路径缺 SetWarning | `redis_set.go:147` — `out.SetWarning(...)` | `TransformRedisSet.java:108` — 仅 stderr，无 setWarning | ⬜ |
| H6 | redis_set failOnError=true 缺前缀包装 | `redis_set.go:145` — `fmt.Errorf("transform_redis_set: ...")` | `TransformRedisSet.java:106` — 直接 `throw e` 无前缀 | ⬜ |
| H7 | ValidationError 响应体 schema 不一致 | 返回完整结构 `{common:null, items:null, error:"..."}` | 返回精简 `{error:"..."}` | ⬜ |
| H8 | /health 方法检查不一致 | 无方法检查，任何 method 返回 200 | 仅 GET，其余 405 | ⬜ |

#### 🟡 MEDIUM 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| M1 | reorder_shuffle anyToString 整数值 float fast-path | `%g` 统一格式化 → `"1e+06"` | `Long.toString` fast-path → `"1000000"` | ⬜ |
| M2 | validateSourcesOrder 错误类型 | → `*ValidationError` | → `PineErrors.ConfigError` | ⬜ |
| M3 | validateDataParallel 错误类型 | → `*ValidationError` | → `IllegalArgumentException` | ⬜ |
| M4 | ParallelExecutor Throwable 包装类型 | panic → `PanicError{stack}` | non-Exception Throwable → `ExecutionError` (应为 PanicError) | ⬜ |
| M5 | /dag 端点格式验证缺失 | 无效 format → 返回错误 | 静默回退 DOT | ⬜ |
| M6 | max_request_body_size intValue 截断 | int64 完整 | `intValue()` 截断后赋 long | ⬜ |
| M7 | lastReloadDurationNs 初始值 | 保持 0（初始 load 不算 reload） | 设为初始加载耗时 | ⬜ |
| M8 | PanicError 详细日志缺失 | server 用 `DetailedError()` 记录 stack | 不记录 | ⬜ |
| M9 | execute 响应 JSON 字段顺序 | warnings→trace→error | warnings→error→trace | ⬜ |

#### 🟢 LOW 严重度

| # | 差异点 | 修复状态 |
|---|--------|----------|
| L1 | awaitWithCancel 5ms 轮询 vs ctx.Done O(1) | ⬜ 已接受（平台限制）|
| L2 | fail_on_error 解析风格 (Boolean vs "true" string) | ⬜ |
| L3 | Lua 池回收策略差异 | ⬜ 已接受 |
| L4 | Go writeJSON 尾部换行 | ⬜ 已接受 |

