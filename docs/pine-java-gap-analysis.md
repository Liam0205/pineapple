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
| H1 | 调度器 PanicError 身份保留 | `scheduler.go:197` — PanicError 直接作为 fatalErr，不包装 ExecutionError | `Engine.java:322` — 所有 exception 统一包装 ExecutionError，PanicError 身份丢失 | ✅ |
| H2 | ParallelExecutor shardToken 不继承 parent 取消 | `parallel.go:82` — `context.WithCancel(ctx)` 子级自动继承 | `ParallelExecutor.java:44,51` — shardToken 独立，已运行 shard 无法感知 parent 取消 | ✅ |
| H3 | Execute API 无外部取消能力 | `pine.go:180` — `Execute(ctx, req)` 接受调用方 context | `Engine.java:174` — `execute(Map, List)` 无 token 参数 | ✅ |
| H4 | ResourceAware 并发 data race | 资源通过 `context.Context` 传递，无共享可变状态 | `Engine.java:274-279` — 每次请求在共享实例上 setResourceProvider()，非 volatile 字段并发写 | ✅ |
| H5 | redis_set 非致命路径缺 SetWarning | `redis_set.go:147` — `out.SetWarning(...)` | `TransformRedisSet.java:108` — 仅 stderr，无 setWarning | ✅ |
| H6 | redis_set failOnError=true 缺前缀包装 | `redis_set.go:145` — `fmt.Errorf("transform_redis_set: ...")` | `TransformRedisSet.java:106` — 直接 `throw e` 无前缀 | ✅ |
| H7 | ValidationError 响应体 schema 不一致 | 返回完整结构 `{common:null, items:null, error:"..."}` | 返回精简 `{error:"..."}` | ✅ |
| H8 | /health 方法检查不一致 | 无方法检查，任何 method 返回 200 | 仅 GET，其余 405 | ✅ 已接受 |

#### 🟡 MEDIUM 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| M1 | reorder_shuffle anyToString 整数值 float fast-path | `%g` 统一格式化 → `"1e+06"` | `Long.toString` fast-path → `"1000000"` | ✅ |
| M2 | validateSourcesOrder 错误类型 | → `*ValidationError` | → `PineErrors.ConfigError` | ✅ |
| M3 | validateDataParallel 错误类型 | → `*ValidationError` | → `IllegalArgumentException` | ✅ |
| M4 | ParallelExecutor Throwable 包装类型 | panic → `PanicError{stack}` | non-Exception Throwable → `ExecutionError` (应为 PanicError) | ✅ |
| M5 | /dag 端点格式验证缺失 | 无效 format → 返回错误 | 静默回退 DOT | ✅ |
| M6 | max_request_body_size intValue 截断 | int64 完整 | `intValue()` 截断后赋 long | ✅ |
| M7 | lastReloadDurationNs 初始值 | 保持 0（初始 load 不算 reload） | 设为初始加载耗时 | ✅ |
| M8 | PanicError 详细日志缺失 | server 用 `DetailedError()` 记录 stack | 不记录 | ✅ |
| M9 | execute 响应 JSON 字段顺序 | warnings→trace→error | warnings→error→trace | ✅ |

#### 🟢 LOW 严重度

| # | 差异点 | 修复状态 |
|---|--------|----------|
| L1 | awaitWithCancel 5ms 轮询 vs ctx.Done O(1) | ⬜ 已接受（平台限制）|
| L2 | fail_on_error 解析风格 (Boolean vs "true" string) | ✅ |
| L3 | Lua 池回收策略差异 | ⬜ 已接受 |
| L4 | Go writeJSON 尾部换行 | ⬜ 已接受 |

### 第八轮决策

所有 17 项已在 commit `65f3fd1` 中修复：
- H1-H7, M1-M9, L2：修 Java
- H8：已接受（Java 限 GET 更严格）
- L1, L3, L4：已接受（平台差异）

## 第九轮审计 (2026-05-15)

### 第八轮修复复验

| 项 | 结论 |
|---|------|
| H1 PanicError 身份保留 | ✓ 确认修复 |
| H2 shardToken 继承 parent | ✓ 确认修复 |
| H3 execute(CancellationToken) 重载 | ✓ 确认修复 |
| H4 ResourceAware 注入时机 | ✓ 确认修复 |
| H5/H6 redis_set warning+前缀 | ✓ 确认修复 |
| H7 ValidationError 完整响应 | ⚠️ 修复了结构（common+items+error），但发现 null vs empty 差异（新 H5）和缺 "pine:" 前缀（新 H6）|
| M1 anyToString fast-path | ✓ 确认修复 |
| GoFormat.sprint ≥1e6 转科学记号 | ✓ 确认修复；但发现小数方向 [1e-4,1e-3) 仍有问题（新 H1）|

### 新发现

