# Monorepo 重构与 Pine-Java 基础设施补齐复盘

## Task
- 将 Go 引擎从仓库根目录迁移至 `pine-go/` 子目录，形成 `apple/` + `pine-go/` + `pine-java/` 三个平级子项目的 monorepo 结构。
- 为 Pine-Java 从零补齐工程基础设施（P0-P3 四阶段路线图）：版本同步、测试体系、覆盖率、Fuzz、Lint、CI 流水线。
- 将 `docs/` 内部文档迁移至 `pine-java/notes/` 就近管理。

## Expected vs Actual
- Expected: Go 模块路径平滑变更，CI/脚本/llmdoc 引用同步更新，Pine-Java 基础设施一次性补齐。
- Actual: 目标全部达成。Go 模块路径从 `github.com/Liam0205/pineapple` 变为 `github.com/Liam0205/pineapple/pine-go`，93 个文件 import 更新；Pine-Java 在 4 个 commit 中完成 unit/integration/fuzz/lint/coverage/release 全套基础设施。

## What Went Wrong
1. **llmdoc 全部路径引用一次性失效** — Go 从根目录迁出后，所有 llmdoc 中引用 Go 源文件路径（如 `pkg/engine/`、`operators/`）的文档同时失效，需要批量更新。
2. **module path 变更是破坏性变更** — 外部消费者的 import path 全部失效。虽然 Go module proxy 缓存保护了已有 consumer 的旧版本，但新版本需要所有下游更新 import。
3. **内部脚本中硬编码路径** — `scripts/bump-version.sh`、CI workflow 中对 Go 源码位置的硬编码引用需逐个排查修复。

## Root Cause
1. **文档中使用绝对/相对路径引用源文件而非逻辑标识** — llmdoc 直接引用 `pkg/engine/engine.go` 这类路径，当目录结构变动时全部失效。如果使用 `pine-go:pkg/engine/engine.go` 这样带前缀的逻辑标识，则只需更新一处前缀映射。
2. **monorepo 结构未在项目初期确定** — Go 先占据根目录是历史惯性，随着 Java 和 DSL 并行发展，结构不可避免需要重组。越晚重组，影响面越大（93 文件 vs 首日 0 文件）。
3. **基础设施延迟构建** — Pine-Java 功能代码已完成 17 轮 parity 审计，但工程基础设施（测试/lint/coverage/release）直到最后才补齐，前期缺乏质量门禁。

## Missing Docs or Signals
- llmdoc 中无 monorepo 目录结构概览文档 — 应有一处描述三个子项目的职责边界与路径约定。
- llmdoc 中无"路径引用约定" — 未规定文档中引用源文件时应使用何种格式（绝对路径 / 相对路径 / 逻辑前缀）。
- CI 文档未覆盖 Pine-Java 的完整流水线（ServerTest/IntegrationTest/Jacoco/Jazzer/Checkstyle）。
- 无文档描述 `scripts/bump-version.sh` 的覆盖范围变更（现在同时管理 fixtures/examples 版本）。

## Promotion Candidates

### 应提升到 `overview/project-overview.md`
- **仓库目录结构** — 明确描述 `apple/`（Python DSL）、`pine-go/`（Go 运行时）、`pine-java/`（Java 运行时）三个平级子项目，以及 `scripts/`、`llmdoc/` 等顶层目录的职责。

### 应提升到 `must/conventions.md`
- **Go module path** — 记录当前模块路径为 `github.com/Liam0205/pineapple/pine-go`，避免文档/脚本中使用旧路径。
- **文档路径引用约定** — 规定 llmdoc 中引用源文件时使用 `{子项目}/` 前缀（如 `pine-go/pkg/engine/`），使结构变更时影响面可控。

### 应提升到 `guides/ci-quality-baseline.md`
- **Pine-Java CI 基础设施** — 补充 ServerTest、IntegrationTest（E2E）、Jacoco 覆盖率、Jazzer Fuzz、Checkstyle lint 的接入方式与触发条件。
- **跨语言 fixture 边界用例** — 记录 reorder_shuffle、transform_by_lua_format、filter_condition、merge_dedup、transform_normalize 等边界 fixture 的设计意图。

### 仅保留在 memory
- 93 文件批量 import 更新的具体操作方式 — 一次性操作，不会复现。
- P0-P3 路线图优先级排序经验 — 流程偏好，不具有架构约束力。
- `docs/` → `pine-java/notes/` 迁移决策 — 内部文档就近放置的实践偏好。

## Follow-up
- 更新 `overview/project-overview.md`：补充三子项目平级结构描述和新 module path。
- 更新 `must/conventions.md`：添加 Go module path 变更说明与文档路径引用约定。
- 更新 `guides/ci-quality-baseline.md`：补充 Pine-Java 完整 CI 流水线描述。
- 考虑在 llmdoc 中添加路径前缀映射表，使未来结构变动只需更新一处。
