# 性能演进路线（引擎侧）

记录 2026-06 性能战役收口后确定的 pineapple 引擎侧（三运行时）性能演进方向。每一步独立交付价值；与外部 Go-native Lua VM 项目（纯 Go 解释器 → Wasm 编译层 → method JIT → trace JIT 的分层路线，完整路线图见 `.code-review/go-native-lua-vm/roadmap.md`，gitignored 工作区文档，不在 llmdoc 管辖内）保持松耦合——**该项目成立与否，本路线均成立**。

## 决定路线的两个校准事实（2026-06）

背景：Frame 锁形态三运行时已对齐 per-call（见 `llmdoc/memory/reflections/bench-lock-optimization-campaign.md`），pine-java 已启用 LuaJC 默认后端（commit `dd0e260`）。期间的测量产生两个校准事实：

1. **per-item 边界主导**：隔离算子级 L5（Horner 循环）/1000 items 实测——gopher-lua 729μs、LuaJ-luajc 164μs、LuaJIT 154μs。真 LuaJIT 只比 luajc 快 6%：per-item 跨 VM 边界 + 装箱成本钉死了 VM 层加速的上限。
2. **端到端稀释**：luajc 隔离算子级 -37%，但端到端跨引擎 benchmark（2026-06-11，report-20260611-101131 vs report-20260610-230235，全 14 fixtures，10000 请求 × 16 并发）全部落在 ±5-7% 噪声带内。现有 fixtures 的 Lua 全是单行 if/return 判断，边界成本主导，VM 层加速在端到端不可见。

推论：在负载形状（per-item 边界）和数据布局（interface 装箱）改变之前，继续投入 VM 层优化没有端到端回报。

## 第一步：typed-ColumnFrame / arena（最高优先，独立收益）

- 现状：pine-go ColumnFrame（`pine-go/internal/dataframe/column_frame.go` `ColumnFrame`）列是 `map[string][]any`——列的"壳"对了，但每元素仍是 interface 装箱指向 Go 堆散落对象；pine-java / pine-cpp 的 ColumnFrame 同构。
- 演进：类型化扁平列（`[]float64` / `[]int64` / `[]bool` + 字符串 arena + presence bitmap）。
- 独立收益：原生算子去装箱 + cache 友好，不依赖任何 VM 野心。
- 配合收益：扁平列可零拷贝映射进未来 VM 的 linear memory / arena；若外部 VM 项目成立，arena 内存布局 ABI 需与其协同设计。
- 适用判据沿用列存复盘的场景分析（`llmdoc/memory/reflections/column-store-dataframe.md`：大量 item、少结构变更的负载占优；removals / reorder 天然劣势）。

## 第二步：算子 API 负载形状迁移——common-mode 列内核

- 现状：transform_by_lua 的 item-mode 每 item 跨一次 VM 边界（per-item globals 装箱 + invoke），这是校准事实 1 的根源。
- 演进：鼓励 / 扩展 common-mode——循环写在 Lua 内，一次 Execute 跨一次界，整列在 VM 内迭代。这是任何 VM 层加速（LuaJC 已落地，未来 VM 同理）在端到端可见的**前提闸门**。
- 注意：common-mode 三运行时已存在（如 pine-java `TransformByLua.executeForCommon`），缺的是列内核风格的 fixture / 生产负载与配套文档引导。

## 第三步（条件触发）：VM 适配层可插拔

- 触发条件：仅当外部 Go-native Lua VM 项目产出可用解释器后启动——pine-go 的 Lua 适配层支持替换 gopher-lua。
- 语义闸门：替换必须通过 cross-validate + diff-fuzz byte-equal，沿用 pine-java luajc 的验证模式（`pine-java/src/test/java/page/liam/pine/operators/TransformByLuaCompilerBackendTest.java` 风格的后端等价钉住 + 全量 CI）。

## 明确不做

- 让 Lua / VM 直接摸 Go heap：pin / 写屏障 / 内部布局漂移税，被"数据搬家进共享 arena"以小得多的风险替代。
- 在现有简单脚本负载上继续投入 VM 层优化：端到端 ±0% 已证明不可见（校准事实 2）。

## 决策纪律

- calibrated fixtures 是性能决策唯一裁判；benchmark 卫生纪律见 `llmdoc/guides/benchmark-hygiene.md`。
- 锁形态维持三运行时对齐（per-call），不再单边优化，见 `llmdoc/memory/reflections/bench-lock-optimization-campaign.md`。
