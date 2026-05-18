# Pine-Java 第十五轮 Go-parity 审计复盘

## Task
- 第十五轮审计修复（commits 86261fb + 0717ff8），处理 2 项 MEDIUM 差异：GoFormat.formatFloatF NaN/Infinity 显式守卫、TransformNormalize 错误消息格式对齐。

## Expected vs Actual
- Expected: 经过 14 轮审计（从 20+ 项/轮收敛到个位数），剩余差异应极少且低影响。
- Actual: 仅剩 2 项 MEDIUM。修复后审计结论为 0 HIGH、2 MEDIUM deferred，接近或已达最终对等状态。

## What Went Wrong

### M1: GoFormat.formatFloatF NaN/Infinity 守卫缺失
formatFloatF 对 NaN 和 ±Infinity 没有显式守卫，依赖 `Double.toString()` 产出 Java 风格字符串（"Infinity"/"NaN"），而 Go 约定为 "+Inf"/"-Inf"/"NaN"。formatG 在第九轮已修复相同问题（Infinity→"+Inf"/"-Inf"），formatFloatF 应同步但遗漏。

### M2: TransformNormalize 错误消息缺少结构化前缀
错误消息缺少算子前缀、item 索引、字段名等上下文信息，与 Go 侧 `fmt.Errorf("normalize[%d].%s: ...")` 格式不一致。属于常规错误格式对齐。

## Root Cause

1. **"修一个方法，审全模块"模式再次验证** -- formatFloatF 的 Infinity 守卫缺失与第九轮 formatG 的 Infinity 修复、第十四轮 formatFloatF 的 -0.0 修复属于同一模式：edge case fix 应用到一条路径后未检查同模块其他路径。这是第十、十三、十四轮均已记录的教训，但 NaN/Infinity 守卫仍遗漏说明：每轮审计聚焦于当轮发现的 edge case 类型，不会主动回溯前轮已修复的 edge case 类型在当前方法是否覆盖。

2. **错误消息格式对齐是长尾收敛** -- TransformNormalize 是 18 个算子之一，其错误消息在前 14 轮中未被抽检到。错误格式差异不影响功能，只在审计 diff output 时才暴露。

## Missing Docs or Signals

- `dag-engine.md` GoFormat 段落已描述 formatG 的 Infinity→"+Inf"/"-Inf" 处理，但未显式声明"所有 GoFormat 公开方法必须对 NaN/±Infinity 做相同守卫"。当前描述是隐含的（只描述了一个方法的行为），改为显式枚举更安全。
- 无文档要求"对每个 GoFormat 方法，检查 IEEE 754 特殊值（-0.0, NaN, +Inf, -Inf）的完整覆盖表"。

## Promotion Candidates

### 暂留 memory（不需提升到稳定文档）
- **GoFormat 特殊值覆盖表** -- `dag-engine.md` 已有 formatG 段落描述 Infinity 处理，formatFloatF 的守卫是相同模式。当前文档隐含覆盖，无需额外更新。
- **"修一条路径，审全模块"** -- 第十、十三、十四、十五轮连续四轮验证同一教训。已有足够证据但属于工作流纪律，不适合写入架构文档。如果未来再次命中（不太可能，因为 GoFormat 只有 3 个公开方法且已全部覆盖），可考虑提升到 `guides/` 作为审计 checklist。

### 不需要稳定文档更新
- TransformNormalize 错误消息是常规格式对齐，不引入新架构概念。
- GoFormat 段落已有充足描述，formatFloatF 的 NaN/Infinity 守卫是同一模式的自然延伸。

## Follow-up
- 审计声明收敛（0 HIGH, 2 MEDIUM deferred）。除非发现新的功能对等需求，Pine-Java parity 审计可结束。
- 剩余 2 MEDIUM deferred 项需记录其具体内容，以便未来按需处理。
- GoFormat 三个公开方法（sprint、formatFloatF、formatG）的 IEEE 754 特殊值覆盖现已完整：-0.0（第十/十三/十四轮）、NaN/Infinity（第九/十五轮）。不应再有同类遗漏。
