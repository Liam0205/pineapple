# Lua 后端参考（pine-go）

本文档记录 pine-go `transform_by_lua` 算子的 Lua VM 后端选择契约、共享抽象、wangshu 双向 pin 所有权边界 API、内存模型与 GC 触发点、pool 复用模型与后端对比 benchmark 入口。仅适用于 pine-go——pine-java 默认 LuaJC、pine-cpp 用 LuaJIT，不暴露 build-tag 切换面。

## 后端选择契约

- 默认：**wangshu**（纯 Go Lua 5.1 VM，NaN-boxing + arena GC，下限版本 **v0.2.0-rc3**），build tag 表达为 `!lua_gopher`
- Opt-in：`-tags=lua_gopher` → gopher-lua

编译期单一后端零运行时分发，binary 只链一个 VM。Build tag 极性是排他选择，不存在双后端共存运行时。Tag 文件在 `pine-go/operators/lua/` 下成对出现（`*_wangshu.go` `//go:build !lua_gopher` / `*_gopher.go` `//go:build lua_gopher`），Makefile 与 `scripts/bench-lua-backends.sh` 同步切换。

下限版本采用 release candidate（**v0.2.0-rc3**）的原因：下游依赖的 `State.NewArrayTable` / `State.MaybeCollectNow` / `State.ArenaCapKB` 三个 API 均为 v0.2.0 系列新增，上游 maintainer 已确认 v0.2.0 API 面冻结、rc 仅做缺陷修复；生产路径通过 cross-validate 54 项与双 tag 测试套全量验证。后续跟踪 rc4 / 正式 v0.2.0 时复跑 cross-validate + calibrated_itemlua 端到端 benchmark 即可平滑升级。

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

issue #8 反馈闭环：边界双拷贝（state.go:557 + wangshu.go:371，每调用 72B/2 allocs）→ CallInto 零分配；v0.1.4 引入此 API 后曾长期作 pine-go 下限，v0.2.0-rc3 起下限随 GC/arena API 升级前移（见「后端选择契约」节）。

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

### auto-GC pacing 缺口与 host-triggered collect（#100 / wangshu#9 已解）

由"safepoint 仅 opcode 路径"派生出一个 pacing 缺口：wangshu 的 auto-GC 只在 VM opcode safepoint 检查 trigger 阈值。**boundary-dominated 负载**（common-mode `transform_by_lua` 用 `SetGlobal` 灌大 composite + 极短脚本）每次执行的 opcode 极少、safepoint 稀少，于是 GC 的内部 accounting（`bytesAllocSince`）在推进、trigger 却几乎不触发——auto-GC 被**饿死**，arena 线性爬升。pacing 缺口是 wangshu 的本质事实，host 调度路径不会自动消失。

下游应对（v0.2.0-rc3 起，对应上游 **wangshu#9 已解**）：上游新增 host-callable API 三选一——`State.MaybeCollectNow()`（受 pacing 提示节制）/ `State.Collect()`（强制 full collect）/ `State.SetHostTriggeredCollect(on)`（opt-in、default off，让脚本执行中的 GC trigger 也允许 host 触发）。下游 `pool_wangshu.go`（`wangshuPool.Return`）改用 `MaybeCollectNow()`，每次返还即在该 goroutine 仍独占 state 时（交回 warm/pool 之前，遵守 wangshu 单 goroutine 单 state 契约）尝试推进一次 sweep。pacing 决策由 wangshu 自身控制，无需下游维护 cadence 计数。旧 cadence-sweep workaround 中的 `gcCadenceWangshu` / `collectProg` / `gcReturnCount` 已**整体移除**。

> 为什么不用 direction 1（`SetHostTriggeredCollect(on)`）：脚本执行中可能存在 transient GCRef 路径（intern / 元方法回调 / `fromInnerWithPin` 之前的瞬时引用），host 在 opt-in 状态下抢跑 sweep 不安全。maintainer 在 wangshu#9 close comment 中推荐使用 direction 2（host-callable 三选一）；下游 `Return` 路径用 `MaybeCollectNow()` 正是这条推荐路径。

### arena 高水位形态：transient peak 自愈与 sustained-fat latch（#105 / wangshu#11 partial）

host-triggered collect 让 GC accounting 平了，但生产 RSS 仍可能爬升——根因不在 pacing，而在 arena 的高水位释放形态。v0.2.0-rc3 起此节呈**两种 case**，必须分别理解：

**(c1) transient peak**：collector sweep 完会自动调用 `Arena.Compact()`，把 arena **cap** 缩到 `max(bump, 64 KiB)`，被翻倍 doubling 出来的 capacity 余量交还 Go runtime。曾被偶发大分配 balloon 过、随后 live set 已释放的 state 能就地自愈到接近 initial（64 KiB），不再 latch。

**(c2) sustained-fat latch**：`Compact()` **只缩 cap、不动 bump**——bump 单调增、GCRef 不 remap。如果 live set 一直保持大（reset 后仍持续灌大 composite），即使死对象已归 freelist，live 块与 freelist-dead 块仍散布在 `[0..bump)`，**bump extent 内仍 latch**。完整 copy-compact GC（真正回退 bump + 重映射 GCRef）是 maintainer follow-up，**v0.2.0 系列不会到来**。

