# Pineapple llmdoc 索引

本索引是 Pineapple 持久化文档的全局导航，按分类列出所有稳定文档及其检索路径。

## must/

- `llmdoc/must/conventions.md` — 跨代码库约定：算子命名、JSON 作为 Apple DSL 与各运行时之间的契约、各运行时副作用注册（Go blank-import / Java static init / C++ `PINE_REGISTER_OPERATOR_T`）、版本同步（含 pine-cpp `kVersion`）、codegen 新鲜度、测试规范、外部 I/O 安全默认值（LimitReader、sync.Once、goroutine 与 C++ graceful shutdown 生命周期）、cross-validate 入口指针、跨引擎能力等价审计维度、禁止硬编码定量描述、InputFieldSpec 三态模型（Nullable/Strict/Defaulted）、operator-level debug 三态继承。

## overview/

- `llmdoc/overview/project-overview.md` — Pineapple 是什么、系统边界在哪里，以及 Apple DSL 声明层 + Go / Java / C++ 三运行时拆分的设计决策（含 pine-python 运行时已于 v0.9.7 后移除、资源数据型/句柄型区分）；包含各运行时 CLI/HTTP 入口点（含 pine-cpp server timeout / max-body-size / dag-pool-size / shard-pool-size flag）。

## architecture/

- `llmdoc/architecture/dag-engine.md` — 核心引擎架构：配置编译流水线、DAG 推导规则（三标记 + auto-inject 模型：ConsumesRowSet/MutatesRowSet/AdditiveWritesRowSet 标记与 item 字段自动注入）、调度模型、DataFrame 语义（含 InputFieldSpec 三态模型：Nullable/Strict/Defaulted）、算子类型约束、行集依赖行为，以及引擎级 option / 根级配置注入（含 debug nullable 三态继承）、Server struct 生命周期与 context 传播、服务端 reload 集成与 HTTP middleware 包装边界、双通道运行时观测、ExecutionError/PanicError 因果链（三运行时 cause chain parity）、资源数据型（snapshot 导出）/句柄型（borrow 借用，如 redis_connection）区分、Pine-Java 完整功能对等描述。
- `llmdoc/architecture/apple-compiler.md` — Python DSL 架构：Flow 声明 API、SubFlow 契约声明与编译期强制（`common_input`/`common_output`/`item_input`/`item_output` 在 issue #78 落地为 subtree-scoped 字段覆盖 + 死代码校验，未声明契约的 SubFlow 自动继承外层；`required_resources` 沿用 issue #37 校验）、编译流水线（含 step 8b `_validate_subflow_contracts`）、校验规则（含 `validate_write_without_read` 对 `AdditiveWritesRowSet` 算子的同字段豁免，issue #72）、控制流降级（含 `_rename_field` Lua `_G[]` 语法处理）、资源声明处理、根级配置字段扩展路径（如 `storage_mode`、`log_prefix`、`debug`），以及 row-set 标记三元组（`consumes_row_set` / `mutates_row_set` / `additive_writes_row_set`）通过 `apple_generated/markers.py` 表填充 `OpCall`、True-OR widen 合并语义、`_apply` 与 `_add_op` 的刻意非对称（仅 `consumes_row_set` 暴露给 DSL 调用点）。
- `llmdoc/architecture/pine-cpp-runtime.md` — Pine-C++ 运行时架构：作为标杆运行时的定位、错误/fixture parity 契约、CLI 与 HTTP 入口（含 HTTP/1.1 keep-alive / read-header-timeout / idle-timeout / max-body-size / middleware / graceful shutdown / 客户端断连取消 eventfd 零延迟唤醒）、codegen 入口（`-schema-json` schema 导出 + `-output` 发射完整 Apple DSL 产物集与 Go/Java 字节级一致，`format_g` 对 |d| > LLONG_MAX 的 UB 守卫与 Ryu/Grisu 路由点、ResourceSchema 全局注册表与 `reset_resource_schema_registry`/`reset_all_resource_registries` 拆分语义）、`metrics::Provider` 与 `resource::Manager` 对等（`ResourceValue` 数据 `Variant` XOR 句柄 `shared_ptr<void>` 双通道，数据型走 `snapshot()`、句柄型走 `borrow()`，RAII 拆除）、Frame 多态基类 + ColumnFrame/RowFrame 双物理实现（C++23，per-call 锁形态与 Go/Java 对齐、`pine::SharedMutex` 备件）、Column 类型层级、`PINE_REGISTER_OPERATOR_T` 注册模型、ValidateOutput 类型约束、NaN/Inf 校验、PanicError stacktrace、外部 stop_token 取消、ready-queue DAG 调度器（双隔离线程池 + in-degree 原子追踪）、observe_log/pine-debug 日志。

