# [wangshu v0.2.0-rc3 升级与两 workaround 重构 / 拆除 / 判据迁移复盘]

## Task
- 评估 wangshu v0.2.0-rc3 是否回应了我们前面提的上游 issue #9 / #10 / #11,以及能否顺势在 pine-go 侧拆掉两个临时 workaround(cadence-sweep + drop-fat-state)、把 makeArrayTable 切到 NewArrayTable。
- 任务形态从"评估"演变成"激进升级 + 重构 workaround + 验证",**中途有一次重要的方向回退**(下面会讲)。
- 这是 wangshu 内存系列**第四篇**,承接:
  - `wangshu-backend-callinto-and-default-flip.md`(后端引入 + CallInto + 翻默认,v0.10.0)
  - `wangshu-pin-table-input-leak-fix.md`(#100 入参 pin 泄漏,v0.10.1)
  - `wangshu-rss-growonly-issue105-drop-fat-state.md`(#105 grow-only + cadence-sweep + drop-fat-state,v0.10.2~v0.10.4)
  本篇**纠正了** 第三篇 reflection 与 `reference/lua-backend.md` 里的多处表述(具体在「历史纠偏」小节),后续 recorder 必须连锁修订。
- 工作树状态:`pine-go/go.mod` v0.1.4→v0.2.0-rc3、`pool_wangshu.go` 重构、`pool_wangshu_test.go` 一删一改一保留,**尚未 commit**(等用户指令)。

## Expected vs Actual
- Expected:rc3 release notes 与源码看起来全解了三个 issue → "激进路径"应可一鼓作气:升级 + 拆 cadence-sweep + 拆 drop-fat-state + 切 NewArrayTable + 删相关测试。这是任务开始时给用户的方案,user 选了它。
- Actual:
  - rc3 **完整回应**三个 issue 的方向是真的——`Arena.Compact()`(collector sweep 完自动调用,把 cap 缩到 `max(bump, 64 KiB)`)、`State.ArenaCapKB()` / `MaybeCollectNow` / `Collect` / `SetHostTriggeredCollect`、`Options.InitialArenaBytes/MaxArenaBytes` 激活、`Table.Preallocate(n)` + `State.NewArrayTable(vals)` + arena LARGE freelist 改 power-of-2 size-class buckets——**API 表面完全到位**。
  - **但是 #11 Direction 1(arena shrink)是 partial**——升级 + 改完编译,在还没跑测试时,user 留言"上游在 issue 里留言了,可以看看"。回头读 maintainer 关闭评论:"this shrinks cap to max(bump, 64 KiB) but doesn't retract bump or remap GCRefs. Live objects scattered across [0..bump) interspersed with freelist-dead blocks still occupy the same bump extent. A full copy-compact GC (real bump retraction + GCRef remapping) is a much larger change touching the GC mark/sweep core — left as a follow-up. For the pineapple#105 long-running-pool case, the immediate relief is: once a state's transient peak releases, the doubled cap doesn't latch (released to Go heap). For sustained-fat states (live set actually stays large), Direction 3's ArenaCapKB exposure is the lever — pool layers can drop them."
  - 即:**transient peak 自愈(Compact 解决),sustained-fat latch 仍需 drop**——maintainer 自己点名 ArenaCapKB 是给我们用来 drop 的杠杆。"拆两个 workaround"判断是错的:cadence-sweep 可以拆(MaybeCollectNow 真等价替换),但 drop-fat-state **不能删,只能换判据**。立刻回退激进路径,把 drop 加回来重写,告诉 user 这个发现并写最终落地。
  - 最终落地:cadence-sweep 替换成 `MaybeCollectNow`,drop 判据从 `GCCountKB`(活跃量,sweep 前采样)迁移到 `ArenaCapKB`(post-Compact cap,sweep 后采样),`makeArrayTable` 切到 `NewArrayTable`(空数组 fast-path 走 `NewTable`),fat fixture 从 120k 调到 200k(post-Compact cap 实测)。
  - 验证:双 build tag 全绿、race 干净、lint 0 issues、cross-validate 54 项 0 FAIL(wangshu 升级不影响 byte-equal)。

## What Went Wrong
- **评估阶段没主动读 issue 关闭评论**,把"源码看到 Compact() / ArenaCapKB() / NewArrayTable() / power-of-2 buckets 全到位"等价当成"三个 issue 都全解"——源码看到的是"有什么",不是"还没有什么"。前者只能由上游叙事(release notes / closing comments)给出。若不是 user 一句"上游在 issue 里留言了",激进路径会一直执行到 commit/push,后果是把 drop-fat-state 错误地一起拆掉,**在 sustained-fat 生产负载上 RSS 仍会 latch、回归到 #105 的现象**——这是一次"方向性错误险些 ship"的近 miss。
- **第三篇 reflection 与 `reference/lua-backend.md` 在 `makeArrayTable` 的根因推断上是错的**——前面那一轮调查推断"顺序 append 时超出数组段的 key 落哈希段、哈希段满触发 `rehash`、累积成 O(N²)",并写进了稳定文档(`lua-backend.md` 第 182 行)。但 wangshu maintainer 关闭 #10 时贴的 profiling 数据显示:**90% CPU 时间在 `arena.popLarge` 上,根因是 arena LARGE freelist 是单链 first-fit**,扫整链找合适块导致 O(N²);rehash 不是热点。修复是 LARGE freelist 改 **power-of-2 size-class buckets**,`Preallocate`/`NewArrayTable` 是辅助优化避免反复 grow。我们之前在 reflection 里写"根因/修复在 wangshu"是对的,但具体根因机理写错了——读 `crescent/rawtable.go` 源码自证的推断没有 profiling 数据做基线。
- **fat fixture 标定阈值漂了**:旧 fixture 用 120k float64 元素,基于 v0.1.4 下 `GCCountKB ≈ 1032 KB` 刚越 1024 KB 阈值。v0.2.0-rc3 下,**ArenaCapKB 的标定全变了**——probe 实测:n=100k post-Compact cap=793 KB(**不到**阈值);n=200k=1574 KB(越过)。判据迁移后必须重新 probe,旧的 fixture 大小用 ArenaCapKB 是会 false-negative 的。如果不主动 probe,直接拿旧 120k 跑测试会失败,然后误判为"drop 没工作"。
- **`TestWangshuDropsFatStateOnReturn` 里有一行旧 sanity check `if kb := we.st.GCCountKB(); kb <= arenaDropThresholdKB { ... }`**——判据迁移后这条 sanity 用错指标(`GCCountKB` 是活跃量,reset 后回落到 ~10 KB),必须删。如果没察觉到,fat workload 这条 check 会立即失败、把测试导向错误结论。

## Root Cause
- **rc 评估的根因覆盖检查必须靠上游叙事**:rc / 大版本升级时,"源码看到 API 表面到位"和"issue 真的从根因层关掉"不是同一件事。源码自证只能告诉你"上游做了什么",**告诉你"上游故意没做什么 / 留作 follow-up 什么"** 的唯一渠道是上游的 issue close comments / release notes。这次的 #11 是教科书例子:`Arena.Compact()` 确实存在并工作,但它是"shrink cap to max(bump, 64 KiB)",不是真的 copy-compact GC——maintainer 在 close comment 里明确说"留作 follow-up"。**评估阶段不读 issue close comments 就给出激进方案,是方法论缺位,不是个例**。
- **"workaround 拆除"判定必须区分"上游解了 root cause"还是"上游只解了我们之前用的 proxy"**:
  - cadence-sweep 的目标是"让 GC accounting 上的 bytes 真去 sweep,把 transient peak 还给 runtime"——`MaybeCollectNow` 是**真等价替换**(API 语义对齐:host 主动触发 GC),所以 cadence-sweep 可拆。
  - drop-fat-state 的目标是"sustained-fat state 不能让它一直占着 fat backing slab、必须把它丢出 pool 让 Go runtime 回收 backing"——`Compact()` 只解了 transient peak 那一半(cap 缩回 `max(bump, 64 KiB)`),**sustained-fat 这一半上游故意没做**(留作 copy-compact GC follow-up)。所以 drop-fat-state **不能拆,只能迁移判据**:从"sweep 前活跃量(GCCountKB)"迁移到"sweep 后高水位(ArenaCapKB)"。
  - 判别准则:**workaround 拆除前先问"上游修的是我们 workaround 防御的 root cause,还是只是我们用的 proxy 观测量?"** 前者真拆,后者只换判据。
- **顺序耦合消失是好的工程信号(被动检测有效)**:旧设计的脆弱点是"drop 的 `GCCountKB` 采样必须排在 cadence sweep **之前**"——cadence sweep 拉回 GCCountKB,采样错位静默废掉 drop(前篇 reflection 重点警告过)。新设计是 `reset → RemoveContext → MaybeCollectNow → ArenaCapKB 采样 → drop`,**采样在 sweep 之后**,因为 ArenaCapKB 是 backing cap 不会被 sweep 拉回,**顺序耦合天然消失**。这不是巧合——"判据迁移到 sweep-resistant 量"的副作用就是"采样时机不再敏感"。如果换判据后仍需"采样必须早于 sweep",说明新 API 没真正取代旧 hack。
- **transient peak vs sustained-fat 是 v0.2.0-rc3 引入的新概念分层**:v0.1.4 下两者无法区分(arena grow-only、什么都 latch);v0.2.0-rc3 下 Compact 把这两类切开了——transient peak 自动自愈,sustained-fat 仍需下游 drop。这个分层是 partial fix 推出的副产品,**前篇 reflection 与稳定文档里"backing slab latch 不自愈"的笼统表述,在 v0.2.0-rc3 下已经不准**——必须分两种 case 描述。
- **执行中纠偏窗口的成本指数模型**:激进路径走到"代码改完、文档没改、测试没跑"是低成本纠偏点;若再向前一步(commit、文档写完、cross-validate 跑过、push 出去),纠偏成本指数上升(要 revert commit、改文档、重 push、重审)。**user 一句"上游在 issue 里留言"恰好落在最便宜的纠偏窗口里**——这一次靠运气而非机制。

## Missing Docs or Signals
- **`guides/investigation-to-fix-testing.md` 缺"rc 升级评估必读上游 issue close comments / release notes"** 的方法论。本次任务前期完全没有这一步,纯靠 user 的偶然提示才进入纠偏窗口。这个信号应该是 rc 升级类任务的入口动作之一,与"先读 llmdoc / startup.md"同级。
- **`guides/investigation-to-fix-testing.md` 缺"workaround 拆除前必须区分 root-cause vs proxy"** 的判别准则。这次靠 maintainer 自己在 close comment 里点名 ArenaCapKB 是 drop 的杠杆才避免了误删 drop——若 maintainer 没主动点,我们可能要在 sustained-fat 生产负载暴露后才回退。
- `reference/lua-backend.md` 多处与本次落地不一致(下面 Promotion 详述):cadence-sweep 章节、grow-only 章节、三态判据、`GCCountKB` 陷阱、drop-fat-state 契约、Arena 列轨 ABI 的 makeArrayTable 根因、build tag 下限版本、index.md 摘要——这些都已与代码脱节,必须连锁更新。
- `wangshu-rss-growonly-issue105-drop-fat-state.md`(第三篇 reflection)说 "drop-fat-state 是结构性方案前的临时止血,wangshu#11 落地后整体移除"——这个论断在 wangshu#11 partial 后**不再成立**:drop 仍需要,只是判据迁移。该篇需追加纠偏注记或交叉链。
- **probe 实测数据(写入备查,作为"transient self-heal vs sustained-fat latch"的实证)**——模拟 Return 路径(reset → MaybeCollectNow → ArenaCapKB 采样):

  | n      | ArenaCapKB | 解读                                                  |
  | ------ | ---------- | ----------------------------------------------------- |
  | 100    | 64.0       | 完美回到 initial,Compact 全自愈                        |
  | 1000   | 64.0       | 同上                                                  |
  | 5000   | 64.0       | 同上                                                  |
  | 20000  | 168.2      | 开始 latch(bump 推过 grow doubling)                  |
  | 50000  | 402.6      | 继续 latch                                            |
  | 100000 | 793.2      | 仍**不到** 1024 阈值——不会触发 drop                   |
  | 200000 | 1574.5     | 越过阈值,触发 drop                                    |

  GCCountKB 在所有 N 下 post-collect 都是 ~10.7(live set reset 后空)——这印证:**ArenaCapKB 才是 latch 量**,GCCountKB 在 sweep 之后看不到 sustained-fat,**判据必须用 ArenaCapKB**。
- 测试套缺一个"判据迁移 mock"的范式断言——`TestWangshuReturnDrivesArenaCompact` 替换了 `TestWangshuGCCadenceBoundsArenaWithoutInLoopCollect`,语义同构但底层 API 全换。这种"测试换 API 但语义保持"的迁移模式值得作为复刻后端的 checklist 项。

## Promotion Candidates

**为什么本批 promotion 不能延后**:本次稳定文档已经在 5 个位置(`lua-backend.md` cadence-sweep 节 / grow-only 节 / 三态判据 / `GCCountKB` 陷阱节 / drop-fat-state 节 / makeArrayTable 根因 / build tag 下限,加 `index.md` 摘要 + 第三篇 reflection)与现工作树代码 / wangshu maintainer 给出的事实**强烈脱节**。下一个读 lua-backend.md 的人会按过期内容做决策——比如"drop 采样必须早于 cadence sweep"(已不存在的耦合)、"GCCountKB 是判据"(已迁移到 ArenaCapKB)、"makeArrayTable 根因是 rehash 风暴"(实际是 arena LARGE freelist)。这些都属于"会主动诱导错误判断"的活跃误导,不是消极陈旧。

1. **(最高优先)** `reference/lua-backend.md` "wangshu 内存模型与 GC 触发点" 全节重写——本节是最大重灾区,**多处自相矛盾或与代码脱节**,必须现在改:
   - "auto-GC pacing 缺口与 cadence-sweep workaround" 小节:**归档**——改写成"v0.2.0-rc3 起 `MaybeCollectNow` 替代 cadence-sweep(host 主动触发 GC),代码侧 `gcCadenceWangshu` / `collectProg` / `gcReturnCount` 已移除"。
   - "grow-only high-water latch" 小节:改写——"v0.2.0-rc3 起 collector sweep 完自动 `Arena.Compact()`,**transient peak** cap 缩到 `max(bump, 64 KiB)` 自愈;**但 bump 不回退、GCRef 不 remap(maintainer follow-up)**,sustained-fat live set 仍 latch 在 bump extent,需 drop"。
   - "三态内存判据" 小节:**(c) 拆成 c1/c2**:c1) transient peak Compact 自愈、c2) sustained-fat 仍 latch 需 drop。引用 maintainer close comment 原文坐实"为什么 c2 不消失"。
   - "`GCCountKB` 语义陷阱" 小节:保留(教育价值仍在——告诉嵌入者 sweep 前后语义差异),但**加补丁**:"v0.2.0-rc3 起 `State.ArenaCapKB()` 是更精准的 backing-capacity 高水位 API,做 drop 判据应用 ArenaCapKB(sweep 后采样);GCCountKB 仍是活跃量,sweep-前/sweep-后行为差异仍是观测员要懂的事实"。
   - "drop-fat-state 止血契约" 小节:**重写**——判据 `ArenaCapKB`(post-`MaybeCollectNow` 采样),阈值含义从 "live-bytes 代理" → "post-Compact backing cap";**删除整段"关键顺序约束(务必保持):drop 采样必须排在 cadence sweep 之前"**(顺序耦合已消失,继续保留会误导后人引入不存在的约束);常量数字 1024 KB(=16× 64 KB initial)保留,但说明从 "v0.1.4 GCCountKB 阈值" 改为 "v0.2.0-rc3 ArenaCapKB 阈值";阈值 sizing 数据点直接用上文 probe 表替换;"REMOVE once wangshu#11/#9 lands" 标记改为"REMOVE once wangshu 落地完整 copy-compact GC(bump retraction + GCRef remapping)follow-up";`dropFatCount` 语义不变。

