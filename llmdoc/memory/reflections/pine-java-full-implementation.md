# Pine-Java 完整实现旅程复盘

## Task
- 从零实现 Pine-Java 引擎至与 Go 完全功能对等，再推进到 Schema 独立架构。
- 历经 8 个阶段、24 commits、98 files、+9147 lines：JSON fixture 框架、Engine 核心、data_parallel、Resource Management、HTTP Server、Codegen、CI 集成、Schema Independence。
- 最终将目录从 `java-pine/` 重命名为 `pine-java/`，术语对齐。

## Expected vs Actual
- Expected: 完成后 Pine-Java 具备 Go 引擎全部运行时语义，两侧可独立演进 Schema，CI 三层交叉验证确保一致性。
- Actual: 目标全部达成。18 算子完整实现，70+ 测试通过，零编译/测试回归。三层交叉验证（Schema JSON diff、Config 共享 fixture、Execution 结果对比）已在 CI 中运行。

## What Went Wrong
1. **Go parity 审计发现多处语义差异** — 首轮实现后仍需第二轮审计修复：cancelLatch 错误传播、FNV-64a 哈希 unsigned 除法、formatValue 对 `1.0` → `"1"` 的对齐、SSRF DNS 重解析、Stats.recordError CAS 语义、OperatorType.validateOutput 执行后校验。这表明仅从 API 文档实现不足以保证语义对等，需要行为级对齐测试。
2. **conventions.md 中 "Go Schema 是唯一事实源" 的假设在 Schema Independence 阶段被打破** — 实现进行到最后才发现文档约束与新架构矛盾，需要反向更新多个稳定文档。
3. **目录命名 java-pine 不符合项目整体命名模式** — 直到接近尾声才统一为 pine-java，引发全量路径引用修改。应在项目初始化时即确定命名。

## Root Cause
1. **语义对等不能靠"读 Go 代码翻译到 Java"达成** — Go 的隐式行为（context cancel 传播、fmt.Sprintf 格式化规则、FNV hash 的 unsigned 算术）在 Java 中没有直接对应物，必须通过共享 fixture 的行为测试才能暴露差异。
2. **文档中的"唯一事实源"声明是架构约束** — 当设计决策改变了架构方向（从单一事实源到双独立源），稳定文档必须同步更新，否则后续开发者会基于过时约束做决策。
3. **命名决策延迟的成本随代码量线性增长** — 在 9000+ 行代码之后重命名目录比在第一个 commit 时决定要昂贵得多。

## Missing Docs or Signals
- `conventions.md` 的 "Go Schema 是算子的唯一事实源" 章节现在过时 — 两侧均为独立 Schema 源，通过 CI JSON diff 保持一致。
- `operator-contract.md` 的 "Go Schema 仍是唯一事实源；Java 侧实现等效语义，不引入新的 Schema 定义" 声明过时。
- 无文档描述三层交叉验证架构（Schema diff / Config fixture / Execution comparison）。
- 无文档描述 Pine-Java Codegen 双模式（`--export-schema` / `--schema-from-registry`）。
- `dag-engine.md` 的 Pine-Java 描述需要更新以反映 Schema 独立性和 Registry 重写。
- 缺少关于 Java 中 Go 语义对等的已知陷阱列表（cancelLatch vs context、unsigned FNV、formatValue 等）。

## Promotion Candidates

### 应提升到 `must/conventions.md`
- **替换 "Go Schema 是唯一事实源"** → "Go 和 Java 均为独立 Schema 源，通过 CI 三层交叉验证保持一致"
- **三层交叉验证模型** — Schema（JSON diff）、Config（共享 fixture）、Execution（结果对比）作为双运行时一致性的标准保证机制

### 应提升到 `reference/operator-contract.md`
- Pine-Java Registry 重写：schema-based registration、validateAndExtractParams、exportSchemaJSON
- Codegen 双模式：`--export-schema`（从 Registry 导出 JSON）、`--schema-from-registry`（从 JSON 生成 Python DSL）

### 应提升到 `architecture/dag-engine.md`
- Pine-Java Schema Independence 描述：`ParamSpec.java` + `OperatorSchema.java` + `Registry.java` 完整链路
- cancelLatch 模式作为 Go context.Context cancel 的 Java 等效实现

### 仅保留在 memory
- 目录命名教训（应在首个 commit 定好）— 流程经验
- 第二轮审计中的具体 Go↔Java 语义对齐细节（FNV unsigned、formatValue、SSRF DNS re-resolution）— 实现细节
- LinkedHashMap 用于稳定 JSON 输出 — 实现选择
- 设计文档先行（docs/pine-java-schema-independence.md）有效保持实现聚焦 — 流程经验

## Key Design Decisions (Record)
1. **cancelLatch 替代 context.Context** — Java 无 Go context cancel 机制；使用 CountDownLatch + 5ms polling awaitWithCancel helper 实现等效语义。
2. **Schema 独立架构** — 放弃单一事实源模型，两侧独立维护 Schema，CI 通过 JSON export diff 保持对齐。
3. **LinkedHashMap 保持注册顺序** — Registry 使用 LinkedHashMap 保证 exportSchemaJSON 输出稳定、可 diff。
4. **validateAndExtractParams 跳过空 schema** — 允许 @Deprecated 注册路径继续工作，不强制全部迁移。
5. **Codegen 内部模型与 Registry 模型分离** — 内部类带 Jackson 注解适配 Go 的 capitalized JSON keys，bridge method `fromRegistry()` 做转换。

## Follow-up
- 更新 `conventions.md`：替换 "唯一事实源" 为 "双独立源 + CI 三层验证"。
- 更新 `operator-contract.md`：移除 "Java 侧不引入新 Schema 定义" 声明，补充 Registry 重写和 Codegen 双模式。
- 更新 `dag-engine.md`：Pine-Java 章节补充 Schema Independence 和 cancelLatch 设计。
- 考虑新增 `reference/cross-validation.md` 专门描述三层验证架构和 CI 集成细节。
- 考虑新增 `reference/go-java-parity-pitfalls.md` 记录已知语义对齐陷阱，供后续新增算子时参考。
