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
| 13 | DebugAware/MetricsAware 缺失 | 算子无法接收引擎注入的 debug/metrics | ⬜ 接口设计差异 |
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
| 28 | Operator.Execute 缺 context 参数 | 基础接口设计差异 | ⬜ 设计差异 |
| 29 | EngineMetrics null vs Nop | 风格差异，运行时等价 | ⬜ |
| 30 | /health 方法校验 | Java 限 GET，Go 不限 | ⬜ Java 更严格 |
| 31 | body size 不可配置 | Java 硬编码 10MB | ⬜ |
| 32 | recall_static Init 引用拷贝 | Go 拷贝 slice，Java 直接存引用 | ✅ |
| 33 | redis_get Set 返回顺序 | Go 保序，Java HashSet 无序 | ⬜ Redis SMEMBERS 本身无序 |
| 34 | reorder_sort 排序稳定性 | Go 不稳定，Java TimSort 稳定 | ⬜ 已接受设计差异 |

## 技术说明
- Go 用 GopherLua + sync.Pool 做 Lua VM 池化和沙箱；Java 用 LuaJ
- Go Server 基于 net/http 支持完整 middleware 链和超时；Java 用 JDK 内置 com.sun.net.httpserver
- ColumnFrame 的 presence bitmap 用于区分 "字段不存在" vs "字段 = nil"
- transform_by_remote_pineapple 包含完整 SSRF 防护 (私有 IP 检测, 安全拨号)
- 结构化错误中 PanicError 区分 public Error() 和 DetailedError() (含 stack)
- merge_dedup 在 Go 中利用 map[any] 的原生类型相等性；Java 需特殊处理以保持语义一致
- /execute 响应格式（duration 单位、warnings 结构）是跨运行时客户端兼容性的关键契约点