## guides/

- `llmdoc/guides/standard-workflow.md` — 标准工作流程：llmdoc 加载、plan mode 对齐、任务跟踪、逐步验证、文档同步。
- `llmdoc/guides/ci-quality-baseline.md` — CI 工程质量基线：lint（含 Java checkstyle `failOnViolation=true` + `OneStatementPerLine`、C++ clang-format）/ test / coverage / fuzz / differential-fuzz / cross-validate / nightly cross-runtime benchmark / release-gate 架构与接入约定（含 pine-cpp 的 4 个 CI job 与 cross-validate cpp 二进制注入路径），以及本地 `.githooks/` 体系（`pre-commit` staged-only 格式 gate + `pre-push` 工程级 lint + 自包装 CI watch）。
- `llmdoc/guides/investigation-to-fix-testing.md` — 从调查到修复的测试策略：按缺陷类型选择测试层、最小修复面原则。
- `llmdoc/guides/cross-layer-validation.md` — 跨层语义校验：JSON 边界类型枚举、codegen 语义验证、边界值 E2E、隐含 metadata 契约检测、扩展点对等验证（能力等价）。
- `llmdoc/guides/benchmark-hygiene.md` — Benchmark 噪声卫生：跑前/跑后 load 与残留进程检查、同日同机对照纪律、±5-7% 二进制布局噪声与 perf stat 交叉验证、fixture 代表性（calibrated 为性能决策唯一裁判）、microbench 访问模式戒律、逐 op 删除归因法。

## reference/

