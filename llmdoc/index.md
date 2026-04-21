# Pineapple llmdoc 索引

本索引是 Pineapple 持久化文档的全局导航，按分类列出所有稳定文档及其检索路径。

## must/

- `llmdoc/must/conventions.md` — 跨代码库约定：算子命名、JSON 作为 Python/Go 契约、blank-import 注册、版本同步、codegen 新鲜度、测试规范。

## overview/

- `llmdoc/overview/project-overview.md` — Pineapple 是什么、系统边界在哪里，以及 Python DSL + Go 运行时拆分的设计决策。

## architecture/

- `llmdoc/architecture/dag-engine.md` — 核心引擎架构：配置编译流水线、DAG 推导规则、调度模型、DataFrame 语义、算子类型约束、行依赖行为。
- `llmdoc/architecture/apple-compiler.md` — Python DSL 架构：Flow 声明 API、编译流水线、校验规则、控制流降级、资源声明处理。

## guides/

- `llmdoc/guides/standard-workflow.md` — 标准工作流程：llmdoc 加载、plan mode 对齐、任务跟踪、逐步验证、文档同步。

## reference/

- `llmdoc/reference/operator-contract.md` — 算子开发参考：接口、Schema 注册契约、可选的 metadata/debug 钩子、类型/输出限制、保留 JSON 键、命名规范。

## memory/

- `llmdoc/memory/reflections/bugfix-six-items.md` — 六项缺陷修复与测试补齐的复盘，记录 llmdoc 的有效导航点、缺失信息与可提升为稳定文档的候选项。
- `llmdoc/memory/reflections/fix-output-projection-semantics.md` — 输出投影语义修复复盘，记录 None→[] 编码、projectMap 空列表语义、pre-1.0 兼容性立场。
- `llmdoc/memory/reflections/fix-three-small-defects.md` — 三项小缺陷修复的复盘，补充控制流校验、source 语义与相关文档检索线索。
- `llmdoc/memory/reflections/ci-quality-lint-coverage-fuzz.md` — CI 质量基线补齐的复盘，涵盖 lint、覆盖率产物、fuzz 入口选择与 codegen 目录边界。
