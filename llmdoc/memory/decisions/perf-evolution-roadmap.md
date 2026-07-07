# 性能演进路线（引擎侧）

记录 2026-06 性能战役收口后确定的 pineapple 引擎侧（三运行时）性能演进方向。每一步独立交付价值；与外部 Go-native Lua VM 项目（纯 Go 解释器 → Wasm 编译层 → method JIT → trace JIT 的分层路线，完整路线图见 `.code-review/go-native-lua-vm/roadmap.md`，gitignored 工作区文档，不在 llmdoc 管辖内）保持松耦合——**该项目成立与否，本路线均成立**。

## 决定路线的两个校准事实（2026-06）

背景：Frame 锁形态三运行时已对齐 per-call（见 `llmdoc/memory/reflections/bench-lock-optimization-campaign.md`），pine-java 已启用 LuaJC 默认后端（commit `dd0e260`）。期间的测量产生两个校准事实：

1. **per-item 边界主导**：隔离算子级 L5（Horner 循环）/1000 items 实测——gopher-lua 729μs、LuaJ-luajc 164μs、LuaJIT 154μs。真 LuaJIT 只比 luajc 快 6%：per-item 跨 VM 边界 + 装箱成本钉死了 VM 层加速的上限。
2. **端到端稀释**：luajc 隔离算子级 -37%，但端到端跨引擎 benchmark（2026-06-11，report-20260611-101131 vs report-20260610-230235，全 14 fixtures，10000 请求 × 16 并发）全部落在 ±5-7% 噪声带内。现有 fixtures 的 Lua 全是单行 if/return 判断，边界成本主导，VM 层加速在端到端不可见。
   - **第二证据点（2026-06-13，wangshu CallInto）**：新增的 `realistic_*_calibrated_itemlua` 变体把 boundary 调用密度推到极致——per-item lua 加权打分、3000 调用/请求，是设计上 boundary-dominated 的形状。即便如此，wangshu(CallInto) vs gopher 端到端仍统计持平（p=0.21~0.84）。逐 op 归因量化：3000 次 per-item Lua 调用只给 ~30ms 请求加 <1ms（<3%），38-op DAG + stub I/O + 3000-item DataFrame 主导 ~97% 成本。即便边界是"主导"形状，依然不足以让 VM 层差异在端到端可见。这进一步确认：**common-mode 列内核负载迁移（第二步）才是 VM 层加速可见性的真正闸门**，单纯增大 boundary 密度不行。

推论：在负载形状（per-item 边界）和数据布局（interface 装箱）改变之前，继续投入 VM 层优化没有端到端回报。

## 第一步：typed-ColumnFrame / arena（最高优先，独立收益）

- 现状：pine-go ColumnFrame（`pine-go/internal/dataframe/column_frame.go` `ColumnFrame`）列是 `map[string][]any`——列的"壳"对了，但每元素仍是 interface 装箱指向 Go 堆散落对象；pine-java / pine-cpp 的 ColumnFrame 同构。
- 演进：类型化扁平列（`[]float64` / `[]int64` / `[]bool` + 字符串 arena + presence bitmap）。
- 独立收益：原生算子去装箱 + cache 友好，不依赖任何 VM 野心。
- 配合收益：扁平列可零拷贝映射进未来 VM 的 linear memory / arena；若外部 VM 项目成立，arena 内存布局 ABI 需与其协同设计。
- 适用判据沿用列存复盘的场景分析（`llmdoc/memory/reflections/column-store-dataframe.md`：大量 item、少结构变更的负载占优；removals / reorder 天然劣势）。
- **接口税数据点（2026-07-07，列存 vs 行存持平调查）**：逐元素 `OperatorInput.Item(i, field)`（RLock + map 查列 + 边界检查）是列存优势不可见的**第一性原因**——ColumnFrame 逐元素访问 7,210ns vs 理想列扫描 262ns（27x），而 typed `[]float64` 扫描（257ns）与 `[]any` 理想列扫描几乎无差。推论：**typed columns 必须配合批量列访问 API（一次锁 + 一次 lookup 拿整列）才能兑现，单独做 typed columns 仍被逐元素接口税吃掉**。批量列访问 API 已于 2026-07-07 在三引擎实现并提交（`ItemColumn`/`itemColumn`/`item_column`，分支 feat/column-store-batch-access，契约见 `llmdoc/reference/operator-contract.md`）——typed columns 的前置接口已就位；ColumnFrame 三处行主序热路径同批改为列主序，transform-heavy e2e 列存快 ~30%。详见 `llmdoc/memory/reflections/column-vs-row-parity-investigation.md`。

## 第二步：算子 API 负载形状迁移——common-mode 列内核