- `llmdoc/reference/operator-contract.md` — 算子开发参考：接口、Schema 注册契约、可选的 metadata/debug/metrics/stats 钩子、类型/输出限制、保留 JSON 键、命名规范、网络调用安全约束（SSRF 防护、LimitReader、fail_on_error 模式）、Redis 算子句柄型资源借用契约（`transform_redis_get`/`transform_redis_set` 按 `resource_name` 借用 `redis_connection`、借用失败静默降级；`redis_connection` 参数含 `metrics_name` 资源级指标开关）。
- `llmdoc/reference/apple-control-template-syntax.md` — Apple DSL 控制流条件参考：`if_` / `elseif_` 需要使用 `{{field_name}}` 模板语法显式标记字段引用，编译器据此提取依赖并在发射 Lua 前去掉模板标记。
- `llmdoc/reference/metrics-observability.md` — 可插拔观测参考：跨运行时 `Provider` 契约（pine-go 规范 + pine-cpp/pine-java 对等）、引擎/调度器/Lua pool 指标注入、`/stats` 组合响应（含 `/stats.http` 与 `/stats.resources` 子树 schema）、内置 HTTP metrics middleware（各运行时 default-on）、资源级指标 fan-out（Tee）路由与 Collector 契约、Prometheus 适配边界。
- `llmdoc/reference/dag-visualization.md` — DAG 可视化参考：`RenderDAG` / `WithCollapse` API、SubFlow 折叠规则、`GET /dag` 参数与 DOT/Mermaid 输出约定。
- `llmdoc/reference/admin-pprof-disclosure.md` — pine-go 可选 admin server 的 pprof 暴露面、默认关闭契约、五条 `/debug/pprof/*` 路径的信息泄漏内容、运维约束(网络层隔离/认证反代/诊断窗口)与跨 runtime 对等(pine-cpp/pine-java 不实现 admin pprof)。

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
- `llmdoc/memory/reflections/pine-java-parity-round-17.md` — Pine-Java 第十七轮（最终轮）Go-parity 审计复盘，记录 6 项 LOW 错误消息措辞对齐（首个零 M/H 轮次），标志 parity 审计从行为修复完全过渡到措辞打磨，审计正式完结。
- `llmdoc/memory/reflections/pine-java-parity-rounds-18-19.md` — Pine-Java 第十八/十九轮 Go-parity 审计复盘（最后两轮），记录 8 项 LOW 修复（common-mode CancellationToken 遗漏、错误消息类型/值/引号对齐、trace 微秒精度截断），确认 rounds 1-19 全量审计完结，总计约 90 项差异关闭。
- `llmdoc/memory/reflections/monorepo-restructure-and-java-infra.md` — Monorepo 重构（Go→pine-go/ 子目录）与 Pine-Java 工程基础设施补齐（P0-P3 路线图）复盘，记录 llmdoc 路径批量失效教训、module path 破坏性变更影响、路线图驱动基础设施建设的有效性。
- `llmdoc/memory/reflections/p2-refactor-cross-validate-scripts.md` — P2 重构 + 跨验证框架扩展 + 开发者脚本基础设施复盘，记录 fixture 路径再次失效、定量描述过时、重构累积效应、工具文档入口缺失四项教训。
- `llmdoc/memory/reflections/pine-python-and-v07-dag-overhaul.md` — Pine-Python 第三运行时上线、v0.7 DAG 语义重构（ConsumesRowSet/MutatesRowSet/AdditiveWritesRowSet）、Cross-validate 11 层扩展复盘，记录文档未覆盖新运行时、术语重命名后引用未清理、版本同步范围和跨验证层数硬编码再次过时。
- `llmdoc/memory/reflections/audit-extensibility-blindspot.md` — Parity 审计结构性盲点复盘：19 轮审计+11 层交叉验证全部聚焦"函数等价"（已知路径输出一致），从未验证"能力等价"（下游可用的集成模式是否一致），导致 Java middleware 无法拦截未注册路径的问题在下游项目才暴露。
- `llmdoc/memory/reflections/extensibility-parity-tests-and-java-prefix-fix.md` — 扩展性 parity 测试与 Java 前缀匹配修复复盘，记录 HttpServer 前缀语义二次修复、cross-validate 12 层扩展、深层路径测试发现 bug 的验证价值。
- `llmdoc/memory/reflections/v072-074-llmdoc-update.md` — v0.7.2-v0.7.4 llmdoc 大面积过时复盘，记录 21 commit 跨度下硬编码层数第四次过时、并发模型描述陈旧、CI 参数重复、新能力缺失的系统性原因与防范策略。
- `llmdoc/memory/reflections/dag-implicit-row-set-fix-v080.md` — v0.8.0 DAG 隐式行集依赖修复复盘，记录 auto-inject 机制（三标记模型的第四机制）、6 算子 ConsumesRowSet 清理、dag-differential-fuzz 基础设施、文档中"三标记模型"与"可选 ConsumesRowSet"描述过时。
- `llmdoc/memory/reflections/pine-cpp-mvp-to-full-runtime.md` — pine-cpp 从 MVP 文档化到完整第四运行时的 34 commit 累积期偏差复盘，记录 MVP 措辞时效性、引擎数量第五次失效、累积心理模型与新核心概念发现路径缺失。
- `llmdoc/memory/reflections/pine-cpp-p1-p2-buildout.md` — pine-cpp P1/P2 全运行时建设阶段（18 commit）复盘，记录"上一轮 reflection 未落地是放大器"、跨运行时观测契约表格缺失、CLI flag 作为用户契约的同步要求。
- `llmdoc/memory/reflections/pine-cpp-p3-series-buildout.md` — pine-cpp P3-A to P3-D 阶段复盘，涉及 LuaVM StatePool 隔离、StatsProvider/MetricsAware 基建与 remote pineapple SSRF 保护接入。
- `llmdoc/memory/reflections/cause-chain-and-stats-http.md` — /stats.http 四方对齐与 cause-chain parity 复盘（9 commits），记录跨语言状态辨识盲区、std::rethrow_if_nested footgun、R2 审计反向修正机制、cross-validate 第二种验证模式（probe binary stdout 对比）。
- `llmdoc/memory/reflections/p2-perf-and-review-driven-fixes.md` — P2 性能优化批次与审查驱动修复周期复盘（22 commits），记录 CRTP 注册宏、OperatorOutput 向量化、zero-copy window view、Redis idle bound、progress.md 遗漏增量发现、数据结构级变更需文档同步的教训。
- `llmdoc/memory/reflections/r3-audit-and-fuzz-enhancement.md` — R3 parity audit（26+5 项）+ differential fuzz 增强（11 新维度 + 4 引擎接入）复盘（34 commits），记录 Frame 多态化架构变更、C++23 采纳、HTTP/1.1 keep-alive、fuzz 50-round smoke 立即暴露 17 divergence + RawValue 泄漏的 ROI 验证、dual-impl 等价测试覆盖。
- `llmdoc/memory/reflections/differential-fuzz-discoveries.md` — 30k-round differential fuzz 发现与修复复盘（7 commits），记录 IEEE 754 -0.0 跨语言 hash/equality 差异、C++ item_defaults 投影时机缺陷、并行 recall+paginate 非确定性的生成器规避策略、cross-validate set comparison 防护。
- `llmdoc/memory/reflections/v090-nullable-strict-apple-desync.md` — v0.9.0 Nullable→Strict 翻转后 Apple DSL 契约脱节复盘，记录 JSON 键名 `nullable_*`→`strict_*` 翻转时 Apple DSL 侧未同步、Strict 模式声明能力丧失、unique_name hash 包含失效字段三项缺口，以及"声明→生效"端到端校验缺失的根因。
- `llmdoc/memory/reflections/dag-ready-queue-scheduler.md` — v0.9.2 DAG ready-queue 调度器重写复盘，记录双隔离线程池架构、seed loop 竞态根因（读取 mutable atomic 而非 immutable preds）、eventfd 零延迟唤醒、跨运行时 benchmark 工具链建设，以及 C++ 比 Go 快 60-80% 的吞吐结果。
- `llmdoc/memory/reflections/benchmark-infra-and-cross-runtime-perf.md` — Benchmark 基础设施建设与跨运行时性能优化战役复盘（56 commits），记录 fixture-driven benchmark 工具链、四运行时 bench stub 算子设计、C++ PERF-9~18 优化系列（FlatMap/Variant/RapidJSON/jemalloc/arena/bitmap）、Go/Java lazy proxy + bitmap + GOGC=400 优化、profiling 驱动 ROI 验证、Section 8 端口残留修复。
- `llmdoc/memory/reflections/redis-resourcemanager-migration-and-pine-python-removal.md` — Redis→ResourceManager 句柄型资源迁移 + pine-python 运行时下线 PR 收尾复盘，记录"难而正确"归属判据（可共享/可声明/配置驱动→ResourceManager，算子私有→Closer）、"Python"双义陷阱、llmdoc baseline 先 fetch 教训、clang-format commit 阶段无 gate、ruleset 无 required_status_checks 核查路径、pre-push 自包装 hook 行为。
- `llmdoc/memory/reflections/resource-metrics-fanout-stats.md` — 资源级指标 fan-out 与 `/stats.resources` 复盘，记录 fan-out（Tee）路由相对 Collector-only/现状的三选一决策、resources 恒存在键作为跨运行时强断言、探针 probe-once-then-tick 便于测试，以及"scope 靠路由而非过滤"两条教训。
- `llmdoc/memory/reflections/bench-lock-optimization-campaign.md` — Bench 归因与 Frame 锁优化战役复盘（9 commits），记录 merge_dedup O(N²)→O(N) 修复、op-attribution 归因脚本、fixture 代表性错误（large_5000 误导 vs calibrated 真实场景）、zombie 进程污染整天 bench 数据、microbench 访问模式失真、二进制布局噪声、SharedMutex v1 失败三根因与 v2（Go 协议）备件化、Frame 维持 per-call 锁形态与 Go/Java 对齐的最终决策。
- `llmdoc/memory/reflections/review-driven-build-input-error-ordering.md` — 评审驱动的 build_operator_input 报错顺序回归修复复盘，记录锁优化 `eab4415` 把 `validate_strict_items` 移出锁窗口致 strict_item 被提到 common 之前、翻转跨运行时首错优先级的静默回归，沉淀"校验顺序/首错优先级属字节级对等契约""error fixture 需覆盖多违反优先级""perf 改动触及校验路径需复核 error-parity"三条教训。

## memory/decisions/

- `llmdoc/memory/decisions/perf-evolution-roadmap.md` — 引擎侧性能演进路线决策：两个校准事实（per-item VM 边界主导、VM 层加速被端到端稀释）、三步路线（typed-ColumnFrame/arena → common-mode 列内核负载迁移 → 条件触发的 VM 适配层可插拔）、明确不做项（VM 直摸 Go heap、简单脚本负载上的 VM 优化），与外部 Go-native Lua VM 项目松耦合。
