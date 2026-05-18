# Pine-Java 第七/八轮 Go-parity 审计复盘

## Task
- 两轮审计修复（commits b392173, 65f3fd1），共处理 20 项差异，进一步收紧 Pine-Java 与 Go 运行时的对等性。
- 涉及 CancellationToken 层级、ResourceAware 注入时机、GoFormat 阈值、PanicError/Throwable 包装四个核心领域。

## Expected vs Actual
- Expected: 经过前六轮修复，剩余差异应为边角细节，修复量小且风险低。
- Actual: 20 项修复中有多项涉及并发安全语义（shard-level token 隔离）和编译期/运行期职责划分（ResourceAware 注入时机），属于设计层面修正而非简单对齐。

## What Went Wrong

### 1. CancellationToken 层级缺失
ParallelExecutor 直接复用父请求的 CancellationToken，导致单个 shard 失败时 cancel 整个请求 token，影响其他并行路径的清理逻辑。Go 侧使用 `context.WithCancel` 创建 per-shard 子 context，Java 侧缺少等价的 parent-child token 层级。

### 2. ResourceAware 注入在错误的生命周期阶段
ResourceAware 注入发生在 Engine.execute 每次请求循环中（~line 270），而非编译阶段。Go 在 engine build time 一次性注入资源引用。per-request 注入引入了并发执行期间不必要的 mutability。

### 3. GoFormat.sprint 整数检测阈值错误
使用 1e18（接近 Long.MAX_VALUE）作为整数/浮点分界，实际 Go 的 fmt.Sprint 在 1e6 即切换为科学记数法表示。[1e6, 1e7) 范围需要 scientific-to-decimal 转换逻辑。

### 4. 非 Exception Throwable 包装丢失结构化上下文
并行 shard 中的 OutOfMemoryError 等非 Exception Throwable 被包装为裸 RuntimeException，丢失结构化错误信息。Go 侧使用 PanicError 包装并保留上下文。

## Root Cause

1. **Token 层级语义未被文档化** -- 第三/四轮引入 CancellationToken 时只关注"有无取消能力"，未深入分析 Go 的 parent-child context 树状取消传播模型。volatile boolean 满足了单层取消需求，但未建立层级隔离。

2. **"注入"与"绑定时机"是两个独立维度** -- 前轮修复聚焦于"是否注入 ResourceAware"，忽略了"何时注入"同样是对等性的一部分。Go 的 build-time injection 是刻意设计：资源引用在编译期不可变，运行期无需同步。

3. **GoFormat 验证依赖 Java 本地直觉而非 Go fixture** -- 1e18 阈值来自 Java Long 范围推断，而非实际运行 Go fmt.Sprint 观察行为。Go 对浮点数的格式化切换点（1e6）比 Java 开发者直觉低得多。

4. **Throwable vs Exception 边界在 Java 并发代码中被忽视** -- Go 的 recover() 捕获所有 panic（包括 OOM 等系统级错误），Java 的 catch(Exception) 不捕获 Error 子类。并行框架需要 catch(Throwable) 并做结构化包装。

## Missing Docs or Signals

- `architecture/dag-engine.md` 缺少 CancellationToken 的 parent-child 层级描述（仅记录了 volatile boolean 单层）。
- `architecture/dag-engine.md` 缺少 ResourceAware 注入时机的明确约束（"编译期一次性注入，运行期不可变"）。
- `reference/operator-contract.md` 缺少 GoFormat 整数检测阈值的精确规格（1e6 切换点）。
- 无文档描述 Java 并行框架中 Throwable vs Exception 的捕获策略。

## Promotion Candidates

### 应提升到 `architecture/dag-engine.md`
- **CancellationToken parent-child 层级** -- ParallelExecutor 为每个 shard 创建子 token；shard 失败仅 cancel 子 token；每个 shard 启动前检查父 token。等价于 Go 的 `context.WithCancel` per-shard 子 context。
- **ResourceAware 编译期注入约束** -- 与 MetadataAware/DebugAware/MetricsAware 同阶段注入，运行期只读。不得在 execute 循环中重复注入。

### 应提升到 `reference/operator-contract.md`
- **GoFormat.sprint 阈值规格** -- 整数检测上界 1e6（非 Long.MAX_VALUE）；[1e6, 1e7) 范围 scientific-to-decimal 转换；>= 1e7 保持科学记数法。
- **并行 shard Throwable 包装** -- 非 Exception Throwable 必须包装为 PineErrors.ExecutionError 并附带结构化上下文（shard index、operator name），不得丢为裸 RuntimeException。

### 暂留 memory
- ParallelExecutor 中 shardToken.cancel() 的具体调用位置与 try-finally 结构
- formatG 的 [1e6, 1e7) decimal 转换实现细节
- OOM 包装后的错误消息格式

## Follow-up
- 更新 `architecture/dag-engine.md` Pine-Java 小节：补充 CancellationToken parent-child 层级模型、ResourceAware 编译期注入约束。
- 更新 `reference/operator-contract.md`：补充 GoFormat 阈值精确规格、并行 Throwable 包装策略。
- 考虑在 `must/conventions.md` 增加"生命周期阶段注入原则"通用规则：可注入依赖应在最早的确定性阶段绑定，避免运行期重复注入引入 mutability。
