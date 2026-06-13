# [wangshu 后端 borrow/边界路径优化空间调查复盘]

## Task
- 用户开放式提问:"我们有针对 wangshu 做 borrow 之类的优化吗?考虑到它的用法和 gopher 不太一样。"
- 性质:**纯调查 + 原型量化**,目标是审计 wangshu 专属优化空间、评估候选、用对照 benchmark 量化"值不值得做"。
- 不改任何生产代码;原型 benchmark 文件用完即删。本篇记录的是调查方法、量化结论、意外发现与决策依据,不是代码说明。

## Expected vs Actual
- Expected:既然 wangshu 用法与 gopher 不同(NaN-boxing + arena + pin 表 + CallInto),borrow/pool 调度层应有可挖的 wangshu 专属优化。
- Actual:
  - borrow/pool 调度层**刻意对称**——`pool_wangshu.go` 与 `pool_gopher_lua.go` 共享同一双层 warm(`minIdle=100`)+ sync.Pool 模型、同一 5 元组计数器(`reference/lua-backend.md` Pool 复用模型)。这是为 benchmark 对照公平而**有意为之的设计,不是疏漏**,borrow 层无 wangshu 专属空间可挖。wangshu 真正的差异全在边界动作(CallInto 零分配、VM 内建、pin Release),不在调度层。
  - investigator 通读 wangshu v0.1.4 公共 API,审出 5 个未用候选,最高价值是 **arena 列轨 ABI**(`NewArena/AddFloatColumn/.../Program.Call(state, arena)`):把宿主 `[]float64` 列**零拷贝引用**暴露给脚本(`arena.<col>[i]`),专为"per-item 整列投喂"设计。pineapple 现在 commonMode 走 `SetGlobal([]any) → makeArrayTable`(`NewTable` + N 次 `SetIndex` 逐元素装箱),完全没用 arena。
  - 原型对照 benchmark(Boundary 口径,commonMode 灌 N 个 float 列 + Lua 内求和,A=现 SetGlobal 路径,B=`Program.Call(arena)`,count=10 单核,方差 1-3%)量化:N=100 时 16.5µs→12.9µs(**-22%**,B/op -83%);N=3000 时 667µs→363µs(**-46%**,B/op -87%)。arena 提速随 N 增长,因为它消除 N 次 SetIndex 逐元素装箱 + table rehash。
  - **意外发现**:现路径 A 在 N=1000 有稳定的性能悬崖——820µs,比 N=3000 的 667µs 还慢(违反单调性),稳定复现、方差极小。
  - **决策**:候选值得记录但**不立即落地**——落地需破四引擎 parity(改 Lua 脚本访问约定),且 -46% 是边界口径,落到端到端大概率只剩个位数。N=1000 rehash 悬崖单独留作可做的本地优化。

## What Went Wrong
- 本任务没有"翻车",但有几处**差点用微基准数字夸大生产收益**的诱惑,靠口径纪律拦下:
  1. 原型出来的 -46% 是 **Boundary 微基准**,不是 calibrated 端到端裁判。若直接拿这个数字汇报"arena 能提速 46%",会重蹈 `bench-lock-optimization-campaign.md` "把 large_5000 的 +247% 当主路径"的覆辙。`perf-evolution-roadmap.md` 校准事实 2 已两次证明 VM 层加速会被引擎框架(38-op DAG / stub I/O / 3000-item DataFrame)稀释到 ±5-7% 噪声带——所以 -46% 边界提速落到端到端大概率只剩个位数。
  2. benchmark 出现"N=1000 比 N=3000 还慢"的非单调性时,**没有直接拿它当基线下结论**。先重跑确认稳定复现(方差极小),再回读 wangshu table 源码确认根因(`NewTable()` 起始 array 段 `asize=0`,逐个 `SetIndex(1..N)` 触发 Lua 5.1 经典 table 动态 rehash,1000 这个特定大小命中坏的 rehash / array-vs-hash 段分布),才敢用。否则会用一个"踩了坑的基线"夸大 arena 对照倍数。
- 机器卫生:开跑前发现 4 个失控 `yes` 进程吃满 4 核(load 4.5),先向用户确认后 kill、双确认 load 回落 <1 才开跑——这正是 `feedback_kill_zombie_check_load.md` / `bench-lock-optimization-campaign.md` "zombie 污染整天数据"教训的预防性执行,不补救就数据全废。

