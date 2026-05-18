# [Pine-Java Audit Parity -- Round 5 (Final)]

## Task
- 第五轮独立 Go-parity 审计，发现 34 项差异（7 HIGH, 19 MEDIUM, 8 LOW）。
- 最终决策：20 fix Java, 3 fix Go（记录于 `.llmdoc-tmp/go-server-pending-fixes.md`），9 accepted design differences，5 platform limitations。
- 20 项 Java 修复在 commit `11c00cd` 中实现，覆盖 operators/server/codegen/utilities 四个维度。
- 4 个并行 agent 分组执行：fix-quick、fix-goformat、fix-server-lua、fix-codegen，作用于不重叠的文件集。

## Expected vs Actual
- Expected: 第四轮后仅剩少量边缘差异，快速收敛。
- Actual: 独立重新审计发现 34 项，其中 3 项在第四轮已标记"verified"但实际不完整（Lua pool 清理、TransformByLua MetricsAware、filter_condition 精度）。修复范围显著大于预期。

## What Went Wrong
1. **第四轮"verified"条目存在回归** -- Lua pool usedKeys 策略无法处理基线值被覆盖的场景（如 `math = 1` 覆盖原始 math 库）；TransformByLua 未实现 MetricsAware；filter_condition 的 `%g` 6-digit 精度丢失与 Go 不一致。独立重审才暴露这些遗漏。
2. **格式化逻辑散落在多个算子** -- redis_get、resource_lookup、filter_condition、reorder_shuffle 各自内联了 Go 风格的数值→字符串转换逻辑，不一致且重复。
3. **Metadata Contract 文档生成在 Java 侧完全缺失** -- Go 有 `pkg/codegen/docparse.go` 自动提取算子文档，Java 侧无等价物，需新建 Javadoc annotation 体系。

## Root Cause
1. **"Code review verified" != "independent re-audit verified"** -- 第四轮验证在相同 context 中进行（同一个 agent/对话），确认偏误导致遗漏。独立审计（无历史 context）更能发现真实差异。
2. **Lua pool usedKeys 方案存在设计缺陷** -- 只追踪新增 key 并 nil 化，无法感知已有 key 的值被改变。正确方案是借出时快照全部基线 key→value，归还时恢复。
3. **跨算子格式化逻辑缺乏共享抽象** -- 未识别出"Go fmt 兼容格式化"是一个跨 operator 的共享需求，直到第五轮才收敛为 GoFormat 工具类。
4. **文档生成被视为"nice to have"** -- Metadata Contract 在 Go 侧已存在但 Java 侧长期忽略，因为不影响运行时语义。

## Missing Docs or Signals
- **GoFormat 工具类** -- 新文件 `GoFormat.java`，实现 `Sprint`/`FormatFloat`/`Sprintf("%g")`，无 llmdoc 覆盖。
- **Lua pool 策略演进** -- 从 Round 4 usedKeys 到 Round 5 baseline snapshot，架构文档未更新。
- **Metadata Contract Javadoc 注解约定** -- 18 个算子 + Codegen parser 使用 `@MetadataField` 等注解，无文档说明。
- **Go-side pending fixes** -- 3 项待 Go 侧修复已登记于 `.llmdoc-tmp/go-server-pending-fixes.md`，但无 llmdoc 追踪。
- **PineServer maxRequestBodyBytes int→long 溢出风险** -- 隐含的"大于 2GB 配置静默截断"缺陷，无安全约束文档。

## Promotion Candidates

### 应提升到 `reference/operator-contract.md`
- **GoFormat 工具类** -- 跨算子 Go 兼容格式化的标准方法，所有需要数值→字符串转换的 Java 算子必须使用 GoFormat 而非 String.valueOf/String.format。
- **Metadata Contract Javadoc 注解** -- Java 算子通过 Javadoc annotation 声明 input/output metadata，Codegen 自动提取生成 `__init__.py` docstring。

### 应提升到 `architecture/dag-engine.md` Pine-Java 章节
- **Lua pool baseline snapshot 策略** -- 取代 Round 4 的 usedKeys 方案。借出时 snapshot 所有 baseline key→value，归还时逐一恢复。正确处理值覆盖和新 key 添加两类情况。
- **OperatorOutput.setWarning first-wins 语义** -- 多次 setWarning 仅保留首个，与 Go 一致。

### 应提升到 `must/conventions.md`
- **独立重审原则** -- 对等审计的验证不能在同一 context 中完成；最终轮必须由独立审计（无历史 context）执行。

### 暂留 memory
- PineServer spurious reload 修复细节（lastModified 在 initial load 后设置）
- lastReloadDurationNs only-on-success 条件
- Codegen `__init__.py` header comment 对齐、string escape 补充（\0, \b, \f）
- ReorderShuffle Long.compareUnsigned 具体实现
- HTTP metrics statusBucket 标签映射逻辑
- 第三项 Go-side pending fix 的具体内容

## Follow-up
- 更新 `architecture/dag-engine.md` Pine-Java 小节：Lua pool baseline snapshot 策略、OperatorOutput first-wins。
- 更新 `reference/operator-contract.md`：GoFormat 使用约束、Metadata Contract 注解体系。
- 更新 `must/conventions.md`：增加独立重审原则（critical parity 验证须独立 context）。
- 追踪 `.llmdoc-tmp/go-server-pending-fixes.md` 中 3 项 Go-side fixes 的完成状态。
