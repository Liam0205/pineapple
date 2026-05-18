# Pine-Java 第六轮 Go-parity 审计复盘

## 任务概述

第六轮审计发现 19 项差异（5 HIGH, 9 MEDIUM, 5 LOW）。处置结果：修复 14 项 Java 侧、注册 3 项 Go 侧修复、接受 2 项设计差异。

## 关键决策

### GoFormat 统一为跨算子格式化单一事实源

- `FilterCondition.formatValue` 和 `ReorderShuffle.formatFloatG` 被移除
- 所有算子统一通过 `GoFormat.sprint` / `GoFormat.formatG` 实现跨运行时格式一致性
- 该整合本应在第五轮完成，但当时只覆盖了 `TransformResourceLookup` 和 `TransformRedisGet`

### ResourceRegistry 模式

- 新增 `ResourceRegistry.java`，一个 codegen-time 轻量注册表
- 等同 Go 的 `resource.All()`，支持 `--export-schema` 导出资源 Schema
- 无需运行时实例化 ResourceManager

### Java 比 Go 更正确的 3 项

- `globalDebug` 传播逻辑
- Lua Init validate（脚本语法校验）
- `sanitizeMermaidID`（特殊字符处理）

已注册为 Go 侧待修复项。

## 第五轮遗留的回归

| 项目 | 第五轮状态 | 第六轮发现 |
|---|---|---|
| `filter_condition` | 已对齐 formatValue | 未迁移到 GoFormat，大数格式仍有差异 |
| `__init__.py` codegen | 已对齐 | em dash vs double dash、trailing comma 漏修 |
| `statusBucket` | 已对齐 | HTTP status 分桶边界不一致 |
| `reorder_shuffle` | 未触及 | formatFloatG 未统一到 GoFormat |

## 教训

1. **"已验证"不等于"已完成"** -- 前一轮标记为 verified 的项目在独立重审中发现回归，需要定期交叉验证而非单次确认。
2. **格式化方法碎片化代价高** -- 多个算子各自实现 format helper 是技术债；GoFormat 作为单点抽象早该推广到全部消费者。
3. **Round 5 的 `%g` 替换不够彻底** -- 用 `Double.toString` 替代 `%g` 时未验证大数场景（1e7+ 触发科学记数法），应以 Go fixture 为 oracle 做端到端比对。
4. **Codegen 细节需 diff-level 审查** -- em dash 字符差异、trailing comma 等问题只能通过字节级 diff 发现，目视审查容易遗漏。

## 提升为稳定文档的内容

- GoFormat 消费者列表已更新到 `dag-engine.md` 和 `operator-contract.md`
- ResourceRegistry 已添加到 `dag-engine.md` Codegen 小节
- `SetWarning` first-wins 语义已添加到 `dag-engine.md` 错误处理小节
- `conventions.md` 跨运行时格式一致性小节已列出全部统一消费者
