---
name: pine-cpp-mvp-to-full-runtime
description: pine-cpp 从 MVP 文档化到完整第四运行时的 34 个 commit 累积期暴露的 llmdoc 维护教训
type: reflection
---

# pine-cpp：MVP 到完整第四运行时的文档漂移复盘

## 任务背景

上次 llmdoc 同步发生在 commit 7698a43（"docs: update README, design doc, and llmdoc for pine-cpp MVP"）。此后 34 个 commit 内，pine-cpp 完成了从"CLI MVP"到"完整第四运行时"的跨越：HTTP server、`/execute`、`/stats`、hot-reload、13 层 cross-validate 全覆盖、4 个 CI job（cpp-build / cpp-lint / cpp-asan / cpp-test）、ColumnFrame/ColumnStore/Column 类型层级、PanicError、OperatorOutput write-log 模式、per-node thread 并发执行，以及 observe active-reader 语义修正。

34 个 commit 结束时，llmdoc 仍保留 MVP 阶段的描述：`pine-cpp-runtime.md` 的"MVP 边界"一节整段过时；`index.md`、`startup.md`、`must/conventions.md`、`guides/ci-quality-baseline.md`、`overview/project-overview.md`、`reference/metrics-observability.md`、`architecture/dag-engine.md` 均存在不同程度的连带失效。

---

## 关键教训

### 1. "MVP 边界"作为文档措辞天生有时效性，不应写入主体

`pine-cpp-runtime.md` 中专门设了"MVP 边界"章节，列出"暂不实现 /execute、/stats、热加载……但在提交正式 PR 之前必须补齐"。这段话的意图是好的——它标记了当前限制，也声明了终态目标。但其结构决定了它必然过时：一旦目标完成，这段文字不会"自动消失"，而是继续被 AI 读到并误判为当前状态。

**避免方式**：架构文档只描述当前已实现的能力，历史演进路径留给 reflection 或 changelog。如果要在文档中标记"尚未实现"，使用带日期或版本的注记，而不是独立章节。

### 2. 引擎数量硬编码再次失效（本次为第五次）

`index.md`、`startup.md`、`must/conventions.md`、`overview/project-overview.md` 以及 `guides/ci-quality-baseline.md` 中均写"三引擎"或"Go/Java/Python 三运行时"。此前以下复盘均指出了相同问题：

- `p2-refactor-cross-validate-scripts.md`（3→7 层）
- `pine-python-and-v07-dag-overhaul.md`（三运行时首次过时）
- `extensibility-parity-tests-and-java-prefix-fix.md`（12 层）
- `v072-074-llmdoc-update.md`（13 层，第四次）
- **本次**（四运行时，第五次）

历次复盘都建议"改为通用描述"，但未落地，因为落地动作被推迟到"下次一起改"。

**避免方式**：本次更新必须在同一 PR 中将所有运行时计数改为通用措辞（"各运行时"/"当前所有运行时"），并在 `must/conventions.md` 中新增规则——禁止在任何文档主体硬编码运行时数量或跨验证层数，统一指向 `scripts/cross-validate.sh` 和运行时列表表格。

### 3. "准备一次性集中更新"心理模型是拖延的根因

34 个 commit 对应的开发周期中，每个单次提交看起来都"太小，不值得单独更新文档"：

- "加个 ColumnStore" — 感觉是内部实现细节
- "加 PanicError" — 一个错误类型
- "加 CI job" — CI 配置，不是架构
- "接入 cross-validate 第 N 层" — 测试基础设施

但累积到 34 个 commit 时，llmdoc 已经不是"轻微滞后"，而是"整段错误"。开发者在每次提交时的隐含判断是"这个改动的 llmdoc 更新可以和下一个一起做"，这个"下一个"永远没有到来。

**避免方式**：在 `guides/standard-workflow.md` 中明确以下触发规则："新增运行时能力（HTTP 路由、新 CI job、新数据类型、新并发模型）的 commit 必须在同一 PR 中包含 llmdoc delta，或在 commit message 中显式标记 `docs-debt`。" 10 个以上 docs-debt commit 应触发强制 llmdoc 清理。

### 4. 新核心概念未进入 llmdoc 的发现路径缺失

以下概念在 34 个 commit 内成为 pine-cpp 的核心，但从未进入任何 llmdoc 文档：

