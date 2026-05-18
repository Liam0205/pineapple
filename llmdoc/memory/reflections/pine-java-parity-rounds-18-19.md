# Pine-Java 第十八/十九轮 Go-parity 审计复盘

## Task
- 第十八轮（commit e393ac3）：3 项 LOW 修复 -- Engine.renderDAG/execute 错误消息措辞对齐、TransformByLua.executeForCommon 添加 CancellationToken 参数并在 pre-input/pre-invoke 检查取消。
- 第十九轮（commit 03b42d6）：5 项 LOW 修复 -- RecallStatic/FilterTruncate 错误中实际类型/值对齐（Go %T/%d）、TransformCopy/TransformNormalize/MergeDedup/ReorderSort 错误中枚举值加引号（Go %q）、PineServer trace duration_ms 截断到微秒精度（Go Microseconds()/1000.0）。

## Expected vs Actual
- Expected: 第十七轮后审计已收敛，仅剩零星措辞打磨。
- Actual: 8 项全部 LOW，其中 7 项纯措辞/格式对齐，1 项是行为性补充（common-mode Lua cancellation check）。确认审计完全结束 -- 连续三轮（17-19）均为 LOW-only。

## What Went Wrong
- **TransformByLua common-mode CancellationToken 遗漏**：item-mode execute 早已有 cancellation check，但 executeForCommon（common_output 模式）未添加。这是跨切面关注点（cancellation）在双执行路径中的覆盖不完整。该项虽为 LOW（因 common-mode 使用频率低），但属于真正的行为缺口。
- 其余 7 项纯属格式化与措辞打磨，属"做完就忘"类别。

## Root Cause
- **跨切面关注点未全路径审计**：CancellationToken 在初次实现时仅覆盖了主执行路径（item-mode），忽略了较少使用的 common-mode 路径。Go 侧 `ctx.Err()` 检查存在于所有 Lua 调用入口，Java 侧实现时遗漏了非主路径。
- **浮点精度差异仅 fixture 可见**：PineServer trace 的 duration_ms 微秒截断差异（`Microseconds()/1000.0` vs `nanos/1_000_000.0`）在人类 review 中不可能被发现，只有 JSON fixture 逐字段对比才能捕获。
- **Go format verb 语义**：`%T`（类型名）、`%d`（整数值）、`%q`（带引号字符串）在 Java 侧初始翻译中未一一对应。这是已知的 LOW 收尾工作。

## Missing Docs or Signals
- 无缺失稳定文档。`dag-engine.md` 已记录 CancellationToken 取消传播模型（"长时间循环检查 token.isCancelled()"），common-mode 修复是同一模型的路径扩展，不引入新概念。

## Promotion Candidates

### 暂留 memory（不提升到稳定文档）
- **跨切面关注点需全路径覆盖**：添加 cancellation/tracing/metrics 等跨切面功能时，必须枚举所有执行入口（item-mode、common-mode、error-path），不能仅覆盖主路径。此教训适用于未来任何跨切面特性，但作为通用工程实践不需要写入架构文档。
- **Fixture-based E2E 是精度对齐的唯一可靠手段**：浮点截断、时间戳精度等差异只有逐字段 JSON 对比才能发现。此经验已在 round-9 反思中记录，本轮再次验证。
- **审计完结里程碑**：rounds 7-19 共 11 轮修复约 90 项差异，从 20-item HIGH/MEDIUM 轮次逐步收敛到 3-5 item LOW-only 轮次。整体审计轨迹验证了"严重度递降、批量打磨收尾"策略的有效性。

### 不需要稳定文档更新
- dag-engine.md 已涵盖 CancellationToken 模型，common-mode 补充不改变架构描述。
- 措辞/精度对齐不引入新行为或 API。

## Follow-up
- Pine-Java Go-parity 审计正式完结（rounds 1-19）。后续仅需增量同步：Go 侧新功能实现时同步到 Java。
- 完整审计轨迹总结：结构性差异(1-8) -> 错误类型迁移(9-16) -> 措辞/格式打磨(17-19)。
- 经验规则：未来实现跨切面功能时，checklist 应显式列出所有执行入口（不仅主路径），在 PR review 中作为检查项。
