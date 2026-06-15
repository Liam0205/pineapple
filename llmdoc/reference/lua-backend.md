# Lua 后端参考（pine-go）

本文档记录 pine-go `transform_by_lua` 算子的 Lua VM 后端选择契约、共享抽象、wangshu 双向 pin 所有权边界 API、内存模型与 GC 触发点、pool 复用模型与后端对比 benchmark 入口。仅适用于 pine-go——pine-java 默认 LuaJC、pine-cpp 用 LuaJIT，不暴露 build-tag 切换面。

## 后端选择契约

- 默认：**wangshu**（纯 Go Lua 5.1 VM，NaN-boxing + arena GC，下限版本 v0.1.4），build tag 表达为 `!lua_gopher`
- Opt-in：`-tags=lua_gopher` → gopher-lua

编译期单一后端零运行时分发，binary 只链一个 VM。Build tag 极性是排他选择，不存在双后端共存运行时。Tag 文件在 `pine-go/operators/lua/` 下成对出现（`*_wangshu.go` `//go:build !lua_gopher` / `*_gopher.go` `//go:build lua_gopher`），Makefile 与 `scripts/bench-lua-backends.sh` 同步切换。

## Backend / Pool / Engine 三层抽象

抽象定义在 `pine-go/operators/lua/backend.go`，两后端实现各自的 `Backend` / `Pool` / `Engine`：

- `Backend`：构造 Pool 与解析后端能力
- `Pool`：5 元组计数器 + 双层 warm/sync.Pool 复用模型（见下节）
- `Engine`：单次脚本执行单元，封装 `LoadString` / `SetGlobal` / `Call` / 读返回值等边界操作

两后端共享同一 backend-agnostic 测试套（`lua_test.go` / `backend_isolation_test.go` / `sandbox_test.go` 在两个 tag 下都跑 race），加 backend-specific 的 pool 计数器测试（`pool_gopher_lua_test.go` `//go:build lua_gopher` / `pool_wangshu_test.go` `//go:build !lua_gopher`）钉住 borrow/return/create/reuse/active 5 元组对等。新加后端必须复刻 backend-specific 的计数器测试套,不可仅复刻实现。

## wangshu 边界 API 契约（双向 pin 所有权）

wangshu 用 pin 表（pin table，GC root）管理 host 侧持有的 table/function 句柄。跨边界传 Value 时，caller 必须自己管理 pin 槽的归还——边界 API 只搬 GCRef，**不接管** pin 所有权。返回值（dst）方向与入参（host→VM）方向都受此约束，且对称：谁用 `st.NewTable()` 造出占 pin 槽的复合值，谁就负责 `Release()`，否则 pin 槽只增不减。

### 返回值方向（CallInto / dst）

wangshu v0.1.4 引入 `CallInto(dst []Value, fn Value, args ...Value) error` 零分配边界路径：

- 调用方拥有 dst：必须自行预分配并传入
- **dst 底层复用 wangshu 的内部栈，下次进 VM 前必须消费完**
- LuaOp 的消费模式：`CallInto` 返回后立即 `fromValue` 转出 + `Frame.SetItem` 写回 DataFrame，不持有 dst 跨调用
- 类型转换语义：string 走 arena 拷贝（独立可逃逸），table/function 仍是 pin 句柄需 `Release()`
- `tableToGo` 用 `ForEach` 遍历返回 table 时，**key 与 val 都占 pin 槽**，回调内两者都要 `Release()`——只 Release val 会让 key 方向 pin 槽随返回值累积（`cb58e08`）

issue #8 反馈闭环：边界双拷贝（state.go:557 + wangshu.go:371，每调用 72B/2 allocs）→ CallInto 零分配，wangshu v0.1.4 锁定为 pine-go 下限。

### 入参方向（SetGlobal / GetGlobal / Call 灌入复合值）

`SetGlobal` / `GetGlobal` / `Call` 只拷贝 GCRef，**不接管** pin 所有权，caller 必须自己 Release：