#### 🔴 HIGH 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| H1 | GoFormat.formatG 小数科学记号阈值 [1e-4, 1e-3) | `%g(0.0001)` = `"0.0001"` (Go 仅 exponent < -4 用科学记号) | `Double.toString(0.0001)` = `"1.0E-4"` → 进入科学分支 → `"1e-04"` | ⬜ |
| H2 | GoFormat.formatFloatF 非整数精度错误 | `FormatFloat(0.1,'f',-1,64)` = `"0.1"` (shortest round-trip) | `BigDecimal(0.1).toPlainString()` = `"0.1000...055..."` (exact binary) | ⬜ |
| H3 | TransformRedisSet.toStringList 格式化 | `fmt.Sprint(nil)` = `"<nil>"`; `fmt.Sprint(1e6)` = `"1e+06"` | `String.valueOf(null)` = `"null"`; `String.valueOf(1e6)` = `"1000000.0"` | ⬜ |
| H4 | Server: 畸形 JSON Body → 500 (应为 400) | JSON 解析失败 → 400 `{"error":"invalid request: ..."}` | `JsonProcessingException` 被 `catch(Exception)` → 500 | ⬜ |
| H5 | Server: ValidationError 响应 common/items null vs empty | `{"common":null,"items":null,"error":"pine: ..."}` | `{"common":{},"items":[],"error":"..."}` | ⬜ |
| H6 | 全局错误消息缺 "pine:" 前缀 | `"pine: validation error: ..."`, `"pine: execution error in ..."` | 无前缀，直接裸消息 | ⬜ |
| H7 | Engine: allDone.await() 无超时保护 | context.Context 天然支持 timeout；goroutine select 快速退出 | `allDone.await()` 无限阻塞，算子挂起 → 线程永不返回 | ⬜ |
| H8 | transform_by_lua: 无法中断单次 Lua 调用 | `L.SetContext(ctx)` 每条指令边界检查取消 | 仅 item 循环间隙检查 token；common 模式完全不检查 | ⬜ |

#### 🟡 MEDIUM 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| M1 | GoFormat.formatG: Infinity 格式 | `"+Inf"` / `"-Inf"` | `"Infinity"` / `"-Infinity"` | ⬜ |
| M2 | GoFormat.sprint: List 格式 | `fmt.Sprint([]string{"a","b"})` = `"[a b]"` | `list.toString()` = `"[a, b]"` | ⬜ |
| M3 | Server: /health 方法限制 | 接受任何 HTTP 方法 | 仅 GET，其他 405 | ⬜ |
| M4 | reorder_sort 排序稳定性 | `sort.Slice` 不稳定 | `Arrays.sort` TimSort 稳定 | ⬜ |
| M5 | awaitWithCancel 5ms 轮询延迟 | goroutine select 零延迟 | 每前驱完成后最多 5ms 延迟，深度 DAG 累加 | ⬜ |
| M6 | 外部取消不能终止前驱等待 | `select {case <-ctx.Done()}` 立即退出 | awaitWithCancel 不检查外部 token | ⬜ |
| M7 | PanicError 语义范围差异 | 所有 panic (含 NPE) → PanicError | 仅 Error → PanicError；RuntimeException → ExecutionError | ⬜ |
| M8 | transform_by_remote_pineapple SSRF TOCTOU | DNS 检查在 TCP DialContext 层 | 检查与连接有时间窗口 | ⬜ |
| M9 | transform_by_lua debug 日志泄漏 | 输出字段计数摘要 | 输出完整 common 数据 map | ⬜ |
| M10 | Server: JSON 响应尾部 `\n` | `json.Encoder.Encode` 追加 `\n` | 无尾部换行 | ⬜ |
| M11 | Server: /dag collapse 错误消息不一致 | 统一 `"collapse must be a non-negative integer"` | 两条独立路径，消息文本不同 | ⬜ |

#### 🟢 LOW 严重度

| # | 差异点 | 修复状态 |
|---|--------|----------|
| L1 | transform_by_lua 沙箱多加载 PackageLib（入口已抹除） | ⬜ 已接受 |
| L2 | transform_by_lua pool 关闭：Go 显式 close vs Java GC | ⬜ 已接受 |
| L3 | observe_log JSON key 排序（Go 字母序 vs Java 插入序） | ⬜ |
| L4 | observe_log 输出目标（Go log.Printf vs Java System.err） | ⬜ 已接受 |
| L5 | ColumnFrame validateValue 错误不含字段名 | ⬜ |
| L6 | applyOutput 错误消息缺上下文分层 | ⬜ |
| L7 | transform_redis_set toStringList 对非 List 类型更宽松 | ⬜ |
| L8 | Codegen __init__.py import 排序可能因 locale 不同 | ⬜ |
| L9 | Codegen 控制字符 escape 格式微差 (`\x00` vs `\0`) | ⬜ |

### 第九轮决策

