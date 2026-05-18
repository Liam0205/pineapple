# Pine-Java 第十一轮 Go-parity 审计复盘

## Task
- 第十一轮审计修复（commit e7263c9），处理 4 项差异：ApplyOutput 错误分类简化、警告前缀格式、Lua 调试日志计数、item-mode 错误索引。

## Expected vs Actual
- Expected: 引擎 applyOutput 阶段的错误应映射为 ExecutionError（框架级预期错误），而非 PanicError。
- Actual: Java 实现从 operator execute() 路径复制了三分支分类（OperatorException->ExecutionError, RuntimeException->PanicError, other->PanicError），导致 applyOutput 内部失败被错误归类为 PanicError。Go 对等实现始终返回 ExecutionError。

## What Went Wrong

### 1. ApplyOutput 三分支错误分类是 cargo-cult
applyOutput 与 operator execute() 语义不同。execute() 需要区分"算子主动报错"（OperatorException->ExecutionError）和"算子意外崩溃"（RuntimeException->PanicError）。applyOutput 是引擎内部的输出应用步骤，失败永远是框架级错误（如类型转换失败、DataFrame 投影异常），不存在"算子 panic"的语义。Java 实现在 Engine.schedule() 中机械复制了 execute() 的 try-catch 模式，引入了错误的 PanicError 分类。

### 2. 警告前缀格式遗漏
Go 端 /execute 响应中的 warnings 字段每条格式为 `operator "name": message`，包含算子名称前缀。Java 端只输出裸 message，缺少 operator 前缀。这种 wire format 细节在前几轮审计中未被发现，因为需要完整 E2E 响应体对比才能暴露。

### 3. Lua 调试日志计数对象错误
TransformByLua 的 debug 日志应记录 metadata 字段数（commonInput.size()），但 Java 实现误用了 input.rawCommon().size() 计数 DataFrame 原始行数。两者在非空场景下数值不同。

### 4. Item-mode 错误缺少索引
Lua item-mode 处理单个 item 失败时，Go 端格式为 `lua: item[N]: message`（包含 item 索引），Java 端只输出 `lua: message`，丢失了定位信息。

## Root Cause

1. **语义复制 vs 语义理解** -- applyOutput 的错误处理被从 execute() 路径机械复制，未分析 applyOutput 失败的本质语义（它总是框架错误，不是算子错误）。引入错误模式时应先回答"这个路径的失败代表什么含义"。

2. **Wire format 对比缺乏系统性** -- 警告前缀、错误索引这类 wire format 细节分散在多个代码路径中，逐行审计容易遗漏。需要 E2E fixture 对比完整响应体（包括 warnings、errors、debug 字段）才能系统性发现。

3. **调试日志无测试覆盖** -- debug 日志输出通常不在单元测试断言范围内，计数对象错误可能长期存在而不被发现。

## Missing Docs or Signals

- `architecture/dag-engine.md` 的结构化错误模型段落描述了 execute() 边界的 OperatorException->ExecutionError / RuntimeException->PanicError 映射，但未显式说明 applyOutput 阶段"始终归为 ExecutionError"的简化规则。此为实现细节，不需要提升到文档。
- 无文档描述 /execute 响应 warnings 字段的 wire format 规范（含 operator 前缀格式）。这属于 API 契约级信息，但优先级不高。

## Promotion Candidates

### 暂留 memory（无需提升到稳定文档）
- **applyOutput 错误始终为 ExecutionError** -- 这是内部实现简化，不改变文档中描述的 error model（文档只描述 operator execute() 边界）。
- **wire format 前缀规则** -- `operator "name": message` 警告格式和 `lua: item[N]: message` 错误格式属于实现细节，若未来有 API spec 文档可纳入。
- **parity gap 收敛信号** -- 第十一轮仅 4 项（对比早期 14-20 项/轮），表明双运行时对等正在收敛。

## Follow-up
- 考虑建立 /execute E2E fixture 测试，对比完整响应体（含 warnings、errors、trace），系统性检测 wire format 差异。
- 无稳定文档更新需求。
