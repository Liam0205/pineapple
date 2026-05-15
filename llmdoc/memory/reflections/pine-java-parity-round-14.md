# Pine-Java 第十四轮 Go-parity 审计复盘

## Task
- 第十四轮审计修复（commit c6e287d），处理 7 项差异（1H/4M/2 sweep）：GoFormat.formatFloatF -0.0、ReorderSort 错误消息、TransformRedisSet 异常类型、TransformByLua debug 计数对象、Engine.applyOutput 双重包装、RecallResource 错误信息、TransformRedisGet 错误信息。

## Expected vs Actual
- Expected: GoFormat -0.0 问题在第十轮（formatG）和第十三轮（sprint）修复后应已覆盖所有路径。OperatorException 迁移在第九至十三轮共五轮清扫后应无残余。Engine applyOutput 在第十一轮简化后应工作正确。
- Actual: GoFormat.formatFloatF 仍有独立的整数快捷路径绕过 -0.0 检查（第三条路径）。TransformRedisSet 仍存在 IllegalArgumentException。Engine.applyOutput 在第十一轮简化时引入了双重包装（OperatorException 内 ExecutionError 再包一层）。

## What Went Wrong

### H1: GoFormat.formatFloatF -0.0 -- 第三次命中
formatFloatF 的整数快捷路径（`value == Math.floor(value)` 时格式化为无小数点数字）在 -0.0 时不正确输出 "0"。这是 GoFormat 模块中第三条独立的整数快捷路径：
- Round 10: `formatG` 的快捷路径
- Round 13: `sprint` 的快捷路径
- Round 14: `formatFloatF` 的快捷路径

三条路径结构相同（检测整数值 → 跳过浮点格式化）、缺陷相同（未在快捷路径前检查 -0.0 符号位），却分别在三个独立审计轮次中被发现。

### M1: ReorderSort 错误消息格式
错误消息缺少算子前缀、item 索引和字段名等结构化信息，与 Go 侧格式不一致。

### M2: TransformRedisSet IllegalArgumentException
OperatorException 迁移第五轮仍发现残余。这次是 IllegalArgumentException 而非 IllegalStateException（前几轮清扫的目标），说明之前 grep 范围不够宽。

### M3: TransformByLua debug nonNil 计数
Java 计数了 DataFrame 中所有非 nil 字段数，Go 只计数 commonInput 声明的字段中非 nil 的。语义差异导致 debug 日志数值不同。

### M4: Engine.applyOutput 双重包装
第十一轮将 applyOutput 简化为"始终 ExecutionError"，但在异常路径上额外 catch 了 OperatorException 并重新包装成 ExecutionError，而 ExecutionError 构造器本身已将 cause 链保留。结果是：OperatorException → ExecutionError（applyOutput catch）→ 再被外层记录为 ExecutionError。修复为直接向上传播，不做额外包装。

## Root Cause

1. **-0.0：修复应覆盖模块而非路径** -- 连续三轮修复同一 edge case 的不同代码路径。根因不是不知道 -0.0 需要检查，而是每次只修复报告的单一函数，未审计同模块其他路径是否有相同模式。GoFormat 只有 3 个公开方法，完整审计成本极低（< 5 分钟），但连续三轮未执行。

2. **OperatorException grep 范围不足** -- 前几轮 grep 目标是 `IllegalStateException`，本轮发现 `IllegalArgumentException` 也需要迁移。根因是思维定式将"非 OperatorException"等同于"IllegalStateException"，实际上任何非 checked exception 都应被 OperatorException 替代。

3. **简化引入新 bug** -- 第十一轮的"始终 ExecutionError"简化看似统一了错误路径，但引入了对已是 OperatorException 的 catch-and-rewrap，形成双重包装。简化重构需要验证所有 catch 块是否真的需要重新包装，尤其是当被 catch 的异常类型本身已经是结构化错误时。

## Missing Docs or Signals

- GoFormat 文档应明确列出所有公开方法的完整列表，并标注"边角覆盖（-0.0/Infinity/NaN）必须在每条方法的每条快捷路径前检查"。当前文档只描述 formatG 的 -0.0 行为。
- OperatorException 迁移文档应给出完整 grep 模式：`IllegalStateException|IllegalArgumentException|RuntimeException|UnsupportedOperationException` 而非只 grep 单一异常类型。
- 无文档约束"简化重构后必须验证 error 嵌套层数不增加"。

## Promotion Candidates

### 可提升到 `architecture/dag-engine.md` GoFormat 段落
- **已执行**：将 formatG 的 -0.0 描述扩展为覆盖所有三个方法（sprint、formatFloatF、formatG）。

### 暂留 memory
- **"修一个路径，审全模块"模式** -- 三轮连续命中同一 edge case 是最强证据。这是工作流纪律而非架构知识，保留在 memory 中作为审计前置检查项。
- **异常类型 grep 宽度** -- `IllegalArgumentException` 是第五轮才新发现的遗漏类型，需要更宽的正则。保留在 memory 中。
- **简化不等于安全** -- 重构引入的 bug 比原始代码更难发现，因为审查者倾向于信任简化后的代码。保留在 memory 中。

## Follow-up
- 确认 GoFormat 三个公开方法（sprint、formatFloatF、formatG）的 -0.0 覆盖已全部到位，不应再有第四条路径。
- 下轮审计前用宽模式 grep 验证 OperatorException 迁移完成：`grep -rn "throw new \(IllegalStateException\|IllegalArgumentException\|RuntimeException\|UnsupportedOperationException\)" pine-java/src/main/`
- 验证 Engine.applyOutput 错误链层数：throw → catch → 记录，每个错误最多被包装一次。