| # | 决策 | 备注 |
|---|------|------|
| H1 | 修 Java | formatG 检测 exponent=-4 时转回十进制 |
| H2 | 修 Java | formatFloatF 改用 Double.toString + 剥离科学记号 |
| H3 | 修 Java | toStringList 改用 GoFormat.sprint(elem) |
| H4 | 修 Java | 加 catch JsonProcessingException → 400 |
| H5 | 修 Java | ValidationError 路径 common=null, items=null |
| H6 | 修 Java | 错误类型 getMessage() override 加 "pine:" 前缀 |
| H7 | 修 Java | allDone.await → CompletableFuture（联动 M5/M6）|
| H8 | 已接受 | LuaJ 平台限制，无指令级中断 |
| M1 | 修 Java | Infinity → "+Inf"/"-Inf"；NaN → "NaN" |
| M2 | 修 Java | List → "[a b]" 格式（空格分隔无逗号）|
| M3 | 已接受 | Java 限 GET 更严格，无害 |
| M4 | 已接受 | 排序稳定性不影响业务 |
| M5 | 修 Java | 方案 1：CompletableFuture 替代 CountDownLatch，零延迟唤醒 |
| M6 | 修 Java | 联动 M5，CompletableFuture.orTimeout + token check |
| M7 | 修 Java | 方案 B：新增 OperatorException checked exception，改 Operator 接口签名，18 个算子 throws 声明；其余 RuntimeException → PanicError |
| M8 | 已接受 | 平台限制（Java HttpClient 无 dial-time hook）|
| M9 | 修 Java | debug 日志改为输出字段计数摘要，不泄漏数据 |
| M10 | 已接受 | 尾部换行无实质影响 |
| M11 | 修 Java | 统一为 "collapse must be a non-negative integer" |
| L1-L9 | 暂不修 | 已接受或低优先级 |

### 待修项汇总

**修 Java 侧（14 项）：**

格式化修复（H1/H2/M1/M2）：
- GoFormat.formatG：小数 [1e-4,1e-3) 转十进制；Infinity→"+Inf"/"-Inf"
- GoFormat.formatFloatF：BigDecimal → Double.toString 剥离科学记号
- GoFormat.sprint：List 格式 "[a b]"（空格分隔）

算子修复（H3/M9）：
- TransformRedisSet.toStringList → GoFormat.sprint
- TransformByLua debug 日志 → 字段计数摘要

Server 修复（H4/H5/M11）：
- catch JsonProcessingException → 400
- ValidationError 路径 common=null, items=null
- /dag collapse 错误消息统一

错误体系重构（H6/M7）：
- PineErrors 各类型加 "pine:" 前缀
- 新增 OperatorException + 改 Operator 接口 + 18 算子 throws

调度器重构（H7/M5/M6）：
- CountDownLatch → CompletableFuture
- 外部 token cancel 联动
- allDone 超时保护

## 第十轮审计 (2026-05-15)

### 第九轮修复复验

| 项 | 结论 |
|---|------|
| H1 GoFormat.formatG 小数阈值 [1e-4,1e-3) | ✓ 确认修复 |
| H2 GoFormat.formatFloatF 非整数精度 | ✓ 确认修复 |
| H3 TransformRedisSet.toStringList → GoFormat.sprint | ✓ 确认修复 |
| H4 Server JSON Body → 400 | ✓ 确认修复 |
| H5 ValidationError common/items null | ✓ 确认修复 |
| H6 全局错误消息 "pine:" 前缀 | ⚠️ RegistryError 遗漏（新 H1）|
| H7 CompletableFuture 调度器 | ✓ 确认修复 |
| M1 GoFormat.sprint ≥1e6 formatG | ✓ 确认修复 |
| M2 GoFormat.sprint Infinity → "+Inf"/"-Inf" | ✓ 确认修复 |
| M5 CompletableFuture 零延迟 | ✓ 确认修复 |
| M6 外部 token cancel 联动 | ✓ 确认修复 |
| M7 OperatorException checked exception | ⚠️ RecallResource 遗漏（新 M2）|
| M9 TransformByLua debug 日志 | ✓ 确认修复 |
| M11 /dag collapse 消息统一 | ✓ 确认修复 |

### 新发现

经独立重审，排除前轮误报后确认 7 项有效差异。

#### 🔴 HIGH 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| H1 | RegistryError 缺 "pine:" 前缀 | `"pine: registry error [op]: msg"` (`errors.go:20`) | `"operator \"op\": msg"` (PineErrors.java:27) — 无 "pine:" 前缀，无 "registry error" 标签 | ✅ |
| H2 | PanicError getMessage() 仅输出类名 | `"pine: panic in operator %q: %v"` 含完整 panic 值 | `"... " + getCause().getClass().getSimpleName()` 仅类名（如 "NullPointerException"），丢失具体消息 | ✅ |

#### 🟡 MEDIUM 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| M1 | data_parallel 校验消息缺类型信息 | `"data_parallel=N is only supported for Transform operators, got recall"` 含实际类型 | `"data_parallel=N is only supported for Transform operators"` 无 "got xxx" | ✅ |
| M2 | RecallResource 抛 IllegalStateException → PanicError | `return error` → scheduler 包装为 ExecutionError | `throw new IllegalStateException(...)` → RuntimeException → 被 Engine 包装为 PanicError | ✅ |

#### 🟢 LOW 严重度

