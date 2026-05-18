# P2 重构 + 跨验证框架扩展 + CLI 错误输出对齐 + 开发者脚本基础设施复盘

## Task
- 自 monorepo 重构（fd8171b）以来，Pine-Java 分支完成了一批累积性变更：
  - 13 个 P2 代码质量重构提交（Registry instance-based、CancellationToken interface、OperatorParams、Engine extract runOperator/CompiledOperator、inject ExecutorService、exception 签名收窄等）
  - 跨验证框架从 3 层扩展到 7 层（新增 column-store、error、server HTTP 17 项、cancellation）
  - Fixture 从 pine-go/fixtures/ 提升到仓库根 fixtures/（三子目录: operators/pipelines/errors）
  - 14 个标准化开发者脚本（scripts/ 完整搭建）
  - Wire format parity rounds 8-12（JSON escape、key ordering、pretty-print、CLI error output）
  - CLI 错误输出从 Java stack trace 改为 Go 风格 clean 单行文本

## Expected vs Actual
- Expected: 上述变更完成后 llmdoc 应同步反映最新状态——路径引用正确、跨验证描述准确、重构后的接口描述一致、脚本基础设施有文档入口。
- Actual: llmdoc 出现四类过时问题：(1) fixture 路径再次失效；(2) "三层交叉验证"描述已不准确（实为 7 层）；(3) CancellationToken/Registry 描述与重构后实际不符；(4) scripts/ 目录完全缺乏文档入口。

## What Went Wrong

### 1. fixture 路径引用再次批量失效
Monorepo 重构时已执行过一次全局路径修复（fd8171b），但后续 fixture 从 `pine-go/fixtures/` 提升到仓库根 `fixtures/`（commit 2394032）时，llmdoc 中的路径引用（如 conventions.md 中的 `pine-go/fixtures/`）未同步更新。同一类问题在两个月内第二次出现。

### 2. 跨验证框架描述迅速过时
conventions.md 写明"三层交叉验证"（Schema/Config/Execution），但实际 cross-validate.sh 已扩展为 7 层检查（codegen schema + codegen Python output + render-DAG + execution + column-store + error + server HTTP 17 项 + cancellation）。写死具体层数使文档在框架每次扩展时都需要手动更新。

### 3. P2 重构累积效应被忽视
13 个重构提交逐个看都是"不影响外部接口的内部改进"，但累积起来导致文档中的以下描述与实际不符：
- CancellationToken：文档描述为 volatile boolean class → 实际已改为 interface（commit f923889）
- Registry：文档描述为 static-only 注册 → 实际已改为 instance-based（commit 4e67418）
- Engine.execute：文档描述为单体方法 → 实际已 extract runOperator/CompiledOperator（commits 8285470, 0fa729d）

### 4. 开发者工具基础设施缺乏文档入口
14 个脚本被创建（apple-compile/bump-version/codegen/cross-validate/go-bench/go-fuzz/go-test/java-bench/java-fuzz/java-test/lint/render-dag/run-pipeline/test-all），但 llmdoc 中（包括 project-overview.md）完全未提及。新开发者无法从文档发现这些工具的存在。

## Root Cause

1. **路径引用缺乏间接层** -- llmdoc 直接使用文件系统路径引用 fixture 和源文件。monorepo 重构复盘（`monorepo-restructure-and-java-infra.md`）中已提出"逻辑前缀映射"方案，但未落地执行。同类问题再次发生证明"识别问题"和"解决问题"之间的执行差距。

2. **活跃演进功能使用定量描述** -- "三层"是一个在某个时间点准确但注定会过时的描述。对于快速迭代的框架，应使用通用描述 + 指向实际脚本的检索指针（如"详见 scripts/cross-validate.sh"），避免文档中的数字需要频繁手动同步。

3. **"内部改进不影响接口"的判断标准过窄** -- 对于 LLM 辅助开发场景，llmdoc 中对内部实现的描述本身就是"接口"的一部分。重构改变了类的性质（class→interface、static→instance）时，即使公共 API 不变，llmdoc 中的实现描述也需要同步更新。

4. **新增基础设施的文档入口被遗漏** -- 脚本是逐步增量添加的（先有 3 个，后补到 14 个），每次增量时都觉得"还不完整，等做完再统一写文档"，最终全部做完后忘记补文档。

## Missing Docs or Signals

- `overview/project-overview.md` 中缺少 `scripts/` 目录概述段落，无法从文档发现开发者工具。
- `must/conventions.md` 中 "三层交叉验证" 应改为通用描述 + 指向 scripts/cross-validate.sh 的检索指针。
- `must/conventions.md` 中 fixture 路径引用仍指向 `pine-go/fixtures/` → 应为 `fixtures/`（仓库根）。
- `architecture/dag-engine.md` 中 Pine-Java 实现描述（CancellationToken、Registry）与重构后实际不符。
- 无"重构类变更也需要 llmdoc 同步检查"的流程约定。

## Promotion Candidates

### 应提升到 `overview/project-overview.md`
- **scripts/ 目录概述** -- 列出 14 个标准化脚本的分类和用途（编译/测试/验证/基准/lint），使新开发者可以从文档发现工具入口。

### 应提升到 `must/conventions.md`
- **修正 fixture 路径** -- 将所有 `pine-go/fixtures/` 引用更新为 `fixtures/`（仓库根），并记录三子目录（operators/pipelines/errors）的用途。
- **跨验证描述改为通用形式** -- 将"三层交叉验证"改为"多层跨验证框架（详见 scripts/cross-validate.sh）"，避免层数硬编码。
- **重构类变更的 llmdoc 检查约定** -- 当重构改变了类/接口的性质（如 class→interface、static→instance、单体→拆分）时，应检查 llmdoc 中是否有对应描述需要更新。

### 应提升到 `architecture/dag-engine.md`
- **CancellationToken** -- 从 "volatile boolean class" 更新为 "interface（支持 parent-child 层级隔离）"。
- **Registry** -- 从 "static-only 注册" 更新为 "instance-based（支持测试隔离和并行引擎实例）"。
- **Engine 内部结构** -- 补充 CompiledOperator 和 runOperator 的职责分离。

### 仅保留在 memory
- 13 个 P2 重构的具体实现细节（inject ExecutorService、defensive copies、AtomicBoolean）-- 实现细节，不具有架构约束力。
- Wire format parity rounds 8-12 的具体修复项（JSON escape、key ordering、pretty-print）-- 已在各自的 round 复盘中记录。
- CLI 错误输出格式修改的具体 catch 块变更 -- 一次性实现决策。

## Follow-up
1. 更新 `must/conventions.md`：修正 fixture 路径引用，将"三层"改为通用描述 + 脚本指针。
2. 更新 `overview/project-overview.md`：添加 scripts/ 概述段落。
3. 更新 `architecture/dag-engine.md`：同步 CancellationToken interface、Registry instance-based、Engine 内部结构变更。
4. 考虑在 `guides/standard-workflow.md` 中添加"重构类变更的 llmdoc 检查步骤"。
5. 评估是否引入"逻辑路径前缀映射"机制，从根本上避免路径失效复发（此方案在 monorepo 重构复盘中已提出但未实施）。