上游 wangshu#11 状态（partial）：
- ✅ **`Options.InitialArenaBytes` / `MaxArenaBytes` 激活**：v0.1.4 下是 dead param（全模块零读取），v0.2.0-rc3 起 `NewState` 真正消费这两个 cap。
- ✅ **`State.ArenaCapKB()` 暴露真高水位**：公共 `State` 面新增此访问器，是 `bump`（不是 live bytes）的 KB 量；对 `Compact()` 后采样得到 post-compact cap，是下游做 drop 判据的杠杆。
- ⚠️ **`Arena.Compact()` 只解 transient peak**：sustained-fat live set 仍 latch 在 bump extent，full copy-compact 留作 follow-up。下游针对 (c2) 仍需 drop，详见「drop-fat-state 止血契约」节。

### 三态/四态内存判据

判断一条内存曲线是否异常，须区分三态（其中 (c) 在 v0.2.0-rc3 下进一步分两子情形）：

- **(a) pin 槽随请求单调增** = 入参方向 pin 泄漏。**真 bug**，已在 v0.10.1（`477dacd`）修；判据是 pin 槽计数本身单调增长（globals/pin 是独立账本，5 元组计数器钉不住，须直接观测）
- **(b) arena 字节滞后** = pin 槽已释放但 arena 未 sweep。**可自愈**：等下一次脚本执行命中 safepoint 即回落，是正常爬坡曲线（v0.2.0-rc3 起 `MaybeCollectNow` 也可加速回落，见 pacing 小节）
- **(c) backing slab high-water**，v0.2.0-rc3 起分两子情形：
  - **(c1) transient peak**：曾 balloon 过、live set 已释放的 state。**Compact 自愈**——collector sweep 完自动 `Arena.Compact()` 把 cap 缩到 `max(bump, 64 KiB)`，无需下游介入
  - **(c2) sustained-fat latch**：live set 持续保持大的 state。**不自愈**——`Compact()` 不动 bump，bump extent 内 live + freelist-dead 散布、仍 latch；唯有 **drop 掉该 state** 才能让 Go runtime 回收 fat backing slab（详见「drop-fat-state 止血契约」节）

> 历史纠偏：v0.1.4 时代 (c) 笼统为"不自愈"是当时正确表述；v0.2.0-rc3 partial fix 把 (c) 切成 (c1) 自愈 + (c2) 仍 latch 两种情形。系列第三篇 reflection `wangshu-rss-growonly-issue105-drop-fat-state.md` 中"backing slab latch 不自愈"的笼统判据在 v0.2.0-rc3 下已不充分；详见 reflection 第四篇 `wangshu-v020rc3-upgrade-and-workaround-refactor.md`。

### `GCCountKB` 语义陷阱 与 `ArenaCapKB` 真高水位

`State.GCCountKB()` 的**实测语义是 `bump − freeBytes`**，即**活跃量**（sweep 后随 `freeBytes` 上升而回落），**不是 capacity 高水位**：

- 门面注释（`wangshu.go`）写"含 freelist 空闲块"、core 注释写"活跃 KB"，两处**口径分叉**，必须读实现才能确认真实语义。这条事实在 v0.2.0-rc3 下仍成立
- 它会被 sweep **拉回**，sweep 后 reset 完整 live set 的 state 上 `GCCountKB` 落到 ~10 KB 量级，**完全看不到 latch 状态**

v0.2.0-rc3 起：上游新增 `State.ArenaCapKB()`，是 `bump` 的 KB 量（不是 `bump − freeBytes`），即**真高水位 / backing-cap 量**。它不会被 sweep 拉回，post-`Compact()` 采样得到 post-compact cap。**做 drop / 是否 latch 判据时优先用 `ArenaCapKB()`**；`GCCountKB` 仍是有用的活跃量观测（嵌入者诊断 live set 大小、判断 sweep 是否在工作），但不再是高水位代理。

> 历史纠偏：v0.1.4 时代把 `GCCountKB` 的"sweep 前峰值"当唯一可用的"是否 ballooned"代理，在 v0.2.0-rc3 下被 `ArenaCapKB()` 取代。前两篇 reflection 与本节 v0.1.4 版本中"sweep 前采样是判据"的表述已被覆盖，详见第四篇 reflection。

### drop-fat-state 止血契约（#105 / wangshu#11 partial，判据已迁移）

针对 (c2) sustained-fat latch 的下游止血，实现于 `pool_wangshu.go`（`wangshuPool.Return`）。v0.2.0-rc3 起判据已**从 `GCCountKB`（reset 前采样活跃量）迁移到 `ArenaCapKB`（`MaybeCollectNow` 后采样高水位）**：

