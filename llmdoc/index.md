# Pineapple llmdoc 索引

本索引是 Pineapple 持久化文档的全局导航，按分类列出所有稳定文档及其检索路径。

## must/

- `llmdoc/must/conventions.md` — 跨代码库约定：算子命名、JSON 作为 Python/Go 契约、blank-import 注册、版本同步、codegen 新鲜度、测试规范。

## overview/

- `llmdoc/overview/project-overview.md` — Pineapple 是什么、系统边界在哪里，以及 Python DSL + Go 运行时拆分的设计决策；其中包括可复用 HTTP server 的职责边界与 middleware 注入位置。

## architecture/

- `llmdoc/architecture/dag-engine.md` — 核心引擎架构：配置编译流水线、DAG 推导规则、调度模型、DataFrame 语义、算子类型约束、行依赖行为，以及引擎级 option / 根级配置注入（`storage_mode`、`log_prefix`、`debug`）、服务端 reload 集成与 HTTP middleware 包装边界、双通道运行时观测（/stats 原子统计 + 可插拔 Provider metrics）。
- `llmdoc/architecture/apple-compiler.md` — Python DSL 架构：Flow 声明 API、编译流水线、校验规则、控制流降级、资源声明处理，以及根级配置字段扩展路径（如 `storage_mode`、`log_prefix`、`debug`）。

## guides/

- `llmdoc/guides/standard-workflow.md` — 标准工作流程：llmdoc 加载、plan mode 对齐、任务跟踪、逐步验证、文档同步。
- `llmdoc/guides/ci-quality-baseline.md` — CI 工程质量基线：lint/test/coverage/fuzz/release-gate 架构与接入约定。
- `llmdoc/guides/investigation-to-fix-testing.md` — 从调查到修复的测试策略：按缺陷类型选择测试层、最小修复面原则。
- `llmdoc/guides/cross-layer-validation.md` — 跨层语义校验：JSON 边界类型枚举、codegen 语义验证、边界值 E2E、隐含 metadata 契约检测。

## reference/

- `llmdoc/reference/operator-contract.md` — 算子开发参考：接口、Schema 注册契约、可选的 metadata/debug/metrics/stats 钩子、类型/输出限制、保留 JSON 键、命名规范。
- `llmdoc/reference/apple-control-template-syntax.md` — Apple DSL 控制流条件参考：`if_` / `elseif_` 需要使用 `{{field_name}}` 模板语法显式标记字段引用，编译器据此提取依赖并在发射 Lua 前去掉模板标记。
- `llmdoc/reference/metrics-observability.md` — 可插拔观测参考：`pkg/metrics` Provider 契约、引擎/调度器/Lua pool 指标注入、`/stats` 组合响应、Prometheus 适配边界，以及 server middleware 与 observability 的职责分离。

## memory/