2. `reference/lua-backend.md` "Arena 列轨 ABI" 节末段 `makeArrayTable 的 SetIndex 顺序 append O(N²) 建表`:**纠错根因 + 状态更新**——
   - 现有第 182 行"根因(`wangshu/internal/crescent/rawtable.go`):顺序 append 时超出数组段的 key 落哈希段,哈希段满触发 rehash,每次 rehash 重插全部活键(O(当前 N));数组段增长策略在纯顺序 append 下不够激进"**全部是错误推断**,wangshu maintainer profiling 显示 90% 时间在 `arena.popLarge`,根因是 **arena LARGE freelist 单链 first-fit**(O(N) 扫整链找合适块,累积 O(N²)),不是 rehash 风暴;
   - 修复在 wangshu:LARGE freelist 改 **power-of-2 size-class buckets**(根因修复)+ `Preallocate(n)` / `NewArrayTable(vals)` 辅助优化避免反复 grow;
   - 下游已切到 `NewArrayTable`(`makeArrayTable` 走 `vals []wangshu.Value` 一次性建表,空数组走 `NewTable` fast-path);
   - 标注"本节的早期根因推断由 reflection 第四篇纠正,profiling 数据来自 maintainer 关闭 #10 时的报告,以此为权威"。

3. `reference/lua-backend.md` 后端选择契约小节:下限版本 `v0.1.4` → `v0.2.0-rc3`。