- 阈值 `arenaDropThresholdKB` = 1024（KB，= 16× wangshu 默认 64 KiB initial arena，常量出处见 `pool_wangshu.go`）；命中计数 `dropFatCount`（**内部计数器，非公共 `/stats` 键**，仅供测试断言与"workaround 是否在触发"的廉价信号）
- 阈值含义已变：v0.1.4 下含义是"sweep 前活跃量上限代理"，v0.2.0-rc3 下含义是 **post-Compact backing cap 上限**——同一个常量 1024 KB，但 1024 与 64 的 16× 关系含义不同
- `Return` 顺序：`reset → RemoveContext → MaybeCollectNow → ArenaCapKB() 采样`，超阈值则 `drop=true`：**不回 warm/pool**，让 fat state 落出作用域被 Go runtime 回收（连同其 fat backing slab）；下次 `Borrow` 重建干净 ~64 KiB state。代价是 create-rate 略升、换 high-water 有界
- **transient peak 不触发 drop**：(c1) 场景下 `MaybeCollectNow` 让 collector 跑 sweep + 自动 `Compact()`，post-collect cap 缩回 `max(bump, 64 KiB)`，远低于 1024 阈值，drop 不触发。**(c2) sustained-fat 触发**：live set 持续保持大、bump 推过阈值，post-collect cap 仍超 1024，drop 触发

判据迁移的副效应：旧设计中 drop 的 `GCCountKB` 采样必须排在 cadence sweep **之前**（cadence sweep 拉回 `GCCountKB`），是一条脆弱的"顺序耦合"约束。新设计采样在 sweep **之后**——`ArenaCapKB` 是 backing cap、不会被 sweep 拉回——**顺序耦合天然消失**。这是 reflection #3 教训"工程信号"的体现：若判据迁移后仍需保留旧顺序约束，说明新 API 没真正取代旧 hack。

> 临时性：drop-fat-state 仍是临时止血，代码标 `REMOVE once wangshu 落地 full copy-compact GC（bump retraction + GCRef remapping）`。上游 maintainer 已在 wangshu#11 close comment 中明确该 follow-up **不会在 v0.2.0 系列里到来**——本节短期内不会拆除。

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

wangshu 公共 API 提供一条专为"per-item 整列投喂"设计的零拷贝边界通道（v0.1.4 即存在、v0.2.0-rc3 沿用），pineapple 现未使用。本节记录其契约与不采用的硬约束，供未来想优化 commonMode 边界的人查阅，避免重新发现后不知"落地需破 parity"而误动手。

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

### makeArrayTable 顺序 append 建表（wangshu#10 已解，下游已切 NewArrayTable）

历史现象：v0.1.4 下 `makeArrayTable` 用 `NewTable()` 构表 + 逐个 `SetIndex(1..N)` 顺序 append，隔离 benchmark 实测在 N≈400 起明显超线性，N=1000 时单次建表已是毫秒量级，整体呈 **O(N²) 建表签名**。已提上游 issue **Liam0205/wangshu#10**。

**根因纠正**（v0.2.0-rc3 maintainer profiling 报告）：早期推断"根因是 wangshu rawtable 数组段溢出哈希段触发反复 rehash"**是错的**。maintainer profiling 显示 90% 时间在 **`arena.popLarge`** ——根因是 arena **LARGE freelist 是单链 first-fit**，在 doubling 工作负载下 chain 变 O(N) 长，每次大块分配 O(N) 扫整链，累积 O(N²)；rehash 不是热点。早期推断由源码自证（读 `crescent/rawtable.go`）得出、缺 profiling 数据基线，是方法论盲点的产物（详见 reflection 第四篇）。

**已修（v0.2.0-rc3）**：
- 根因修复：arena LARGE freelist 改 **power-of-2 size-class buckets**（`9827141` / `bfbbbec`），按桶 O(1) 分配，从根本上消除 chain-walk 退化
- 辅助 API：`Table.Preallocate(n)` 让脚本侧预分配数组段；`State.NewArrayTable(vals []Value)` 一次性建表，跳过逐元素 `SetIndex`
- 即便仍用 naive `SetIndex` 顺序 append，根因修复后已是 amortized O(N)；maintainer 给出的 N=1000 benchmark 提速倍数（指向 wangshu#10 close comment）真实但易过时，不抄进本文档

**下游使用模式**（v0.2.0-rc3 起）：
- `makeArrayTable` 已切到 `State.NewArrayTable(vals)`，一次性建表
- 空数组 fast-path 走 `State.NewTable()`，避免无谓的 vals 切片构造

arena 列轨路径（本节所在的「Arena 列轨 ABI」上文）的零拷贝设计与本子节正交：它通过完全避开 host 侧 table 构造来消除此类边界开销，**未来若 wangshu#10 之外再出现新的 host-VM 边界热点**，列轨仍是备选项（落地破 parity 的硬约束仍在，见上文「未采用原因」）。

## 决策记忆

- 翻默认门槛：见 `llmdoc/memory/decisions/perf-evolution-roadmap.md` 第三步——三条 AND 闸门（calibrated 不劣化 + 受影响场景显著胜出 + 双 tag 全绿）。
- 复盘：见 `llmdoc/memory/reflections/wangshu-backend-callinto-and-default-flip.md`。
