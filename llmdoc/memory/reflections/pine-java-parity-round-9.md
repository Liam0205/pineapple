# Pine-Java 第九轮 Go-parity 审计复盘

## Task
- 第九轮审计修复（commit 03b6d46），处理 14 项差异，涵盖错误边界重构、调度器事件驱动化、GoFormat 边角修复、Server 行为对齐。

## Expected vs Actual
- Expected: 经过八轮修复，剩余项应为格式化和 API 细节微调。
- Actual: 14 项中包含两个设计层面重构（OperatorException checked boundary、CompletableFuture 调度器），复杂度超预期；GoFormat 持续发现新边角（Infinity、List/array、小数阈值），表明缺乏系统性验证手段。

## What Went Wrong

### 1. `throws Exception` 过于宽泛
`Operator.execute()` 声明 `throws Exception` 导致引擎无法区分算子层预期错误与框架级 panic。Go 通过 `error` 返回值 vs `panic` 建立了天然边界，Java 需要通过 checked exception（`OperatorException`）vs unchecked `RuntimeException` 显式建模。

### 2. CountDownLatch + 5ms 轮询引入不必要延迟
前轮调度器使用 CountDownLatch 加 5ms 定期唤醒判断前驱完成。零前驱算子仍需等待首次轮询周期；高频调度场景下 CPU 浪费可观。Go 使用 done-channel 的事件驱动模型无此问题。

### 3. GoFormat 边角持续逐项发现
每轮审计都新增 2-4 个格式化边角（本轮：Infinity→"+Inf"/"-Inf"、List/array→空格分隔、[1e-4, 1e-3) 小数 BigDecimal.toPlainString()、formatFloatF shortest round-trip）。逐项发现模式说明缺乏系统性 fixture 对比。

### 4. Server 错误码/响应体不一致
JsonProcessingException 未映射到 400；ValidationError 响应使用空集合而非 null；/dag collapse 错误消息措辞与 Go 不统一。属于集成测试覆盖不足。

## Root Cause

1. **Java 初始设计未区分错误层级** -- 第一轮实现 Operator 接口时直接使用 `throws Exception`（最简写法），未预见引擎需要在 catch 层做语义路由。Go 的 error/panic 二分模型应在接口设计阶段就映射到 Java 的 checked/unchecked 分界。

2. **调度器首版照搬同步模型** -- CountDownLatch 是 Java 并发最熟悉的原语之一，容易成为默认选择。Go 的 channel 天然事件驱动，Java 等价物是 CompletableFuture 而非 Latch + poll。首版应直接选择 CompletableFuture.allOf() 模式。

3. **GoFormat 缺乏 fixture-based 验证** -- 每轮靠人工阅读 Go 源码对比，发现边角依赖审计者经验。正确做法是构建 Go-side fixture generator（`go test -run TestGenerateFormatFixtures`），产出 JSON fixture，Java 侧逐条断言。

4. **Server 集成测试不测响应体结构** -- 现有 Server 测试仅断言 status code，未校验 JSON body 的 null vs empty 语义、error message 文本。

## Missing Docs or Signals

- `architecture/dag-engine.md` 缺少错误边界模型的显式描述（OperatorException → ExecutionError vs RuntimeException → PanicError）。
- `architecture/dag-engine.md` 调度器段落未记录 CompletableFuture 事件驱动模型（仍描述旧 CountDownLatch 语义）。
- `reference/operator-contract.md` 未记录 `throws PineErrors.OperatorException` 约束（算子开发者必须知道只能抛此受检异常）。
- 无文档描述 GoFormat fixture-based 验证策略。
- `must/conventions.md` 缺少 "pine:" 错误消息前缀约定。

## Promotion Candidates

### 应提升到 `architecture/dag-engine.md`
- **错误边界模型** -- `Operator.execute()` 声明 `throws OperatorException`（checked）；引擎 catch 路由：`OperatorException` → `ExecutionError`（预期错误），`RuntimeException` → `PanicError`（意外 panic）。所有错误 getMessage() 带 "pine:" 前缀。等价于 Go error vs panic。
- **CompletableFuture 调度器** -- 每个算子绑定 `CompletableFuture<Void>`，前驱等待用 `.join()`，主线程用 `CompletableFuture.allOf()` 阻塞。Fatal error 时对所有未完成 future 执行 `completeExceptionally()`。等价于 Go done-channel + select。

### 应提升到 `reference/operator-contract.md`
- **OperatorException 约束** -- 算子只能抛出 `PineErrors.OperatorException`（或其子类）表示业务错误；抛出其他 RuntimeException 会被引擎归类为 PanicError 并附带 stack trace。
- **GoFormat 完整边角规格** -- Infinity → "+Inf"/"-Inf"；List/array → "[a b c]" 空格分隔；[1e-4, 1e-3) 使用 BigDecimal.toPlainString()；formatFloatF 使用 Double.toString shortest round-trip。

### 应提升到 `must/conventions.md`
- **"pine:" 错误消息前缀** -- 所有 PineErrors 子类的 getMessage() 以 "pine:" 开头，与 Go 保持一致。

### 暂留 memory
- CompletableFuture force-complete 的具体实现（`completeExceptionally` vs `complete(null)`）
- BigDecimal.toPlainString() 在 [1e-4, 1e-3) 范围的精度行为
- Server ValidationError null vs empty 的 Jackson 序列化配置

## Follow-up
- 更新 `architecture/dag-engine.md`：补充错误边界模型描述与 CompletableFuture 调度器段落。
- 更新 `reference/operator-contract.md`：补充 OperatorException 约束与 GoFormat 边角规格。
- 考虑创建 GoFormat fixture generator（Go 侧）+ Java fixture consumer，终结逐轮发现模式。
- Server 集成测试增加 response body 结构断言（null vs empty、error message 文本）。