4. `llmdoc/index.md` 中 `lua-backend.md` 条目摘要:全段重写——目前列举的 "cadence-sweep workaround(v0.10.2 已 ship)"、"grow-only high-water latch"、"三态内存判据(... backing slab latch=不自愈需 drop)"、"`GCCountKB`=bump−freeBytes 活跃量语义陷阱(...须 sweep 前采样)"、"drop-fat-state 止血契约(Return reset/sweep 前采样超 arenaDropThresholdKB ... + 两 workaround 顺序耦合约束,标 REMOVE on wangshu#11/#9)"、"makeArrayTable 的 SetIndex 顺序 append O(N²) 建表(根因/修复在 wangshu,已提 issue #10)" 全部是过时表述,需镜像上面 #1/#2/#3 的修订。

5. `llmdoc/memory/reflections/wangshu-rss-growonly-issue105-drop-fat-state.md`(第三篇):追加**纠偏注记或顶端交叉链**——"本篇 Promotion #1 中'wangshu#11 落地后两 workaround 整体移除'的论断在 wangshu#11 **partial fix**(只解 transient peak、sustained-fat live set 仍 latch)下不再成立。drop-fat-state **不能移除**,判据迁移为 `ArenaCapKB`(post-Compact cap)。详见 reflection 第四篇 `wangshu-v020rc3-upgrade-and-workaround-refactor.md`"。同时本篇 reflection(第四篇)是该论断的纠偏入口,index.md 第三篇摘要也建议追加"被第四篇纠偏"标识。