| 概念 | 为何重要 | 漏记原因 |
|---|---|---|
| **ColumnFrame** | pine-cpp 的主执行表示，取代 DataFrame | 被认为是"内部实现"，但实际影响算子开发契约 |
| **OperatorOutput write-log 模式** | 所有算子统一通过 write-log 写回，影响 parity 审计 | 纯 C++ 内部模式，未映射到跨运行时文档 |
| **PanicError** | 运行时恢复与错误分级的关键机制 | 已有 security-audit-fixes reflection 提到，未提升到稳定文档 |
| **per-node thread 并发** | pine-cpp 的调度模型，与 Go/Java 不同 | 被归入"实现细节" |
| **observe active-reader 语义** | 影响所有运行时的 DAG 节点标记规则 | 修正发生在 `dag-engine.md` 的一个 commit，反而得到了更新 |

**避免方式**：在新运行时的开发开始时，在 `pine-cpp-runtime.md` 中预先为"关键数据类型"和"执行模型"留出章节占位符，强迫开发者在实现对应能力时填充，而不是事后追补。

### 5. cross-validate 层数从 CI 脚本可以自动推断，但文档未建立指向关系

13 层 cross-validate 接入 pine-cpp 后，`ci-quality-baseline.md` 和 `conventions.md` 中的层数描述立即过时。但层数是 `scripts/cross-validate.sh` 的一个事实——文档如果直接指向"详见 cross-validate.sh 第 N 行"，这个过时就不会发生。

**避免方式**：删除所有文档中对 cross-validate 层数的具体数字，替换为 `scripts/cross-validate.sh` 的引用指针。这是本次更新必须完成的操作之一。

---

## Promotion 候选

以下内容应在本次 llmdoc 更新中提升到稳定文档：

### 立即更新到 `architecture/pine-cpp-runtime.md`
- 删除"MVP 边界"章节，替换为"已实现能力"段落（HTTP server `/execute` `/stats`、hot-reload、ColumnFrame 执行路径、per-node thread 调度、OperatorOutput write-log、PanicError dispatch recovery）
- 补充 ColumnFrame 与 Column 类型层级的架构描述
- 更新测试策略一节，反映 4 个 CI job 和 13 层 cross-validate

### 立即更新到 `architecture/dag-engine.md`
- observe active-reader 语义已由 commit 6798faa 更新，确认此条已同步

### 立即更新到 `guides/ci-quality-baseline.md`
- 补充 4 个 pine-cpp CI job（cpp-build / cpp-lint / cpp-asan / cpp-test）
- 删除 cross-validate 层数硬编码

### 立即更新到 `must/conventions.md`
- 三运行时改为四运行时（pine-go / pine-java / pine-python / pine-cpp）
- 版本同步范围补充 pine-cpp
- 增加"禁止在文档中硬编码运行时数量和跨验证层数"规则

### 立即更新到 `overview/project-overview.md`
- 三运行时定位更新为四运行时

### 立即更新到 `reference/metrics-observability.md`
- pine-cpp metrics 接入与 pre-init 行为

### 仅保留在 memory
- 34 个 commit 的具体时间线与 GAP 编号（GAP2 / GAP4 / GAP5）
- ColumnFrame 的 C++ 实现细节（`shared_ptr<const Column>` COW 策略）
- per-node thread 的具体线程模型参数
- 各 CI job 的具体编译标志（`-Werror`、`-fsanitize=address,undefined`）

---

## 下次类似任务的检查清单（不超过 5 条）

1. **新运行时里程碑 commit**（HTTP server 上线、首次接入 cross-validate、新 CI job 合入）必须在同一 PR 中携带 llmdoc 更新，不得推迟。

2. **任何在文档中写"MVP 边界"或"暂不实现"的段落**，必须同时在同一文档末尾附上"此段落的删除条件"，并在条件满足时作为 follow-up 任务立即执行。

3. **运行时数量、层数等定量描述**不得出现在文档主体文字中，只允许出现在有维护责任人的表格或指向脚本的引用中。

4. **新核心概念**（影响多算子或影响 parity 审计的数据类型/模式）上线时，开发者负责在对应架构文档中添加或更新对应章节，不得仅停留在 reflection。

5. **累积 10 个以上未同步 docs-debt commit 时**，下一个任务开始前先做 llmdoc 全量审视，不能在漂移状态下继续新功能开发。
