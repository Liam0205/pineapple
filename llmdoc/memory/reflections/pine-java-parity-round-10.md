# Pine-Java 第十轮 Go-parity 审计复盘

## Task
- 第十轮审计修复（commit dc9f2dc），处理 7 项差异，主要完成第九轮引入但未彻底扫描的约定（pine: 前缀、OperatorException 边界）及格式化边角。

## Expected vs Actual
- Expected: 第九轮引入 OperatorException 边界和 pine: 前缀约定后，所有调用点应已统一。
- Actual: RegistryError 和 PanicError 的 getMessage() 未遵循新约定；RecallResource 仍在抛 IllegalStateException 而非 OperatorException。说明引入约定时未做全局扫描。

## What Went Wrong

### 1. 约定引入与全局扫描分离
第九轮建立了两个约定（pine: 错误消息前缀、OperatorException 作为算子唯一 checked exception），但只在当轮修复的文件中应用。RegistryError、PanicError、RecallResource 等未在同一轮覆盖。

### 2. -0.0 符号位遗漏
Go 的 `fmt.Sprintf("%g", -0.0)` 自动输出 `"-0"`，Java 的 `Double.toString(-0.0)` 输出 `"-0.0"` 但 `formatG` 先前的零值快捷路径 (`== 0`) 无法区分 +0.0 与 -0.0。需要通过 `Double.doubleToRawLongBits` 检测符号位。这是 IEEE 754 在两种语言中默认格式化行为差异的典型案例。

### 3. 验证消息缺少类型信息
data_parallel 校验消息只说"operator does not implement ConcurrentSafe"但未包含实际算子类型名，导致多算子 DAG 中难以定位问题。Go 的等价消息包含类型字符串。

### 4. ObserveLog JSON key 排序不确定
ObserveLog 使用 HashMap 序列化 JSON，key 顺序不确定。Go 使用 `sort.Strings(keys)` 保证字母序输出。Java 侧改用 TreeMap 对齐。

## Root Cause

1. **约定引入未配合 grep 扫描** -- 当引入一个跨类约定（如所有错误带 "pine:" 前缀）时，应当在同一 PR 中执行全仓 grep 确保所有现有调用点已对齐。分轮修复效率低且易遗漏。

2. **IEEE 754 特殊值需要显式测试矩阵** -- 浮点格式化的边角（-0.0、NaN、Infinity、subnormal）在 Go 和 Java 的默认行为不同。GoFormat 应建立 fixture 覆盖所有特殊值，而非依赖逐轮发现。

3. **日志/调试输出的确定性排序容易被忽视** -- HashMap 的随机顺序在单元测试中偶尔暴露为 flaky test，但在 cross-runtime parity 审计中表现为输出不一致。任何序列化到 JSON 的 map 都应显式使用 TreeMap 或 sorted keys。

## Missing Docs or Signals

- `architecture/dag-engine.md` GoFormat 段落缺少 -0.0 符号位保留的描述。
- `must/conventions.md` 的 "pine:" 前缀约定应补充"引入约定时须执行全仓 grep 扫描"的操作规范。
- 无文档列举 IEEE 754 特殊值的跨运行时行为差异矩阵。

## Promotion Candidates

### 应提升到 `architecture/dag-engine.md`
- **GoFormat -0.0 符号位** -- `formatG(-0.0)` 保留负零符号位，输出 `"-0"`，通过 `Double.doubleToRawLongBits` 检测。（本次同步执行）

### 应提升到 `must/conventions.md`
- **约定引入全扫描规则** -- 当一轮修复引入跨文件约定（如错误前缀、异常类型边界）时，须在同一轮对全仓执行 grep 扫描确保所有调用点已对齐，避免下一轮做清扫工作。

### 暂留 memory
- IEEE 754 特殊值完整矩阵（-0.0、NaN、Infinity、subnormal、MAX_VALUE）的 Go vs Java 差异
- TreeMap vs HashMap 在 JSON 序列化中的排序语义
- 约定扫描的重复模式统计（round 9→10, round 5→6 的 GoFormat 统一）

## Follow-up
- 更新 `architecture/dag-engine.md` GoFormat 段落补充 -0.0 符号位描述。（本次完成）
- 考虑在 `must/conventions.md` 补充"约定引入即全扫描"操作规范。
- GoFormat fixture generator 仍为待建项目，本轮再次确认其必要性。