| # | 差异点 | 说明 | 修复状态 |
|---|--------|------|----------|
| L1 | GoFormat.formatG(-0.0) 返回 "0" | Go `fmt.Sprintf("%g", -0.0)` = `"-0"` 保留符号位；Java `d == 0` 为 true → 返回 `"0"` | ✅ |
| L2 | TransformByLua debug 日志前缀格式 | Go `[pine:debug] operator="name"` (冒号 + operator= 标签)；Java `[pine-debug] name` (连字符 + 无标签) | ✅ |
| L3 | observe_log JSON key 排序 | Go `encoding/json` 对 map key 字母序排序；Java Jackson 保持 LinkedHashMap 插入序 | ✅ |

### 排除项（验证为无效）

| 原始项 | 排除原因 |
|--------|----------|
| /stats JSON 字段顺序 | JSON 规范不要求 key 有序，两侧数据相同 |
| Codegen toPythonLiteral 差异 | 功能等价：Java 已用 GoFormat.formatG 对齐 Go %g |
| Server reload 时序问题 | 两侧均 2s 轮询 + mtime 检测，逻辑相同 |
| GoFormat.sprint 数组处理 | Java 实现正确，格式 "[a b]" 已对齐 Go |
| Engine 外层 catch 双重包装 | 内层逻辑 return 后外层 catch 仅兜底基础设施异常，无双重包装 |

### 第十轮决策

全部 7 项修复 Java 侧：
- H1: RegistryError.getMessage() 重写，对齐 Go `"pine: registry error [op]: msg"` 格式
- H2: PanicError.getMessage() 改用 `getCause().getMessage()` 输出完整信息（null 时回退 toString）
- M1: data_parallel 校验消息补 `", got <type>"` 和 `"$metadata.common_output"` 及 `"(type \"xxx\" does not)"` 对齐 Go
- M2: RecallResource 全部 IllegalStateException → OperatorException
- L1: formatG(-0.0) 检测 IEEE 754 负零位模式，返回 `"-0"`
- L2: TransformByLua debug 前缀 `[pine-debug] name` → `[pine:debug] operator="name"`
- L3: ObserveLog snapshot/row 改用 TreeMap 对齐 Go 字母序 JSON 输出

## 第十一轮审计 (2026-05-15)

### 第十轮修复复验

| 项 | 结论 |
|---|------|
| H1 RegistryError "pine:" 前缀 | ✓ 确认修复 |
| H2 PanicError getMessage() 完整消息 | ✓ 确认修复 |
| M1 data_parallel 校验消息含类型 | ✓ 确认修复 |
| M2 RecallResource → OperatorException | ✓ 确认修复 |
| L1 formatG(-0.0) → "-0" | ✓ 确认修复 |
| L2 TransformByLua [pine:debug] | ✓ 确认修复 |
| L3 ObserveLog TreeMap | ✓ 确认修复 |

### 新发现

经三组并行独立审计（错误/调度器、18 算子、Server/Codegen/Config），确认 4 项有效新差异。

#### 🔴 HIGH 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| H1 | ApplyOutput 错误包装类型 | `scheduler.go:257` — applyErr 始终包装为 `ExecutionError{Err: "apply output: <err>"}` | `Engine.java:373-387` — RuntimeException → PanicError（frame 抛 IOOBE/IAE 等成为 PanicError 而非 ExecutionError）| ✅ |

#### 🟡 MEDIUM 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| M1 | /execute warnings 缺 operator 前缀 | `pine.go:220` — `fmt.Errorf("operator %q: %w", w.Operator, w.Err)` → wire 含算子名 | `PineServer.java:252` — `w.err.getMessage()` → wire 无算子名 | ✅ |
| M2 | TransformByLua debug log 计数来源 | `lua.go:100` — `len(o.CommonInput)` (metadata 声明字段数) | `TransformByLua.java:92` — `input.rawCommon().size()` (DataFrame 全部字段数) | ✅ |
| M3 | TransformByLua 错误缺 item 索引 | `lua.go:149` — `"lua: item[%d]: %w"` 含失败条目索引 | `TransformByLua.java:107` — `"lua error: <msg>"` 无索引 | ✅ |

#### 🟢 LOW 严重度

| # | 差异点 | 说明 | 修复状态 |
|---|--------|------|----------|
| L1 | Trace duration_ms 精度 | Go 截断到微秒 (`Microseconds()/1000.0`)；Java 保留纳秒 (`ns/1e6`) | ⬜ 已接受 |
| L2 | Scheduler-level debug log 缺失 | Go 输出 per-operator `[pine-debug] operator=... duration=... input_size=... output_size=...`；Java 仅有 trace snapshot | ⬜ |
| L3 | validateSourcesOrder 错误消息措辞 | Go "declared after the current operator"；Java "appears later in the sequence" | ⬜ 已接受 |
| L4 | TransformRedisSet failOnError=true 缺日志 | Go 始终 `log.Printf` 后再 return error；Java 仅 throw 不 log | ⬜ |

### 排除项

| 项 | 理由 |
|---|------|
| /stats JSON key 顺序 | JSON 规范不要求 key 有序 |
| Server HTTP 超时 | com.sun.net.httpserver 平台限制（已知接受）|
| CancellationToken vs context | 平台限制（已知接受）|
| Debug trace 错误路径含 inputSnapshot | Java 行为更好（已知接受）|
| 外层 catch Goroutine panic 恢复 | Java 更健壮（已知接受）|

### 第十一轮决策