- 现状：transform_by_lua 的 item-mode 每 item 跨一次 VM 边界（per-item globals 装箱 + invoke），这是校准事实 1 的根源。
- 演进：鼓励 / 扩展 common-mode——循环写在 Lua 内，一次 Execute 跨一次界，整列在 VM 内迭代。这是任何 VM 层加速（LuaJC 已落地，未来 VM 同理）在端到端可见的**前提闸门**。
- 注意：common-mode 三运行时已存在（如 pine-java `TransformByLua.executeForCommon`），缺的是列内核风格的 fixture / 生产负载与配套文档引导。
- **边界层量化数据点（2026-06-13，wangshu Arena 列轨 ABI）**：本步此前只有"列内核负载迁移才是 VM 加速可见性闸门"的定性论断（design_doc/13:91 自陈）。wangshu v0.1.4 公共 API 审计提供了首个边界层实证——commonMode 现走 `SetGlobal([]any) → makeArrayTable`（`NewTable` + N 次 `SetIndex` 逐元素装箱）；对照原型把整列改走 `Program.Call(state, arena)` 零拷贝列轨（宿主 `[]float64` 不复制、脚本读 `arena.<col>[i]`），Boundary 微基准量化：N=100 边界 **-22%**、N=3000 边界 **-46%**，B/op **-83%~-87%**，提速随列长增长（消除 N 次 SetIndex 装箱 + table rehash）。这坐实"列内核迁移确有 VM 边界回报"。但三条克制限定，**不构成"立即做"指令**：
  - (a) 这是 **Boundary 微基准**，不是 calibrated 端到端裁判；端到端会被引擎框架稀释（再次印证校准事实 2 的逻辑——38-op DAG / stub I/O / 3000-item DataFrame 主导成本），-46% 边界提速落到端到端大概率只剩个位数；
  - (b) 落地需把 Lua 脚本访问约定从 `field[i]` 改成 `arena.field[i]`，而 `lua_script` 是四引擎共享的**字节级对等产物**——只改 wangshu 破 parity，真落地需四引擎都支持 arena ABI，是**跨引擎工程**（先在 `apple/` 层统一脚本改写，再四引擎同步 + cross-validate 字节对等），不是 pine-go 本地优化；
  - (c) 触发条件：**仅当 profiling 证明 commonMode 边界是生产端到端热点时才立项**。在此之前作为"第二步是 VM 加速可见性真正闸门"论断的实证补强留痕。
  - 完整调查方法、绝对 ns 数据、SetIndex 顺序 append O(N²) 建表意外发现（已提 wangshu #10）见 `llmdoc/memory/reflections/wangshu-borrow-optimization-survey.md`；arena ABI 契约与限制见 `llmdoc/reference/lua-backend.md`。

## 第三步（条件触发，2026-06-13 已触发）：VM 适配层可插拔

- **触发记录**：wangshu（纯 Go Lua 5.1 VM，NaN-boxing + arena GC）v0.1.3 接入为 opt-in（初始 build tag 为 `lua_wangshu`，翻默认时连同极性反转改名为 `lua_gopher`）；v0.1.4 上游加入 `CallInto(dst, fn, args...)` 零分配边界路径（issue #8 反馈闭环）后翻转默认。当前 build tag 极性：**默认 `!lua_gopher` = wangshu，opt-in `lua_gopher` = gopher-lua**。共享 `Backend/Pool/Engine` 抽象（`pine-go/operators/lua/backend.go`）+ 同一测试套钉两后端字节级对等。详见 `llmdoc/memory/reflections/wangshu-backend-callinto-and-default-flip.md` 与 `llmdoc/reference/lua-backend.md`。
- **决策门槛（实证细化）**：原"显著胜出才翻"被本次任务实证细化为三条 AND 闸门：
  - (a) 在 calibrated 裁判 fixture（端到端代理生产）**不劣化**——统计持平即可，因端到端会稀释 VM 层差异；
  - (b) 在受影响场景（boundary-dominated 隔离 item-mode）**显著胜出**——证明优化在源头维度真实存在；
  - (c) 全量 race + lint + 18 包测试套件**双 tag 全绿**——证明行为对等。
  本次 wangshu 翻默认即沿此模式：calibrated 三变体持平（p=0.21~0.84）+ isolated item-mode 时间 -12.5%/分配 -21.5%（L5 时间 -27%/分配 -35%）+ 双 tag 测试全绿。
- **语义闸门（按切换范围分档）**：
  - **小范围切换**（共享同一适配层 + 同一算子 + 字节级输出对比）：用共享测试套 + calibrated fixture 字节一致即可。本次 wangshu→默认即此档——两后端跑同一 `operators/lua` 测试套（双 tag race 全绿），itemlua calibrated fixture 跨后端字节一致（`sample=1173.7`）。
  - **大范围切换**（如完全替换 VM 引擎、跨架构改动）：仍需 cross-validate + diff-fuzz byte-equal，沿用 pine-java luajc 的验证模式（`pine-java/src/test/java/page/liam/pine/operators/TransformByLuaCompilerBackendTest.java` 风格的后端等价钉住 + 全量 CI）。

## 明确不做

- 让 Lua / VM 直接摸 Go heap：pin / 写屏障 / 内部布局漂移税，被"数据搬家进共享 arena"以小得多的风险替代。
- 在现有简单脚本负载上继续投入 VM 层优化：端到端 ±0% 已证明不可见（校准事实 2）。

## 决策纪律

- calibrated fixtures 是性能决策唯一裁判；benchmark 卫生纪律见 `llmdoc/guides/benchmark-hygiene.md`。
- 锁形态维持三运行时对齐（per-call），不再单边优化，见 `llmdoc/memory/reflections/bench-lock-optimization-campaign.md`。