6. **(中等优先,但有最大杠杆)** `guides/investigation-to-fix-testing.md` 沉淀**两条 rc 升级方法论**:
   - **rc 升级评估**:"必读上游 close comments / release notes 才能决定 workaround 拆除范围;源码自证只能给出'上游做了什么','上游故意没做什么 / 留作 follow-up 什么'必须由上游叙事给出。`Liam0205/wangshu#11` partial fix 是本仓首次踩到这条线"(给一个具名失败案例,后续读者能 google 出原始资料,而非抽象规则);
   - **workaround 拆除判别**:"拆除前先问'上游修的是 workaround 防御的 root cause,还是只是 workaround 用的 proxy 观测量?'前者真拆,后者只换判据。本仓实例:cadence-sweep 拆除(MaybeCollectNow 真等价替换),drop-fat-state 只换判据(GCCountKB → ArenaCapKB)";
   - 这两条 promotion 是本系列教训中**复用价值最高的**——下一次任何 rc 升级 + workaround 评估都会直接受益,不仅限于 wangshu。

7. **(可选)** `guides/investigation-to-fix-testing.md` 或 `reference/lua-backend.md` 测试节:沉淀"判据迁移时 fat fixture 必须重新 probe"——大小是阈值的函数 + 阈值定义的 API 变了(GCCountKB → ArenaCapKB)→ 旧 fixture 大小可能 false-negative。本次 probe 表(100/1000/5000/20k/50k/100k/200k)是范式,可作 fixture 标定 checklist。