全部 4 项修复 Java 侧：
- H1: ApplyOutput catch block 改为始终包装 ExecutionError + "apply output:" 前缀消息，对齐 Go
- M1: /execute warnings 格式改为 `"operator \"name\": message"` 对齐 Go wire 格式
- M2: TransformByLua debug log 改用 `commonInput.size()` 计数 metadata 声明字段
- M3: TransformByLua item 循环中 catch LuaError 包装 `"lua: item[N]: message"` 含索引

## 第十二轮审计 (2026-05-15)

### 第十一轮修复复验

| 项 | 结论 |
|---|------|
| H1 ApplyOutput → ExecutionError | ✓ 确认修复 |
| M1 warnings operator 前缀 | ✓ 确认修复 |
| M2 TransformByLua debug commonInput.size() | ✓ 确认修复 |
| M3 TransformByLua item 索引 | ✓ 确认修复 |

### 新发现

经深度边界审计（Config/DataFrame/Frame/Registry/OperatorType），确认 3 项差异 + 1 项措辞。

#### 🟡 MEDIUM 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| M1 | Type violation 错误归类为 PanicError | `scheduler.go:172` — `fmt.Errorf("type violation: ...")` 是普通 error → ExecutionError | `Engine.java:322` — `new IllegalStateException(...)` (RuntimeException) → PanicError | ✅ 改为 OperatorException |

#### 🟢 LOW 严重度

| # | 差异点 | 说明 | 修复状态 |
|---|--------|------|----------|
| L1 | ParallelExecutor PanicError 用 "parallel-shard" 占位名 | Go 传入 `cop.Name`；Java API 不接收算子名 | ✅ 添加 operatorName 参数 |
| L2 | Init 错误未包装 RegistryError | Go `BuildOperator` 包装为 `RegistryError{Init failed: ...}`；Java 直接抛原始异常 | ✅ try-catch 包装 |
| L3 | ValidateOutput 消息格式（大小写/逗号） | Go: `"operator type Recall must not call [SetCommon SetItem]"`；Java: `"... recall ... [SetCommon, SetItem]"` | ✅ |

### 第十二轮决策

全部 4 项修复 Java 侧：
- M1: type violation 改用 OperatorException（被 catch 归为 ExecutionError）
- L1: ParallelExecutor.execute 添加 operatorName 参数
- L2: Registry.buildOperator 包装 Init 异常为 RegistryError
- L3: ValidateOutput 消息用首字母大写类型名 + 空格分隔 violations

## 第十三轮审计 (2026-05-15)

### 第十二轮修复复验

| 项 | 结论 |
|---|------|
| M1 type violation → OperatorException | ✓ 确认修复 |
| L1 ParallelExecutor operatorName 参数 | ✓ 确认修复 |
| L2 Registry.buildOperator Init → RegistryError | ✓ 确认修复 |
| L3 ValidateOutput 消息格式对齐 | ✓ 确认修复 |

### 全量复验摘要

三组并行独立审计（Engine/调度器/Registry、全部 18 算子、Server/Codegen/Config/Frame）。**前轮全部修复项验证通过**，无回归。

### 新发现

#### 🟡 MEDIUM 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| M1 | TransformResourceLookup 抛 IllegalStateException → PanicError | `resource_lookup.go:67-77` — `return fmt.Errorf(...)` → ExecutionError | `TransformResourceLookup.java:42,46,50` — `throw new IllegalStateException(...)` → PanicError | ✅ |
| M2 | TransformRemotePineapple JSON unmarshal 绕过 handleError | `remote_pineapple.go:204` — unmarshal 错误经 `handleError` 尊重 failOnError | `TransformRemotePineapple.java:146` — 直接 `throw OperatorException`，忽略 failOnError=false | ✅ |
| M3 | GoFormat.sprint(-0.0) 返回 "0" | Go `fmt.Sprint(float64(-0))` = `"-0"`（%g 保留符号位） | Java 整数快速路径 `d == Math.floor(d)` 拦截 → `Long.toString(0)` = `"0"` | ✅ |

#### 🟢 LOW 严重度

| # | 差异点 | 说明 | 修复状态 |
|---|--------|------|----------|
| L1 | 调度器缺 per-operator debug 日志 | Go `scheduler.go:231` 输出 `[pine-debug] operator=... duration=... input_size=... output_size=...`；Java 仅有 trace snapshot | ✅ |
| L2 | TransformByLua 错误前缀 "lua error:" vs "lua:" | Go: `"lua: item[N]: msg"`；Java: `"lua error: msg"` | ✅ |
| L3 | TransformRedisGet warning 消息格式 | Go: `"transform_redis_get: SMembers(key): err"` 结构化；Java: 传入原始 exception | ✅ |
| L4 | TransformRedisSet 非 list 值静默跳过无日志 | Go `log.Printf("value for key X is not []string")`；Java 无输出 | ✅ |

### 排除项