- `st.NewTable()` 返回的 Value 立即占一个 pin 槽（pin 表是 GC root，wangshu 文档明确"返回值需 Release 否则 pin 槽累积"）
- 具体到 LuaOp：`SetGlobal` 灌入复合值（`[]any` / `map`）时，`makeArrayTable` / `makeMapTable` 用 `NewTable()` 造表，**根表与每个嵌套子值各占一个 pin 槽**
- 子值必须在 `SetIndex` / `Set` 之后**立即** Release（含错误返回路径），根表在交给 `st.SetGlobal` 之后 Release
- Release 对标量（bool / number / string）是 no-op，因此可无条件调用，无需先判类型（`477dacd`）

**违反后果**：common-mode `transform_by_lua` 每请求每 ItemInput 字段都会泄漏一组 pin 槽 + arena 表，随 QPS 线性增长。`ResetGlobalsToBaseline`（见下节）只复位 globals 表、**不清 pin 表**，所以这些槽不会被借后重置回收——计数器五元组不变量也钉不住此类泄漏（globals/pin 是两套独立账本）。`TestWangshuSetGlobalCompositeNoPinLeak` 钉死入参方向无泄漏（`c77c0af`）。

## Pool 复用模型（两后端等价语义）

两后端 Pool 共享同一抽象语义，仅实现细节（baseline 重置 API）不同：

### 5 元组计数器

`borrow_count` / `return_count` / `create_count` / `reuse_count` / `active_count`，通过 `MetricsAware` 注入 Provider 暴露。

**核心不变量**：`borrow_count == reuse_count + (create_count - 1)`

减 1 因 pre-warm 创建的实例不计入借出（pre-warm 是非借出 create）。`pool_*_test.go` 钉住此不变量。

### 双层 warm / sync.Pool 复用

- **strong-ref warm tier**（`minIdle=100`）：minIdle 槽位的实例被强引用持有，永不被 GC 回收
- **sync.Pool overflow tier**：超过 minIdle 的归还实例进入 sync.Pool，可被 GC 回收

内存上界 = `minIdle + 当前 in-flight 实例数`，与 GC 周期 / 进程 uptime 无关。

### Baseline 重置契约（借后必须）

借后必须执行 baseline 重置，把脚本运行期写入的全局变量复位回 pre-warm 时的快照——script-level 全局漏到下次借用属契约违反：

- gopher-lua：`snapshotGlobals` / `resetToBaseline`（Pool 内部封装）
- wangshu：`MarkGlobalsBaseline` / `ResetGlobalsToBaseline`（v0.1.3+ 提供）

**closed-branch 例外**：pool 已 closed 时，`Return` 跳过 `ResetGlobalsToBaseline` + `RemoveContext`——state 即将被丢弃，reset 无意义。此分支镜像 gopher-lua 的 closed 行为，避免关停期无谓开销（`ac64491`）。

## wangshu 内存模型与 GC 触发点

嵌入者关键事实：wangshu 的 GC safepoint **只在 opcode 执行路径触发**（RET / CALL / NEWTABLE / CLOSURE 等字节码），**宿主侧 API（`NewTable` / `SetGlobal` / `Release`）不触发 GC**。

后果——即使入参方向已按上节正确 Release：

- `Release()` 只是把 pin 槽归还 `freePins`（计入空闲），并不立刻 sweep 对应的 arena 字节
- arena 字节要等**下一次脚本执行命中 safepoint** 时才被回收
- 因此稳态达成前会有一段"延迟归还"的合理内存爬坡：pin 槽已释放但 arena 未 sweep。这是正常曲线形状，**不应与真泄漏混淆**（判据详见下文「三态内存判据」）

内存上界仍由 Pool 复用模型决定（`minIdle + 当前 in-flight`，与 GC 周期 / uptime 无关）；本节只补充"何时真正释放字节"的时间维度，不改变上界结论。

### auto-GC pacing 缺口与 cadence-sweep workaround（#100 / wangshu#9）

由"safepoint 仅 opcode 路径"派生出一个 pacing 缺口：wangshu 的 auto-GC 只在 VM opcode safepoint 检查 trigger 阈值。**boundary-dominated 负载**（common-mode `transform_by_lua` 用 `SetGlobal` 灌大 composite + 极短脚本）每次执行的 opcode 极少、safepoint 稀少，于是 GC 的内部 accounting（`bytesAllocSince`）在推进、trigger 却几乎不触发——auto-GC 被**饿死**，arena 线性爬升。

