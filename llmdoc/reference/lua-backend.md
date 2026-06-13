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
- 因此稳态达成前会有一段"延迟归还"的合理内存爬坡：pin 槽已释放但 arena 未 sweep。这是正常曲线形状，**不应与真泄漏混淆**——真泄漏的判据是 pin 槽计数本身随请求单调增长（如未 Release 的入参路径），而非 arena 字节滞后

内存上界仍由 Pool 复用模型决定（`minIdle + 当前 in-flight`，与 GC 周期 / uptime 无关）；本节只补充"何时真正释放字节"的时间维度，不改变上界结论。

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

### makeArrayTable 的 N=1000 rehash 悬崖（commonMode 现路径已知特性）

`makeArrayTable` 用 `NewTable()` 构表，**array 段起始 `asize=0`**，逐个 `SetIndex(1..N)` 触发 Lua 5.1 经典 table 动态 rehash 扩容。在特定列长（实测 N≈1000）上出现**非单调性能悬崖**——比更大的 N（如 N=3000）反而更慢，稳定复现、方差极小。

- 这是 commonMode 当前就存在的特性，生产在 N≈1000 规模观测到反常延迟时可据此定位（无需回读 wangshu table 源码）。
- arena 列轨路径（零拷贝、不逐元素 SetIndex）天然没有此悬崖。
- 修复无需上 arena、**不破 parity、不改脚本**：若 wangshu 提供带 size 提示的 `NewTable`，`makeArrayTable` 预分配 array 段容量即可消除，是独立可做的本地优化候选。

## 决策记忆

- 翻默认门槛：见 `llmdoc/memory/decisions/perf-evolution-roadmap.md` 第三步——三条 AND 闸门（calibrated 不劣化 + 受影响场景显著胜出 + 双 tag 全绿）。
- 复盘：见 `llmdoc/memory/reflections/wangshu-backend-callinto-and-default-flip.md`。