| 项 | 理由 |
|---|------|
| Codegen float literal 外部 JSON 模式差异 | 仅影响外部 schema compat 模式，注册模式一致 |
| Codegen string escape \x00 vs \0 | 均为合法 Python escape |
| Server trace duration_ms 精度 | 亚微秒差异，实际不可观测 |
| Server JSON 尾部换行 | 标准 JSON 解析器兼容 |
| 各 Registry/Config 错误消息措辞差异 | 仅开发者可见，非 wire 格式 |

### 第十三轮决策

| # | 决策 | 备注 |
|---|------|------|
| M1 | 修 Java | TransformResourceLookup: 3x IllegalStateException → OperatorException |
| M2 | 修 Java | TransformRemotePineapple: JSON unmarshal 错误走 handleError 尊重 failOnError |
| M3 | 修 Java | GoFormat.sprint: 整数快速路径前添加 -0.0 检测 |
| L1 | 修 Java | Engine: 添加 per-operator debug 日志 (duration/input_size/output_size/JSON) |
| L2 | 修 Java | TransformByLua: 错误前缀 "lua error:" → "lua:" |
| L3 | 修 Java | TransformRedisGet: warning 消息结构化 (SMembers/LRange/Get + key) |
| L4 | 修 Java | TransformRedisSet: set/list case 非列表值增加 stderr 日志 |

## 第十四轮审计 (2026-05-15)

### 第十三轮修复复验

| 项 | 结论 |
|---|------|
| M1 TransformResourceLookup OperatorException | ✓ 确认修复 |
| M2 TransformRemotePineapple handleError 路由 | ✓ 确认修复 |
| M3 GoFormat.sprint(-0.0) 负零检测 | ✓ 确认修复 |
| L1 Engine per-operator debug 日志 | ✓ 确认修复 |
| L2 TransformByLua 错误前缀 "lua:" | ✓ 确认修复 |
| L3 TransformRedisGet structured warning | ✓ 确认修复 |
| L4 TransformRedisSet 非列表值日志 | ✓ 确认修复 |

### 全量复验摘要

三组并行独立审计（Engine/调度器/错误体系/GoFormat/Registry、全部 18 算子、Server/Codegen/Config/Frame）。**前轮全部修复项验证通过**，无回归。

### 新发现

#### 🔴 HIGH 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| H1 | GoFormat.formatFloatF(-0.0) 丢失负号 | `strconv.FormatFloat(-0, 'f', -1, 64)` = `"-0"` | 整数快捷路径 `Math.abs(-0.0)=0.0 < 1e18` → `Long.toString(0)` = `"0"` (`GoFormat.java:67-68`) | ✅ |

#### 🟡 MEDIUM 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| M1 | ReorderSort 错误消息缺上下文 | `"reorder_sort: item[2].score: cannot convert string to float64"` (`sort.go:74`) | `"cannot convert java.lang.String to double"` (`ReorderSort.java:72`) — 缺算子前缀/索引/字段名 | ✅ |
| M2 | TransformRedisSet common_input<2 异常类型 | 返回 `error` → ExecutionError (`redis_set.go:101`) | 抛 `IllegalArgumentException` → PanicError (`TransformRedisSet.java:59`) | ✅ |
| M3 | TransformByLua debug nonNil 统计范围 | 仅统计 commonInput 声明字段非 nil 数 (`lua.go:99-103`) | 统计 rawCommon() 全部非 nil 值 (`TransformByLua.java:93`) | ✅ |
| M4 | Engine.applyOutput 多一层 OperatorException 包装 | `ExecutionError{fmt.Errorf("apply output: ...")}` (`scheduler.go:255`) | `ExecutionError(name, new OperatorException("apply output: ..."))` (`Engine.java:379-380`) | ✅ |

### 排除项

| 项 | 理由 |
|---|------|
| Engine.renderDAG 错误类型 (IllegalArgumentException vs ValidationError) | /dag handler 统一 catch → 400，wire 行为等效 |
| recall_resource 错误消息缺 item 索引/实际类型 | ~~开发者调试信息~~ → 已附带修复（加索引+类型） |
| transform_resource_lookup 错误消息缺 "in context" | 措辞差异，不影响功能 |
| debug 日志 duration 格式差异 (Go time.Duration vs Java 自定义) | 仅影响日志可读性 |
| /health 方法限制 | 已接受（Java 更严格，无害） |
| HTTP 超时配置 | 平台限制（com.sun.net.httpserver 无等效 API） |
| JSON 响应尾部换行 | 已接受 |
| Codegen string escape (\x vs \u) | 均为合法 Python escape |
| merge_dedup key 归一化 | JSON 反序列化后均为 Double，实际不触发 |

### 第十四轮决策

| # | 决策 | 备注 |
|---|------|------|
| H1 | 修 Java | formatFloatF 整数快捷路径前添加 -0.0 检测 |
| M1 | 修 Java | ReorderSort: 错误消息加算子前缀 + item 索引 + 字段名 |
| M2 | 修 Java | TransformRedisSet: IllegalArgumentException → OperatorException |
| M3 | 修 Java | TransformByLua: nonNil 改为仅统计 commonInput 声明字段 |
| M4 | 修 Java | Engine.applyOutput: 去掉多余 OperatorException 包装层 |
| 附带 | 修 Java | RecallResource: 错误消息加 item 索引和实际类型 |
| 附带 | 修 Java | TransformRedisGet: failOnError 错误消息加 Redis 命令名 |