下游止血（v0.10.2 已 ship，对应上游 wangshu#9）：`pool_wangshu.go` 的 `wangshuPool.Return` 每 `gcCadenceWangshu`（= 256，常量出处见 `pool_wangshu.go`）次返还，在该 goroutine 仍独占 state 时（交回 warm/pool 之前，遵守 wangshu 单 goroutine 单 state 契约）跑一次独立的 `collectgarbage("collect")` 脚本（`collectProg`，编译名 `pine_gc_cadence`）主动推进一次 sweep。该 collect chunk 独立于用户程序，不触碰用户脚本 globals / 不复位 baseline；单次开销微秒级，分摊到数千次返还可忽略。`collectProg` 编译失败时静默跳过（pool 仍可用，仅无此 workaround）。计数用独立的 `gcReturnCount`（与公共 `/stats` 的 `return_count` 隔离，便于拆除 workaround 不扰动 stats）。代码标注 `REMOVE once wangshu#9 lands`。

### grow-only high-water latch（#105 / wangshu#11）

cadence-sweep 让 GC accounting 平了，但生产 RSS 仍可能单调爬升——根因不在 pacing，而在 arena 的 **grow-only** 形态：

- arena backing slab 只增不减：`grow64` 只翻倍 copy，整个 `Arena` 方法表**无任何 shrink / release backing 路径**
- `sweep` / `freeObject` 只把死对象还内部 freelist（`Arena.Free` 累加 `freeBytes`），底层 `[]uint64` backing slab **永不交还 Go runtime**
- 因此一个曾被偶发大分配 balloon 过的 state 会**永久变肥**；它被 warm / sync.Pool tier 缓存后，high-water RSS 被 **latch 住**，前述 cadence sweep 也压不下去（它只回 freelist，不动 backing slab）

并发现：`Options.InitialArenaBytes` / `MaxArenaBytes` 是 **dead param**——全模块零读取，`NewState` → `crescent.New()` 无参传 `arena.New(arena.Options{})` 全零值，host **无生效的 arena cap 旋钮**。对应上游 wangshu#11（请求 arena backing release + 激活 cap + 暴露 high-water/Cap 观测 API）。下游止血见「drop-fat-state 止血契约」节。

### 三态内存判据

判断一条内存曲线是否异常，须区分三态（不可一概当真泄漏，也不可一概当可自愈）：

- **(a) pin 槽随请求单调增** = 入参方向 pin 泄漏。**真 bug**，已在 v0.10.1（`477dacd`）修；判据是 pin 槽计数本身单调增长（globals/pin 是独立账本，5 元组计数器钉不住，须直接观测）
- **(b) arena 字节滞后** = pin 槽已释放但 arena 未 sweep。**可自愈**：等下一次脚本执行命中 safepoint 即回落，是正常爬坡曲线
- **(c) backing slab latch** = fat state 被池缓存、high-water 被 latch。**不自愈**：grow-only 决定 sweep 只回 freelist、backing 不交还 runtime，cadence sweep 也压不下；唯有 **drop 掉该 state** 才能让 Go runtime 回收 backing slab（#105 / wangshu#11）

> 历史纠偏：本系列前篇 `wangshu-pin-table-input-leak-fix.md` 仅区分 (a)/(b) 两态，在 grow-only latch 场景下不充分——(c) 既非 pin 泄漏、也非可自愈的字节滞后，是第三种独立形态。

### `GCCountKB` 语义陷阱

`State.GCCountKB()` 的**实测语义是 `bump − freeBytes`**，即**活跃量**（sweep 后随 `freeBytes` 上升而回落），**不是 capacity 高水位**：

- 门面注释（`wangshu.go`）写"含 freelist 空闲块"、core 注释写"活跃 KB"，两处**口径分叉**，必须读实现才能确认真实语义
- 它会被 sweep / cadence sweep **拉回**，因此拿它做 drop 判据**必须在 reset/sweep 之前采样**，否则读到被拉回的小值、永远 drop 不掉 fat state（见下节顺序约束）
- wangshu 公共 `State` 面**无 `Cap` / high-water 访问器**（这正是 wangshu#11 direction 3 的诉求），`GCCountKB` 的"sweep 前峰值"是目前唯一可用的"是否 ballooned"代理