## Follow-up
- recorder 阶段优先级:Promotion **#1 + #2 + #3 + #4 必须本轮一起做**——这四项是稳定文档与代码工作树的连锁脱节,任一遗漏后续读者就会被诱导;#5 紧随(避免第三篇 reflection 持续以错误论断诱导后续 task);#6 是高杠杆通用方法论,值得本轮一起入 guides;#7 可分轮。
- 升级 push 前:等用户明确指示;commit message 需提到"判据迁移"而非"workaround 拆除"——避免历史搜索时被误读为"#105 完全解决"。建议:"upgrade wangshu v0.1.4 → v0.2.0-rc3: replace cadence-sweep with MaybeCollectNow; migrate drop-fat-state criterion GCCountKB → ArenaCapKB (wangshu#11 partial — sustained-fat still latches); use NewArrayTable to bypass arena LARGE freelist O(N²)"。
- 上游 follow-up:跟踪 wangshu maintainer 在 #11 close comment 里提到的 follow-up——完整 copy-compact GC(bump retraction + GCRef remapping)。落地后 drop-fat-state 可真正退场,本仓再次 review。
- 工程健壮性:rc → stable release 跟踪——v0.2.0-rc3 是 release candidate,follow rc4 / 正式 v0.2.0 时复跑 cross-validate + bench(尤其 calibrated_itemlua 端到端,需确认 NewArrayTable + MaybeCollectNow 不引入 boundary regression)。
- 验证已完成(备查):pine-go 全包测试(含 race)、两 build tag 全过、transform 包通过、gofmt / vet / golangci-lint 0 issues、cross-validate 54 项 0 FAIL。