## 第十五轮审计 (2026-05-15)

### 第十四轮修复复验

| 项 | 结论 |
|---|------|
| H1 GoFormat.formatFloatF(-0.0) | ✓ rawLongBits 检查在快捷路径前 |
| M1 ReorderSort 错误消息上下文 | ✓ 含算子前缀+索引+字段名 |
| M2 TransformRedisSet 异常类型 | ✓ OperatorException |
| M3 TransformByLua debug nonNil | ✓ 仅遍历 commonInput |
| M4 Engine.applyOutput 包装层级 | ✓ 单层包装 |
| RecallResource 错误消息 | ✓ 含 item 索引+类型 |
| TransformRedisGet failOnError 命令名 | ✓ 含 SMembers/LRange/Get |

### 全量复验摘要

三组并行独立审计（Engine/GoFormat/Registry、全部 18 算子、Server/Codegen/Config/Frame）。**前轮全部修复项验证通过**，无回归。execute() 路径 OperatorException 迁移已完成。

### 新发现

#### 🟡 MEDIUM 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| M1 | GoFormat.formatFloatF(±Infinity) | `strconv.FormatFloat(+Inf,'f',-1,64)` = `"+Inf"` | `Double.toString(Infinity)` = `"Infinity"` (`GoFormat.java:75`) | ✅ |
| M2 | TransformNormalize 错误消息缺上下文 | `"transform_normalize: item[%d].%s: %w"` (`normalize.go:67`) | `"cannot convert ... to double"` 无算子前缀/索引/字段名 (`TransformNormalize.java:40,60`) | ✅ |

### 排除项

| 项 | 理由 |
|---|------|
| Server /execute ValidationError 响应字段集 | Go omitempty 也省略空字段，wire 等效 |
| TransformRedisSet failOnError=true 日志行为 | Go 额外 log.Printf，仅影响日志 |
| TransformRedisSet/RemotePineapple setWarning 异常类型 | Go error vs Java Exception/RuntimeException，消息等效 |
| Registry/Config 消息文本措辞 | 开发者可见，非 wire 格式 |
| trace duration_ms 精度 | 亚微秒差异不可观测 |
| Codegen string escape | 均为合法 Python escape |

### 第十五轮决策

| # | 决策 | 备注 |
|---|------|------|
| M1 | 修 Java | formatFloatF: NaN/±Infinity 显式守卫 |
| M2 | 修 Java | TransformNormalize: 错误消息加算子前缀 + item 索引 + 字段名 |

## 第十六轮审计 (2026-05-15)

### 第十五轮修复复验

| 项 | 结论 |
|---|------|
| M1 GoFormat.formatFloatF NaN/±Infinity | ✓ L70-72 显式守卫 |
| M2 TransformNormalize 错误消息 | ✓ 含算子前缀+索引+字段名 |

### 全量复验摘要

三组并行独立审计 + execute 路径 RuntimeException 全量追踪。**前轮全部修复项验证通过**，无回归。

### execute() 路径 RuntimeException 全量追踪

| 文件:行 | 异常 | 是否被局部捕获 | 结论 |
|---------|------|---------------|------|
| TransformRedisSet:106 | IAE("unsupported data_type") | ✅ L108 catch → OperatorException | 安全 |
| TransformRedisGet:97 | IAE("unsupported data_type") | ✅ L99 catch → OperatorException | 安全 |
| TransformRemotePineapple:189,195 | IAE("host not allowed") | N/A（init，非 execute） | 安全 |
| TransformRemotePineapple:241 | RE("response too large") | ✅ L132 catch → handleError | 安全 |
| TransformByLua:253 | ISE("lua pool is closed") | ❌ L102 在 try 块外 | **新 M1** |

### 新发现

#### 🟡 MEDIUM 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| M1 | TransformByLua pool.borrow() 关闭时错误分类 | `return fmt.Errorf("lua: pool is closed")` → ExecutionError (`lua.go:111-113`) | `throw IllegalStateException` 在 try 块外 → PanicError (`TransformByLua.java:102,253`) | ✅ |
| M2 | Engine.renderDAG 错误类型 | 返回 `*ValidationError` (`pine.go:296`) | 抛 `IllegalArgumentException` (非 ValidationError) (`Engine.java:482`) | ✅ |

### 排除项

| 项 | 理由 |
|---|------|
| /stats JSON key 顺序 | JSON spec 不保证对象 key 序 |
| /dag 错误消息措辞 | 状态码均为 400 |
| Config 校验消息措辞 | 开发者可见，非 wire 格式 |
| TransformRemotePineapple setWarning RuntimeException | getMessage() 等效 |

### 第十六轮决策

| # | 决策 | 备注 |
|---|------|------|
| M1 | 修 Java | LuaPool.borrow() 关闭返回 null + execute 检查抛 OperatorException |
| M2 | 修 Java | renderDAG IllegalArgumentException → ValidationError |

---

## 第十七轮审计 (R17)

**方法论**: 按差异类型分组交叉验证（错误消息措辞、类型信息完整性、skip 字段校验措辞），独立重新验证源码。

