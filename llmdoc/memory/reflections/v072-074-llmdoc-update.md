---
name: v072-074-llmdoc-update
description: v0.7.2 - v0.7.4 期间 llmdoc 大面积过时复盘，记录 21 个 commit 跨度下文档陈旧的系统性原因与防范策略
type: reflection
---

## Task

对 v0.7.2 到 v0.7.4 期间（自 commit `8e34676` 至 `4864fcf`，共 21 个 commit）的 llmdoc 全量同步更新。上一次 llmdoc 更新发生在 v0.7.1 时期，此后三个小版本未同步文档。

## Expected vs Actual

- **预期**：llmdoc 应基本反映代码现状，仅需少量增量更新。
- **实际**：发现 6 大类系统性过时，涉及 index.md、conventions.md、ci-quality-baseline.md、dag-engine.md、metrics-observability.md、project-overview.md 等多份核心文档。硬编码数值、实现细节描述、新增能力缺失三类问题并存。

## What Went Wrong

### 1. "十二层"硬编码在 4+ 份文档中未更新
cross-validate 已扩展到 13 层（新增 metrics-parity section），但 index.md、conventions.md、ci-quality-baseline.md、dag-engine.md 多处仍写"十二层"。这是第四次出现跨验证层数硬编码过时问题（历次：3->7, 7->11, 11->12, 12->13）。

### 2. Pine-Java 并发模型描述过时
dag-engine.md 仍描述 ForkJoinPool.commonPool，实际代码已迁移到 Java 21 Virtual Threads。实现细节在文档中固化后，代码演进时无提醒机制。

### 3. CI 性能参数在多处重复且过时
diff-fuzz 200 轮已减为 100 轮，stability-runs 3 次已减为 2 次。这些参数在多份文档中被重复提及，任何一次调参都导致多处文档过时。

### 4. 新增能力完全缺失于文档
- metrics pre-init 行为（三引擎启动时预初始化所有 Prometheus 标签）
- Maven Central 发布路径
- 跨引擎 benchmark 基础设施（3 个新脚本）
- tag-release.sh 双 tag 脚本
- cross-validate 并行执行模式

### 5. 脚本计数可能过时
project-overview.md 提到"18 个标准化脚本"，实际新增了 5-6 个脚本。

## Root Cause

### 系统性原因：版本跨度与文档同步频率不匹配
三个小版本（v0.7.2/0.7.3/0.7.4）的开发节奏快于文档同步频率。每个版本包含 5-8 个 commit，累积 21 个 commit 后文档与代码的偏差从"可接受的轻微滞后"变为"大面积不可信"。

### 反复教训：定量描述的脆弱性（第四次）
跨验证层数硬编码问题已在以下复盘中被指出：
- `p2-refactor-cross-validate-scripts.md`（3->7）
- `pine-python-and-v07-dag-overhaul.md`（7->11）
- `extensibility-parity-tests-and-java-prefix-fix.md`（11->12）
- 本次（12->13）

每次复盘都建议"改为通用描述或指向脚本"，但层数硬编码仍在多份文档中存续。根因是修复建议未在同一次 PR 中执行落地——"下次再改"等于"永远不改"。

### 实现细节固化在架构文档中
dag-engine.md 包含具体并发模型（ForkJoinPool.commonPool）和 CI 参数（200 轮、3 次），这些属于"会随工程决策变化的实现参数"，不应在架构文档中作为事实陈述。

### 单次变更"太小不值得更新文档"的累积效应
metrics pre-init 是一个 commit、benchmark 脚本是一个 commit、tag-release.sh 是一个 commit——每个独立看都"太小"，但累积后文档中缺少了整块能力描述。

## Missing Docs or Signals

1. **无文档同步触发规则**：没有明确的规则说明"哪些类型的 commit 必须伴随 llmdoc 更新"。当前依赖人工判断。
2. **无 CI 检查文档新鲜度的机制**：跨验证层数可以从脚本自动推断，但文档中的硬编码数值无法被 CI 检测到过时。
3. **CI 参数的单一事实源缺失**：diff-fuzz 轮次、stability-runs 次数等参数在 CI 配置和多份文档中重复，无指向关系。

## Promotion Candidates

### 应立即更新到 `reference/metrics-observability.md`
- metrics pre-init 行为（三引擎启动时预初始化所有 label 组合，确保 Prometheus 从启动起暴露所有时间序列）作为三引擎共享的核心可观测契约。

### 应立即更新到 `must/conventions.md`
- tag-release.sh 双 tag 约定（vX.Y.Z + pine-go/vX.Y.Z）补充到版本同步段落。
- **彻底消除跨验证层数硬编码**：所有文档中的"N 层"改为"多层（详见 scripts/cross-validate.sh）"。

### 应立即更新到 `guides/ci-quality-baseline.md`
- 跨引擎 benchmark 基础设施（scripts/go-bench.sh, java-bench.sh, python-bench.sh）作为新的 CI 能力段落。
- CI 性能参数（diff-fuzz 轮次、stability-runs）只在此文档中保留一处，其他文档改为引用指针。

### 应立即更新到 `architecture/dag-engine.md`
- Pine-Java 并发模型从 ForkJoinPool.commonPool 更新为 Virtual Threads。

### 仅保留在 memory
- 具体的版本跨度时间线（21 个 commit 清单）。
- tag-release.sh 的具体实现细节（sed 命令、git tag 参数）。
- 各 benchmark 脚本的内部逻辑。
- Maven Central 发布的具体 POM 配置。

## Follow-up

1. **本次更新中必须执行**：将所有文档中跨验证层数硬编码替换为通用描述。这已是第四次复盘指出同一问题，不应再有第五次。
2. **考虑在 conventions.md 中添加规则**："CI 性能参数（轮次、并发数、阈值）只在 ci-quality-baseline.md 中维护一处，其他文档不得重复。"
3. **考虑在 standard-workflow.md 中添加触发规则**："新增脚本、新增 CI 能力、变更并发模型的 commit 必须伴随 llmdoc 更新或至少标记 TODO。"
4. **评估文档同步频率下限**：当累积 commit 超过 10 个或跨越 2 个小版本时，应触发 llmdoc 全量审视。
