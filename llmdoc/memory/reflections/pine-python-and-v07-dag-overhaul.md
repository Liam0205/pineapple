---
name: pine-python-and-v07-dag-overhaul
description: Pine-Python 第三运行时上线、v0.7 DAG 语义重构（ConsumesRowSet/MutatesRowSet/AdditiveWritesRowSet）、Cross-validate 11 层扩展复盘
type: reflection
---

## 概述

自 bd1354e 以来落地五项重大变更：(1) pine-python 完整第三运行时；(2) v0.7 DAG 语义重构（row_dependency → consumes_row_set + mutates_row_set + additive_writes_row_set）；(3) 跨验证从 7 层扩展到 11 层（+concurrent/raw-byte/hot-reload/redis-integration）；(4) CI 扩展（pine-python-test/fuzz/benchmark + differential-fuzz + nightly workflow）；(5) 版本同步范围扩展到 pine-go/pine-java/pine-python/apple/fixtures 五处。

## 教训

### 1. 第三运行时完全未反映在 llmdoc 中
Pine-Python 是完整的第三引擎实现（ThreadPoolExecutor+GIL、lupa/LuaJIT、mtime-polling hot-reload），但 llmdoc 中 `dag-engine.md` 仅描述 Go 和 Java 两个运行时。新运行时的文档记录滞后于代码完成，导致 AI 辅助开发时无法感知 pine-python 的存在与平台差异。

### 2. DAG 语义重命名后文档引用未清理
`row_dependency` 已重命名为 `consumes_row_set`，且新增 `mutates_row_set` 和 `additive_writes_row_set` 两个布尔标志，旧的 barrier 概念已被三个 marker interface 取代。但 `dag-engine.md` 的"行依赖行为"章节仍使用旧术语，`startup.md` 第 3 条摘要也提及"行依赖"。这是典型的"重命名后搜索替换不完整"问题。

### 3. 版本同步范围和跨验证层数再次硬编码过时
`conventions.md` 中版本同步仅列三组文件（pine-go/apple/fixtures），缺少 pine-java 和 pine-python。跨验证描述为"七层"，实际已扩展到 11 层。与上次 p2-refactor 复盘相同教训再次发生——定量描述在活跃演进模块中注定快速过时。

### 4. CI 文档完全缺少 Python 和 differential-fuzz 相关 job
`ci-quality-baseline.md` 列出 7 个 job，但当前 CI 至少新增 pine-python-test、pine-python-fuzz、pine-python-benchmark、differential-fuzz 四个 job，以及 nightly differential-fuzz workflow（5000 轮 + auto-issue）。

## 文档同步缺口

| 文档 | 过时内容 | 应更新为 |
|------|----------|----------|
| `architecture/dag-engine.md` | 仅描述 Go/Java 双运行时；"行依赖行为"章节使用 `row_dependency` 旧术语 | 补充 Pine-Python 运行时描述；术语替换为 `consumes_row_set`/`mutates_row_set`/`additive_writes_row_set` |
| `must/conventions.md` | 版本同步"三组文件"；跨验证"七层" | 五组文件（+pine-java +pine-python）；层数改为通用描述或更新为 11 |
| `guides/ci-quality-baseline.md` | 7 job 表格；无 differential-fuzz；无 nightly workflow | 补充 Python CI + differential-fuzz job 描述 |
| `overview/project-overview.md` | "Go/Java 双运行时"定位 | 更新为三运行时（Go/Java/Python） |
| `startup.md` | "行依赖" | 更新为 consumes_row_set 三标志模型 |

## 提升为稳定文档的候选项

### 应提升到 `architecture/dag-engine.md`
- Pine-Python 运行时架构：ThreadPoolExecutor+GIL 并发模型、lupa/LuaJIT Lua 执行、mtime-polling hot-reload、ColumnFrame 实现
- v0.7 DAG 语义：三个 marker interface（ConsumesRowSet/MutatesRowSet/AdditiveWritesRowSet）对 `_row_set_` sentinel 的操作语义

### 应提升到 `must/conventions.md`
- 版本同步扩展为五处（pine-go/pine-java/pine-python/apple/fixtures）
- 跨验证层数改为通用描述 + 指向 scripts/cross-validate.sh

### 应提升到 `guides/ci-quality-baseline.md`
- pine-python-test/fuzz/benchmark 三个新 job
- differential-fuzz CI（200 轮）+ nightly（5000 轮 + auto-issue）workflow

### 仅保留在 memory
- Pine-Python 平台特定实现细节（GIL 下 ThreadPoolExecutor 的具体行为、lupa 绑定 API）
- differential-fuzz 的具体轮次数配置（会随时间调整）

## Follow-up
1. 更新 `dag-engine.md`：补充 Pine-Python 章节，替换 row_dependency 为 v0.7 三标志术语。
2. 更新 `conventions.md`：版本同步五处、跨验证层数通用化。
3. 更新 `ci-quality-baseline.md`：补充 Python CI 和 differential-fuzz job。
4. 更新 `overview/project-overview.md`：从"双运行时"改为"三运行时"。
