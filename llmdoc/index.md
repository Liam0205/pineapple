# Pineapple llmdoc 索引

本索引是 Pineapple 持久化文档的全局导航，按分类列出所有稳定文档及其检索路径。

## must/

- `llmdoc/must/conventions.md` — 跨代码库约定：算子命名、JSON 作为 Python/Go/Java 契约、blank-import 注册、版本同步、codegen 新鲜度、测试规范、外部 I/O 安全默认值（LimitReader、sync.Once、goroutine 生命周期）。

## overview/

- `llmdoc/overview/project-overview.md` — Pineapple 是什么、系统边界在哪里，以及 Python DSL + Go/Java 双运行时拆分的设计决策；其中包括可复用 HTTP server 的职责边界与 middleware 注入位置。

## architecture/

- `llmdoc/architecture/dag-engine.md` — 核心引擎架构：配置编译流水线、DAG 推导规则、调度模型、DataFrame 语义、算子类型约束、行依赖行为，以及引擎级 option / 根级配置注入（`storage_mode`、`log_prefix`、`debug`）、Server struct 生命周期与 context 传播、服务端 reload 集成与 HTTP middleware 包装边界、双通道运行时观测（/stats 原子统计 + 可插拔 Provider metrics）、Pine-Java 完整功能对等描述（18 算子、Option pattern、ColumnFrame、结构化错误、CancellationToken 取消传播、DebugAware/MetricsAware 注入、Lua 池化沙箱 baseline snapshot/restore、GoFormat 跨运行时格式兼容、Server/Codegen、ResourceRegistry codegen-time schema 导出、Javadoc metadata contract 解析）。
- `llmdoc/architecture/apple-compiler.md` — Python DSL 架构：Flow 声明 API、编译流水线、校验规则、控制流降级、资源声明处理，以及根级配置字段扩展路径（如 `storage_mode`、`log_prefix`、`debug`）。

## guides/

- `llmdoc/guides/standard-workflow.md` — 标准工作流程：llmdoc 加载、plan mode 对齐、任务跟踪、逐步验证、文档同步。
- `llmdoc/guides/ci-quality-baseline.md` — CI 工程质量基线：lint/test/coverage/fuzz/release-gate 架构与接入约定。
- `llmdoc/guides/investigation-to-fix-testing.md` — 从调查到修复的测试策略：按缺陷类型选择测试层、最小修复面原则。
- `llmdoc/guides/cross-layer-validation.md` — 跨层语义校验：JSON 边界类型枚举、codegen 语义验证、边界值 E2E、隐含 metadata 契约检测。

## reference/