- `llmdoc/memory/reflections/bugfix-six-items.md` — 六项缺陷修复与测试补齐的复盘，记录 llmdoc 的有效导航点、缺失信息与可提升为稳定文档的候选项。
- `llmdoc/memory/reflections/fix-output-projection-semantics.md` — 输出投影语义修复复盘，记录 None→[] 编码、projectMap 空列表语义、pre-1.0 兼容性立场。
- `llmdoc/memory/reflections/fix-three-small-defects.md` — 三项小缺陷修复的复盘，补充控制流校验、source 语义与相关文档检索线索。
- `llmdoc/memory/reflections/ci-quality-lint-coverage-fuzz.md` — CI 质量基线补齐的复盘，涵盖 lint、覆盖率产物、fuzz 入口选择与 codegen 目录边界。
- `llmdoc/memory/reflections/fix-empty-trace-on-dag-abort.md` — DAG 中止时 trace 空条目修复复盘，记录预分配零值过滤、文档同步经验。
- `llmdoc/memory/reflections/fix-control-field-pollutes-redis-key.md` — if_ 控制字段污染 Redis key 修复复盘，记录两层过滤策略与 design_doc 假设验证。
- `llmdoc/memory/reflections/fix-ci-lint-v2-release-gate.md` — CI lint v2 迁移与 Release 触发机制两轮修复复盘：从复制检查到 workflow_run 依赖，记录 workflow 隔离模型认知演进。
- `llmdoc/memory/reflections/bench-lua-vs-go-performance.md` — Lua vs Go 原生算子 benchmark 复盘，记录端到端测试下 VM 开销被引擎框架稀释的发现，以及预估偏差反思。
- `llmdoc/memory/reflections/isolated-bench-and-resource-ops.md` — 隔离 benchmark 与资源消费算子复盘，记录引擎框架稀释效应量化、BuildOperator 暴露、inventory 幻觉教训。
- `llmdoc/memory/reflections/dag-visualization.md` — DAG 可视化功能复盘，记录模块放置、OperatorType 编译时填充依赖、文档同步经验。
- `llmdoc/memory/reflections/resource-config-hot-reload.md` — 资源配置热加载复盘，记录原子替换 Manager 策略、跨包测试 helper 导出教训、所有权区分简化。
- `llmdoc/memory/reflections/dag-viz-transitive-reduction-and-layout.md` — DAG 可视化传递性归约与纵向布局复盘，记录渲染层归约策略（已被执行图归约取代）。
- `llmdoc/memory/reflections/execution-graph-transitive-reduction.md` — 执行图传递性归约复盘，记录归约从渲染层下沉到 Build() 阶段的决策、测试直接边断言的教训。
- `llmdoc/memory/reflections/column-store-dataframe.md` — 列存 DataFrame 实现复盘，记录 Frame 接口抽象、`[]any` 设计选择、benchmark 数据与适用场景分析。
- `llmdoc/memory/reflections/apple-dsl-storage-mode.md` — Apple DSL storage_mode 支持复盘，记录根级配置扩展模式与文档同步教训。
- `llmdoc/memory/reflections/control-op-explicit-naming.md` — 控制算子显式命名复盘，记录从 `transform_by_lua_HASH` 到 `if_1`/`else_N` 的改进动机与实现。
- `llmdoc/memory/reflections/fine-grained-frame-concurrency.md` — Frame 并发自治复盘，记录调度器全局锁下沉到 Frame 内部、双锁→单锁回退决策、cache line 膨胀教训。
- `llmdoc/memory/reflections/remote-pineapple-operator.md` — transform_by_remote_pineapple 算子实现复盘，记录同包 helper 重名、工作流遗漏、测试 API 误用教训。
- `llmdoc/memory/reflections/data-parallel-framework.md` — 算子级数据并行框架复盘，记录 Transform-only 设计收敛、common_output 禁止决策、上下文管理教训。
- `llmdoc/memory/reflections/apple-dsl-data-parallel-validation.md` — Apple DSL 侧 data_parallel 编译期校验复盘，记录保留字段提取、校验接入、哈希命名漂移教训。
- `llmdoc/memory/reflections/test-coverage-server-transform.md` — `pkg/server` 与 `operators/transform` 测试覆盖率补齐复盘，记录 handler 直测、原子指针注入、miniredis 模式与不测边界。
- `llmdoc/memory/reflections/review-driven-resource-lookup-fixes.md` — 评审驱动的 resource_lookup 修复复盘，记录 JSON 边界类型枚举、codegen 语义验证、跨层 E2E 与隐含 metadata 契约检查的缺口。
- `llmdoc/memory/reflections/recurring-errcheck-ci-failure.md` — errcheck CI 失败复现复盘，记录已有警告未生效根因与标准工作流强化方案。
- `llmdoc/memory/reflections/global-log-prefix.md` — 全局日志前缀功能复盘，记录 root-level `log_prefix` 扩展路径、标准库全局 logger 约束，以及首版遗漏 `Lshortfile` 的教训。