> 历史纠偏：前篇把 `GCCountKB` 当"真实资源量代理"的表述不精确——它是活跃量、会被 sweep 拉回，只有在 **sweep 前采样**时才具备"是否 ballooned"的判据意义。

### drop-fat-state 止血契约（#105 / wangshu#11）

针对 grow-only latch 的下游止血，实现于 `pool_wangshu.go`（`wangshuPool.Return`，commit `628c4ca`）：

- 阈值 `arenaDropThresholdKB` = 1024（KB，= 16× wangshu 默认 64KB initial arena，常量与定标理由见 `pool_wangshu.go`）；命中计数 `dropFatCount`（**内部计数器，非公共 `/stats` 键**，仅供测试断言与"workaround 是否在触发"的廉价信号）
- `Return` 在 reset/sweep **之前**采样 `GCCountKB()`，超阈值则 `drop=true`：**跳过 reset 和 cadence sweep、不回 warm/pool**，让 fat state 落出作用域被 Go runtime 回收（连同其 fat backing slab）；下次 `Borrow` 重建干净 ~64KB state。代价是 create-rate 略升、换 high-water 有界
- **关键顺序约束（务必保持）**：drop 的 `GCCountKB` 采样必须排在 cadence sweep **之前**。两个 workaround（drop-fat-state + cadence-sweep）同在 `Return` 里且**顺序敏感、相互耦合**——cadence sweep 会拉回 `GCCountKB`，任何重排 `Return` 内部步骤的改动都可能**静默废掉 drop**（reflection 建议对此加注释级断言或测试钉住相对顺序防回归）

> 临时性：drop-fat-state 与 cadence-sweep 均为**临时止血**，代码已标 `REMOVE once wangshu#11/#9 lands`。上游 wangshu#11 落地 arena shrink/rebuild（或激活 `MaxArenaBytes` cap / 暴露 arena-cap 观测）后，high-water 可就地释放，两节连同本文档此处临时内容一并拆除。

## 非字符串 key 错误对等（fromValue / tableToGo）

`fromValue` / `tableToGo` 签名为 `(any, error)`。Lua 函数返回的 table 含非字符串 key 时，wangshu 后端**传播错误而非静默吞掉**（旧版静默返回空 map）：

- 错误文案：`lua: table has non-string key of type "<type>"`（`<type>` 由 `wangshuTypeName` helper 给出），与 gopher-lua 后端**字节级一致**（`b21a085`）
- 由 cross-validate **error-parity** section 断言（`scripts/cross-validate/05-error-parity.sh`）——权威 section 编号是 5

## 后端对比 benchmark

入口：

- `make bench-lua-backends`
- `scripts/bench-lua-backends.sh`（同机串行连跑两后端 + benchstat）

机制：`pine-go/benchmarks/` 是独立 Go 子模块（`pine-go/benchmarks/go.mod`），同时持有 wangshu 与 gopher-lua 两个对照库依赖。脚本以两套 build tag 串行跑相同 benchmark 集合，输出 benchstat delta 报告。

calibrated 端到端：`fixtures/benchmarks/realistic_*_calibrated*` 系列。其中 `realistic_*_calibrated_itemlua` 变体把 boundary 调用密度推到极致（per-item lua 加权打分，3000 调用/请求），用于钉住 boundary-dominated 形状下的端到端表现——本次 wangshu 翻默认时该变体两后端字节一致（`sample=1173.7`）、统计持平（p=0.21~0.84）。

## Arena 列轨 ABI（已评估的备选边界通道，未采用）

wangshu v0.1.4 公共 API 提供一条专为"per-item 整列投喂"设计的零拷贝边界通道，pineapple 现未使用。本节记录其契约与不采用的硬约束，供未来想优化 commonMode 边界的人查阅，避免重新发现后不知"落地需破 parity"而误动手。

### ABI 用法

