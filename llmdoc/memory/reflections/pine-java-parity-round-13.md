# Pine-Java 第十三轮 Go-parity 审计复盘

## Task
- 第十三轮审计修复（commit 263ede0），处理 7 项差异（0H/2M/4L）：TransformResourceLookup 异常类型、TransformRemotePineapple 错误路由、GoFormat sprint -0.0、Engine debug 日志、Lua 错误前缀、Redis 警告消息格式。

## Expected vs Actual
- Expected: OperatorException 边界已在第九轮引入并在第十/十二轮做过清扫，不应再出现 IllegalStateException 遗漏。GoFormat -0.0 在第十轮修复 formatG 后所有路径应已覆盖。
- Actual: TransformResourceLookup 仍有 3 处 IllegalStateException 未迁移。GoFormat.sprint 的整数快捷路径绕过了 -0.0 检查。

## What Went Wrong

### M1: TransformResourceLookup IllegalStateException (3 处)
TransformResourceLookup 在资源查找失败时抛出 IllegalStateException 而非 OperatorException。这是第四次在不同算子中发现相同问题（round 9 引入、round 10 清扫、round 12 再发现、round 13 继续发现）。"约定引入即全扫描"教训已记录三轮但仍未被执行。

### M2: TransformRemotePineapple JSON unmarshal 错误路由
Go 的 `json.Unmarshal` 失败后调用 `handleError`（warning + return），Java 实现直接 throw。handleError 尊重 `fail_on_error` 配置，直接 throw 则绕过了该语义，导致 Java 在 fail_on_error=false 时行为与 Go 不一致。

### L1: Engine per-operator debug 日志缺失
Go 引擎在 debug=true 时对每个算子输出 duration/input_size/output_size/JSON snapshot。Java 引擎没有对应输出，导致调试体验不对等。

### L2: TransformByLua 错误前缀 "lua error:" vs "lua:"
Go 使用短前缀 "lua:"，Java 使用冗长版本 "lua error:"。wire format 不一致。

### L3/L4: TransformRedisGet/Set 警告消息格式
Go 输出结构化警告消息（包含操作类型+key），Java 使用不同格式或缺失日志。

## Root Cause

1. **OperatorException 扫描未被执行** -- 第十轮记录"约定引入即全扫描"教训，第十二轮再次验证，第十三轮第三次发现。问题从"未意识到需要全扫描"演变为"知道需要但未执行"。根因可能是：每轮审计聚焦于被报告的差异而非主动全扫描。

2. **GoFormat -0.0 修复不完整：多路径盲区** -- 第十轮修复 `formatG` 中的 -0.0 检测，但 `sprint` 方法有独立的整数快捷路径（`value == (long)value` 时直接格式化为整数），该路径在 -0.0 时不触发（因为 `(long)(-0.0) == 0` 且 `-0.0 == 0`），但仍需在进入快捷路径前检查符号位。修复一个代码路径中的边角时未审查同模块其他路径是否有相同缺口。

3. **错误路由语义差异** -- handleError vs throw 的选择直接影响 fail_on_error 语义。Java 实现者可能将 JSON 解析失败视为"不可恢复错误"而选择 throw，但 Go 的语义是"可降级错误"通过 handleError 路由。这种语义选择差异不会被纯格式/输出对比发现，需要理解错误处理路径的语义含义。

## Missing Docs or Signals

- `reference/operator-contract.md` 未明确列出哪些错误应走 handleError（可降级）vs 哪些应直接传播（不可恢复）。Go 代码自身即规范，但跨运行时实现者需要显式指引。
- GoFormat 文档未描述 sprint 与 formatG 是两条独立格式化路径，各自需要独立的边角覆盖。
- 无文档强制约定"OperatorException 迁移完成标志 = grep 零残余"。

## Promotion Candidates

### 暂留 memory（无需提升到稳定文档）
- **同模块多路径边角覆盖** -- 修复一个函数中的边角时，grep 同模块所有处理相同类型的函数。这是工作流经验，非架构知识。
- **OperatorException 迁移四轮未清** -- 强烈信号表明教训记录不等于教训执行。可考虑在下次 parity 审计前作为前置步骤执行全量 grep。
- **handleError vs throw 语义区分** -- 属于 parity 实现知识，Go 代码即规范。

### 可考虑提升到 `reference/operator-contract.md`
- **handleError 路由语义** -- 明确列出 JSON 解析失败、外部调用失败等场景应走 handleError（尊重 fail_on_error），仅 Schema 级不可恢复错误才直接传播。这能帮助未来跨运行时实现者正确选择错误路径。

## Follow-up
- 在下一轮审计前执行 `grep -rn "IllegalStateException\|throw new RuntimeException" pine-java/src/` 主动清扫，终结 OperatorException 迁移长尾。
- 审查 GoFormat 所有公开方法（formatG、sprint、formatList 等），确认 -0.0/NaN/Infinity 在每条路径都有覆盖。
- 考虑向 `reference/operator-contract.md` 补充 handleError 路由指引。