## Root Cause
- **borrow 层无空间是设计契约,不是结论错误**:把 pool 调度层做成两后端对称,是为了 benchmark 对照只测"VM + 边界"的纯差异、不被调度层噪声干扰。理解这一点需要先读两边 pool 实现确认"对称是有意的",而不是看到"没优化"就以为是缺口——这一步事实确认是本次调查方法的第一环。
- **arena 收益规模真实但落地成本被 parity 锁死**:arena 的 -46% 边界提速量化无误,但落地要把 Lua 脚本访问约定从 `xs[i]` 改成 `arena.xs[i]`,而 `lua_script` 是 Go/Java/C++ **四引擎共享的字节级对等产物**(部分由 `apple/control.py` 自动生成、部分用户手写,`apple/compiler.py` 已有编译期改写脚本的先例)。只改 wangshu 会破 parity——真落地是**跨引擎工程,不是 pine-go 本地优化**。这个硬约束在开始写原型前就必须确认(本次先确认了 lua_script 来源才敢继续量化)。
- **N=1000 悬崖是生产 commonMode 当前就存在的隐患**:根因是 `makeArrayTable` 的 `NewTable()` 不带容量提示、array 段从 0 起逐步 rehash 扩容。arena 路径(零拷贝、不逐元素 SetIndex)天然没有这个问题;但它也意味着即便不上 arena,只要 wangshu 提供带 size 提示的 `NewTable`,`makeArrayTable` 预分配 array 段容量即可消除悬崖——**不破 parity、不改脚本**的独立本地优化。

## Missing Docs or Signals
- `reference/lua-backend.md` 已覆盖 pin 双向所有权、内存模型 safepoint、Pool 复用、benchmark 入口,但**没有 arena 列轨 ABI 这条已评估的备选边界通道**——下一个想优化 commonMode 边界的人会重新发现它,并可能不知道"落地需破 parity"这个硬约束就动手。
- `reference/lua-backend.md` 也**没有 makeArrayTable 的 N=1000 rehash 悬崖**这一 commonMode 已知特性。生产用户若在 N≈1000 规模观测到反常延迟,无文档可查,只能回读 wangshu table 源码。
- `perf-evolution-roadmap.md` 第二步(common-mode 列内核)目前只有"列内核负载迁移才是 VM 加速可见性闸门"的论断(design_doc/13:91 自陈),**缺一个边界层的量化数据点**坐实"迁移到列内核 / arena 后边界成本能降多少"。本次 -46% boundary 正好补这个实证空位。
- **口径纪律本身**(微基准 vs calibrated 裁判)在 `guides/benchmark-hygiene.md` 已有,但"开放式性能探索结论必须显式标注口径、不得用边界微基准数字代替端到端收益预测"这条信号,本次靠经验执行,值得在探索类任务里显式化。

## Promotion Candidates
- `memory/decisions/perf-evolution-roadmap.md` 第二步(common-mode 列内核):补 **arena 列轨边界层首个量化数据点**——N=100 -22% / N=3000 -46% boundary、B/op -83%~-87%,但端到端会被框架稀释(沿校准事实 2 的逻辑,大概率落到个位数)+ 落地需四引擎破 parity 的成本判断。作为"第二步是 VM 加速可见性真正闸门"论断的实证补强,而非"立即做"的指令。
- `reference/lua-backend.md`:
  - 新增 "arena 列轨 ABI——已评估未采用的备选边界通道"一节:用法(`NewArena/AddFloatColumn/Program.Call(state, arena)`、脚本侧 `arena.<col>[i]`)、零拷贝特性(宿主 `[]float64` 不复制)、限制(只读 / int64 > 2^53 精度 / 不支持嵌套列)、落地需破四引擎 parity(改脚本访问约定)的约束。
  - 把 **makeArrayTable 的 N=1000 rehash 悬崖**记为 commonMode 已知特性:根因(`NewTable()` 起始 array 段 asize=0 + 逐 SetIndex 触发 Lua 5.1 动态 rehash)、稳定复现、arena 路径天然规避。
- N=1000 rehash 悬崖是个**独立可做的本地优化**(若 wangshu 提供带 size 提示的 `NewTable`,`makeArrayTable` 预分配 array 段容量即可消除,不破 parity、不改脚本)——值得作为 follow-up / 潜在 upstream issue 留痕,不必等 arena 大工程。

## Follow-up
- recorder 阶段:把上述 promotion 推进 stable docs(重点是 `reference/lua-backend.md` 的 arena 备选通道 + N=1000 rehash 悬崖,以及 `perf-evolution-roadmap.md` 第二步的边界量化数据点)。本篇之外不碰稳定文档/decision。
- arena 落地若要真做:作为**跨四引擎工程**立项,先在 `apple/` 层(`control.py` 生成 + `compiler.py` 编译期改写)统一脚本访问约定 `xs[i]→arena.xs[i]`,再四引擎同步 + cross-validate 字节对等,不能 pine-go 单边落地。
- N=1000 悬崖:可作为低风险本地优化先行(向 wangshu 提带容量提示的 `NewTable` 需求,或在 pineapple 侧用现有 API 预热 array 段),与 arena 大工程解耦。
- 探索类任务纪律沉淀:把"开放式性能探索的结论必须显式标注口径(边界微基准 ≠ 端到端裁判)、非单调 benchmark 必须先查根因再用、落地评估必须先确认 parity 边界"这组探索纪律,考虑并入 `guides/benchmark-hygiene.md` 的探索章节。