- 宿主侧构造：`NewArena(nrows)` + `AddFloatColumn / AddInt64Column / AddBoolColumn / AddStringColumn(name, vals, present)`，再 `Program.Call(state, arena)` 执行。
- 脚本侧：固定全局名 `arena`，读 `arena.<col>[i]`（**1-based** 下标）与 `arena.rows`（行数）。
- 列**零拷贝引用**：宿主 `[]float64` 等列切片**不复制**，就地 NaN-box 暴露给脚本；不进 pin 表；同一 `*Arena` 只挂载一次，稳态零重建。
- null 经 presence bitmap 表达（`present` 参数）；列**只读**。

### 与现 commonMode 路径的对比

现 commonMode 走 `SetGlobal([]any) → makeArrayTable`：`NewTable` + N 次 `SetIndex` 逐元素装箱 + `RawSet` 构表。arena 通道消除**每请求 O(N×字段)** 的逐元素装箱 / table rehash / arena 表分配——整列以零拷贝引用一次性进 VM。

### 限制

- 列**只读**（脚本不可写回 arena 列）。
- int64 在 `|v| > 2^53` 时**读取报错**（超出 float64 尾数精度）。
- **不支持嵌套 table / map 列**，只支持扁平标量列（float / int64 / bool / string）。

### 未采用原因

落地需把 Lua 脚本访问约定从 `field[i]` 改成 `arena.field[i]`，而 `lua_script` 是四引擎共享的**字节级对等产物**（部分由 `apple/control.py` 自动生成、部分用户手写）——只改 wangshu 破 parity，真落地需四引擎都支持 arena ABI，是跨引擎工程。原型边界收益（Boundary 口径 N=100 -22% ~ N=3000 -46%、B/op -83%~-87%，提速随列长增长）真实，但端到端会被引擎框架稀释（沿性能演进路线校准事实 2 的逻辑，大概率落到个位数）。评估数据与"不立即落地、仅 profiling 证明 commonMode 边界为生产端到端热点才立项"的决策见 `llmdoc/memory/decisions/perf-evolution-roadmap.md` 第二步；完整调查方法与绝对数据见 `llmdoc/memory/reflections/wangshu-borrow-optimization-survey.md`。

### makeArrayTable 的 SetIndex 顺序 append O(N²) 建表（commonMode 现路径已知特性）

`makeArrayTable` 用 `NewTable()` 构表，再逐个 `SetIndex(1..N)` 顺序 append。隔离 benchmark（纯 `NewTable + N×SetIndex`）实测：ns/element 在 N≤200 区间平坦（正常 O(N)），自 N≈400 起明显超线性增长，到 N=1000 单次建表已是毫秒量级——整体呈 **O(N²) 建表签名，拐点约 N=200-400**（非孤立列长上的偶发悬崖）。稳定复现、方差极小。

- 根因（`wangshu/internal/crescent/rawtable.go`）：顺序 append 时超出数组段的 key 落哈希段，哈希段满触发 `rehash`，每次 rehash 重插全部活键（O(当前 N)）；数组段增长策略在纯顺序 append 下不够激进，反复溢出 + 反复 rehash 累积成 O(N²)。对比 Lua 5.1 原生顺序 append 是 amortized O(N)，wangshu 此处退化。
- **根因在 wangshu、修复也在 wangshu**：pineapple 的 `makeArrayTable` 就是标准逐元素 `SetIndex`，没有别的写法，adapter 无法用现有 API 单独绕开。已提上游 issue **Liam0205/wangshu#10**。
- arena 列轨路径（零拷贝、不逐元素 SetIndex）天然规避此退化——这也部分解释 arena 原型为何收益大。
- 生产在大列（N 数百以上）commonMode 观测到超线性建表延迟时可据此定位（无需回读 wangshu table 源码）。

## 决策记忆

- 翻默认门槛：见 `llmdoc/memory/decisions/perf-evolution-roadmap.md` 第三步——三条 AND 闸门（calibrated 不劣化 + 受影响场景显著胜出 + 双 tag 全绿）。
- 复盘：见 `llmdoc/memory/reflections/wangshu-backend-callinto-and-default-flip.md`。