**结果**: 0 HIGH / 0 MEDIUM / 6 LOW

### 🟢 LOW 严重度

| # | 差异点 | Go 行为 | Java 行为 | 修复状态 |
|---|--------|---------|-----------|----------|
| L1 | RecallResource: no resource provider 缺 "in context" | `"no resource provider in context"` (`recall_resource.go:48`) | `"no resource provider"` | ✅ |
| L2 | TransformResourceLookup: 同上 | `"no resource provider in context"` (`resource_lookup.go:68`) | `"no resource provider"` | ✅ |
| L3 | RecallResource: 类型错误缺实际类型 | `"is %T, want []map[string]any"` (`recall_resource.go:77`) | `"is not a List"` | ✅ |
| L4 | TransformResourceLookup: 类型错误缺实际类型 | `"is %T, want map[string]any"` (`resource_lookup.go:76`) | `"is not a Map"` | ✅ |
| L5 | Config skip field: 起始字符校验消息 | `"must start with '_' (control fields are engine-internal)"` (`load.go:222`) | `"must start with underscore"` | ✅ |
| L6 | Config skip field: common_input 关联校验消息 | `"must also appear in $metadata.common_input to ensure correct DAG ordering"` (`load.go:243`) | `"not found in $metadata.common_input"` | ✅ |

### 第十七轮决策

| # | 决策 | 备注 |
|---|------|------|
| L1-L6 | 全部修复 | 错误消息完全对齐 Go 侧措辞 |

### 收敛总结

| 轮次 | HIGH | MEDIUM | LOW | 说明 |
|------|------|--------|-----|------|
| R9   | 8    | 11     | -   | 首轮系统审计 |
| R14  | 1    | 4      | -   | 独立重验 |
| R15  | 0    | 2      | -   | 继续收敛 |
| R16  | 0    | 2      | -   | 深度追查 |
| R17  | 0    | 0      | 6   | 仅剩措辞级差异，全部修复 |

---

## 第十八轮审计 (R18)

**方法论**: 三路并行独立审计——①重新验证 R14-R17 全部 16 项修复到位；②逐算子逻辑语义对比（18 对）；③Engine/Server/Config/GoFormat 非算子代码交叉验证。

**结果**: R14-R17 全部修复确认到位。0 HIGH / 0 MEDIUM / 11 LOW

### 🟢 LOW 严重度

| # | 差异点 | Go 行为 | Java 行为 | 处理 |
|---|--------|---------|-----------|------|
| L1 | RenderDAG 错误消息措辞 | `unsupported DAG format "X" (use "dot" or "mermaid")` | `unsupported format "X": expected "dot" or "mermaid"` | ✅ 修复 |
| L2 | trace duration_ms 精度 | 微秒截断后÷1000 (1.234) | 纳秒÷1000000 (1.234567) | 可接受 |
| L3 | request.Common 校验消息 | `request.Common must not be nil` | `request common must not be null` | ✅ 修复 |
| L4 | MergeDedup normalizeKey | `map[any]` raw key | Number→double 归一化 | 可接受：补偿 Jackson 类型差异 |
| L5 | Redis SMEMBERS 返回顺序 | `[]string` 保留响应序 | `Set<String>` 包装丢失序 | 可接受：Redis Set 本身无序 |
| L6 | reorder_sort 稳定性 | pdqsort（不稳定） | TimSort（稳定） | 可接受：平台差异 |
| L7 | normalize/sort 类型宽度 | 仅 float64/int64/int | 任何 Number 子类 | 可接受：实际 JSON 不触发 |
| L8 | Lua common 模式取消检查 | VM 指令级 ctx 检查 | 无取消检查 | ✅ 修复：添加输入准备后+函数调用前检查 |
| L9 | observe_log 输出流 | log.Printf (stdout) | System.err.printf (stderr) | 可接受：不影响管线输出 |
| L10 | SSRF dial-time 校验 | DialContext 拦截 | DNS 重查 (TOCTOU 窗口) | 可接受：平台限制 |
| L11 | /stats JSON key 顺序 | 字母排序 | 插入顺序 | 可接受：JSON spec 无序 |

### 第十八轮决策

| # | 决策 | 备注 |
|---|------|------|
| L1 | 修 Java | RenderDAG 消息对齐 Go 措辞 |
| L3 | 修 Java | request.Common 消息对齐 Go 措辞 |
| L8 | 修 Java | executeForCommon 添加 token 参数及两处取消检查 |
| 其余 | 可接受 | 平台/设计差异，无功能影响 |

### 收敛总结

| 轮次 | HIGH | MEDIUM | LOW | 说明 |
|------|------|--------|-----|------|
| R9   | 8    | 11     | -   | 首轮系统审计 |
| R14  | 1    | 4      | -   | 独立重验 |
| R15  | 0    | 2      | -   | 继续收敛 |
| R16  | 0    | 2      | -   | 深度追查 |
| R17  | 0    | 0      | 6   | 措辞级差异，全部修复 |
| R18  | 0    | 0      | 11  | 3 修复 + 8 accepted → **审计收敛** |
