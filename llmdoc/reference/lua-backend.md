# Lua 后端参考（pine-go）

本文档记录 pine-go `transform_by_lua` 算子的 Lua VM 后端选择契约、共享抽象、wangshu 边界 API、pool 复用模型与后端对比 benchmark 入口。仅适用于 pine-go——pine-java 默认 LuaJC、pine-cpp 用 LuaJIT，不暴露 build-tag 切换面。

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

## wangshu 边界 API 契约（CallInto）

wangshu v0.1.4 引入 `CallInto(dst []Value, fn Value, args ...Value) error` 零分配边界路径：

- 调用方拥有 dst：必须自行预分配并传入
- **dst 底层复用 wangshu 的内部栈，下次进 VM 前必须消费完**
- LuaOp 的消费模式：`CallInto` 返回后立即 `fromValue` 转出 + `Frame.SetItem` 写回 DataFrame，不持有 dst 跨调用
- 类型转换语义：string 走 arena 拷贝（独立可逃逸），table/function 仍是 pin 句柄需 `Release()`

issue #8 反馈闭环：边界双拷贝（state.go:557 + wangshu.go:371，每调用 72B/2 allocs）→ CallInto 零分配，wangshu v0.1.4 锁定为 pine-go 下限。

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

## 后端对比 benchmark

入口：

- `make bench-lua-backends`
- `scripts/bench-lua-backends.sh`（同机串行连跑两后端 + benchstat）

机制：`pine-go/benchmarks/` 是独立 Go 子模块（`pine-go/benchmarks/go.mod`），同时持有 wangshu 与 gopher-lua 两个对照库依赖。脚本以两套 build tag 串行跑相同 benchmark 集合，输出 benchstat delta 报告。

calibrated 端到端：`fixtures/benchmarks/realistic_*_calibrated*` 系列。其中 `realistic_*_calibrated_itemlua` 变体把 boundary 调用密度推到极致（per-item lua 加权打分，3000 调用/请求），用于钉住 boundary-dominated 形状下的端到端表现——本次 wangshu 翻默认时该变体两后端字节一致（`sample=1173.7`）、统计持平（p=0.21~0.84）。

## 决策记忆

- 翻默认门槛：见 `llmdoc/memory/decisions/perf-evolution-roadmap.md` 第三步——三条 AND 闸门（calibrated 不劣化 + 受影响场景显著胜出 + 双 tag 全绿）。
- 复盘：见 `llmdoc/memory/reflections/wangshu-backend-callinto-and-default-flip.md`。