- `llmdoc/reference/operator-contract.md` — 算子开发参考：接口、Schema 注册契约、可选的 metadata/debug/metrics/stats 钩子、类型/输出限制、保留 JSON 键、命名规范、网络调用安全约束（SSRF 防护、LimitReader、fail_on_error 模式）。
- `llmdoc/reference/apple-control-template-syntax.md` — Apple DSL 控制流条件参考：`if_` / `elseif_` 需要使用 `{{field_name}}` 模板语法显式标记字段引用，编译器据此提取依赖并在发射 Lua 前去掉模板标记。
- `llmdoc/reference/metrics-observability.md` — 可插拔观测参考：`pkg/metrics` Provider 契约、引擎/调度器/Lua pool 指标注入、`/stats` 组合响应、Prometheus 适配边界，以及 server middleware 与 observability 的职责分离。
- `llmdoc/reference/dag-visualization.md` — DAG 可视化参考：`RenderDAG` / `WithCollapse` API、SubFlow 折叠规则、`GET /dag` 参数与 DOT/Mermaid 输出约定。

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
- `llmdoc/memory/reflections/concurrent-safe-opt-in.md` — `data_parallel` 并发安全模型改为 `ConcurrentSafe` 显式 opt-in，记录能力检查下沉到 Go 引擎实例层、Apple 仅保留结构校验，以及 blocklist 漂移问题的收敛。
- `llmdoc/memory/reflections/test-coverage-server-transform.md` — `pkg/server` 与 `operators/transform` 测试覆盖率补齐复盘，记录 handler 直测、原子指针注入、miniredis 模式与不测边界。
- `llmdoc/memory/reflections/review-driven-resource-lookup-fixes.md` — 评审驱动的 resource_lookup 修复复盘，记录 JSON 边界类型枚举、codegen 语义验证、跨层 E2E 与隐含 metadata 契约检查的缺口。
- `llmdoc/memory/reflections/recurring-errcheck-ci-failure.md` — errcheck CI 失败复现复盘，记录已有警告未生效根因与标准工作流强化方案。
- `llmdoc/memory/reflections/global-log-prefix.md` — 全局日志前缀功能复盘，记录 root-level `log_prefix` 扩展路径、标准库全局 logger 约束，以及首版遗漏 `Lshortfile` 的教训。
- `llmdoc/memory/reflections/nested-subflow-multi-skip-design-gaps.md` — nested SubFlow multi-skip 设计缺口复盘，记录单路径思维、IR 原地变异非幂等性、声明期/编译期字段名漂移三类遗漏根因与防范方法。
- `llmdoc/memory/reflections/deep-risk-audit-post-v061.md` — v0.6.1 后深度风险审计复盘，记录 server reload 一致性、Lua pool 生命周期、data_parallel 可重入等运行时操作健壮性缺口。
- `llmdoc/memory/reflections/buildinput-sparse-fix.md` — BuildInput 稀疏语义修复复盘，记录 missing vs explicit-nil 边界、row/column parity 与文档纠偏点。
- `llmdoc/memory/reflections/security-audit-fixes.md` — 安全与正确性审计修复复盘，记录 Lua 沙箱白名单、PanicError 信息分级、Registry 严格参数、HTTP 超时加固、控制流声明期校验等决策。
- `llmdoc/memory/reflections/medium-severity-audit-fixes.md` — 第二轮中等严重度审计修复复盘，记录 Server struct 重构、watchConfig 生命周期、SSRF 防护模型、LimitReader 安全默认、Redis 错误透传模式。
- `llmdoc/memory/reflections/pine-java-full-implementation.md` — Pine-Java 从零到 Schema 独立的完整实现复盘，记录双独立源决策、cancelLatch 模式、命名延迟成本、三层交叉验证架构。
- `llmdoc/memory/reflections/pine-java-audit-parity-rounds-3-4.md` — Pine-Java 第三/四轮 Go-parity 审计复盘，记录 CancellationToken、DebugAware/MetricsAware 注入、NopProvider、Lua pool 目标清理策略、"accepted design difference"流程教训。
- `llmdoc/memory/reflections/pine-java-audit-round5-fixes.md` — Pine-Java 第五轮（最终）Go-parity 审计复盘，记录 34 项差异处理（20 fix Java/3 fix Go/9 accepted/5 platform）、Lua pool baseline snapshot 策略演进、GoFormat 跨算子抽象、独立重审发现第四轮回归的教训。
- `llmdoc/memory/reflections/pine-java-audit-round6-fixes.md` — Pine-Java 第六轮 Go-parity 审计复盘，记录 19 项差异处理（14 fix Java/3 fix Go/2 accepted）、GoFormat 统一为跨算子格式化单一事实源、ResourceRegistry codegen-time 模式、前轮"已验证"项回归教训。
- `llmdoc/memory/reflections/pine-java-parity-rounds-7-8.md` — Pine-Java 第七/八轮 Go-parity 审计复盘，记录 CancellationToken parent-child 层级隔离、ResourceAware 编译期注入、GoFormat 1e6 阈值修正、Throwable 结构化包装四项核心修复与注入时机设计教训。
- `llmdoc/memory/reflections/pine-java-parity-round-9.md` — Pine-Java 第九轮 Go-parity 审计复盘，记录 OperatorException checked 边界（error vs panic 映射）、CompletableFuture 事件驱动调度器、GoFormat 边角（Infinity/List/小数阈值）、Server 响应体对齐，以及 fixture-based 格式化验证缺位的教训。
- `llmdoc/memory/reflections/pine-java-parity-round-10.md` — Pine-Java 第十轮 Go-parity 审计复盘，记录约定引入未全扫描导致的清扫轮、IEEE 754 -0.0 符号位跨运行时差异、JSON key 排序确定性、验证消息类型信息缺失教训。
- `llmdoc/memory/reflections/pine-java-parity-round-11.md` — Pine-Java 第十一轮 Go-parity 审计复盘，记录 applyOutput 错误分类 cargo-cult 简化、wire format 警告前缀与 item 索引遗漏、debug 日志计数对象错误，以及 parity gap 收敛信号（仅 4 项）。
- `llmdoc/memory/reflections/pine-java-parity-round-12.md` — Pine-Java 第十二轮 Go-parity 审计复盘，记录类型违规 OperatorException 边界遗漏、ParallelExecutor 错误归属占位符、Registry.buildOperator 错误包装缺失、ValidateOutput 格式对齐，以及 OperatorException 迁移长尾效应的再次验证。
- `llmdoc/memory/reflections/pine-java-parity-round-13.md` — Pine-Java 第十三轮 Go-parity 审计复盘，记录 TransformResourceLookup OperatorException 遗漏（迁移四轮未清）、GoFormat sprint -0.0 多路径盲区、TransformRemotePineapple handleError 路由语义差异、Engine debug 日志对齐。
- `llmdoc/memory/reflections/pine-java-parity-round-14.md` — Pine-Java 第十四轮 Go-parity 审计复盘，记录 GoFormat -0.0 三轮命中全部三条路径（formatG→sprint→formatFloatF）、OperatorException 迁移第五轮仍发现 IllegalArgumentException、Engine.applyOutput 双重包装（简化重构引入新 bug）。
- `llmdoc/memory/reflections/pine-java-parity-round-15.md` — Pine-Java 第十五轮 Go-parity 审计复盘，记录 GoFormat.formatFloatF NaN/Infinity 显式守卫（同模块 edge case 第四轮重复验证）、TransformNormalize 错误消息格式对齐，以及审计收敛信号（0 HIGH/2 MEDIUM deferred）。
- `llmdoc/memory/reflections/pine-java-parity-round-16.md` — Pine-Java 第十六轮 Go-parity 审计复盘，记录 TransformByLua pool.borrow() null 返回模式（Go error return 映射）、Engine.renderDAG ValidationError 类型迁移，以及 OperatorException 迁移七轮长尾总结与审计正式收敛。
