# [wangshu RSS 单调爬升(#105 grow-only latch)与 drop-fat-state 下游止血复盘]

## Task
- 跟进上游 GitHub issue #100 / #105(Liam0205/pineapple),针对 wangshu(pine-go 默认纯 Go Lua 5.1 VM 后端,v0.1.4)的内存问题:
  - (1) 若已提的上游 issue 未覆盖根因,新提独立 issue;
  - (2) 做下游 workaround 保证生产 RSS 不再单调爬升。
- 结论:#105 与 #100 **同源不同根**,新提 **wangshu#11**(arena grow-only / 无 backing release + `InitialArenaBytes`/`MaxArenaBytes` dead-param)而非追评只讲 pacing 的 wangshu#9;下游在 `pool_wangshu.go` 的 `Return` 里加 drop-fat-state 止血(commit `628c4ca`,未 push)。
- 这是 wangshu 内存系列第三篇,承接 `wangshu-pin-table-input-leak-fix.md`(#100 入参 pin 泄漏,v0.10.1)与 #100 cadence-sweep(v0.10.2 已 ship)。

## Expected vs Actual
- Expected:#100 的两道止血(入参方向 Release `477dacd` + `Return` 每 256 次跑 `collectgarbage("collect")` cadence sweep)落地后,GC 内部 accounting 已平(`bytesAllocSince` 不再饿死),稳态内存应回到 Pool 复用上界(`minIdle + in-flight`,与 uptime 无关,见 `reference/lua-backend.md` Pool 复用模型)。
- Actual:cadence sweep 让 GC accounting 平了,但生产 **RSS 仍单调爬升**。源码核实(wangshu v0.1.4,module cache)定位到三个独立根因,均不在 #100/wangshu#9 的 pacing 范畴:
  1. **arena grow-only**:`grow64` 只翻倍 copy,整个 `Arena` 方法表无任何 shrink/release backing 路径;`sweep`/`freeObject` 只把死对象还 freelist(`Arena.Free` 累加 `freeBytes`),底层 `[]uint64` backing slab **永不交还 Go runtime**。fat state 被 warm/sync.Pool tier 缓存后,high-water 内存被 latch 住。
  2. **dead param**:`Options.InitialArenaBytes` / `MaxArenaBytes` 全模块零读取,`NewState`→`crescent.New()` 无参→`arena.New(arena.Options{})` 全零值,host 想 cap arena 也没有生效旋钮。
  3. **缺 cap 观测 API**(#105 正文未点透,本次额外发现):State 公共面只暴露 `GCCountKB() = bump − freeBytes`,是**活跃量不是 capacity 高水位**,会被 sweep 拉回(`freeBytes` 上升),无法用它识别 latch 住高水位的 fat state;公共面无 `Cap` / high-water 访问器。

## What Went Wrong
- 几乎踩进"把挂着 `follow-up to #100` 标题的 #105 当成 #100 的补充、并入 wangshu#9"的陷阱。核对 wangshu#9 **正文实际范围**才发现它只讲 pacing(sweep cadence),完全不涉及 backing release。pacing 与 backing-release 是两套修法,混在一个 issue 容易让上游只修一半。
- 下游 drop-fat-state 的判据**依赖一个会被误读的观测量**。#105 方向3 建议"`GCCountKB()` 超阈值就 drop state 不回池",但 `GCCountKB` 是活跃量、会被 cadence sweep 拉回:若在 reset/sweep **之后**采样,永远读到被拉回的小值,fat state 永远 drop 不掉。正确实现必须在 `Return` 里、reset/sweep **之前**采样。
- `GCCountKB` 两处注释口径不一(门面注释写"含 freelist 空闲块",core 注释写"活跃 KB"),不读实现(`bump − freeBytes`)无法确定真实语义。这与前篇 `wangshu-pin-table-input-leak-fix.md` 把 `GCCountKB` 当"真实资源量代理"的用法形成**纠偏**:它不是高水位、会被 sweep 拉回,只在 sweep 前采样才有"是否 ballooned"的判据意义。

## Root Cause
- **"follow-up" 措辞 ≠ 同根因**:issue 间的引用关系是作者主观叙事,根因归属必须回读被引用 issue 的正文范围来独立判定,而非顺着 follow-up 措辞合并。pacing(GC 何时跑)与 backing-release(跑完字节还不还 runtime)是正交两层,前者修好不蕴含后者。
- **观测量语义陷阱**:同名函数(`GCCountKB`)在门面层与 core 层注释口径分叉,且语义是"活跃量(可被 sweep 拉回)"而非"高水位(latch 量)"。基于它做控制决策(drop-or-not)时,**采样时机决定正确性**——必须早于把它拉回的那步(reset/sweep)。
- **两个 workaround 在同一 `Return` 方法里、顺序敏感且相互耦合**:cadence sweep 会拉回 `GCCountKB`,drop-fat-state 的采样必须排在 sweep 之前。这种"workaround A 的正确性依赖于它与 workaround B 的相对执行顺序"的耦合,是临时止血代码最易回归的点(后续任何重排 `Return` 内部步骤的改动都可能静默废掉 drop)。
- **文档同步漏项的累积**:`reference/lua-backend.md` 的"wangshu 内存模型与 GC 触发点"节(`wangshu-pin-table-input-leak-fix.md` 的 Promotion #3 落地产物)讲了 safepoint 仅 opcode 路径、pin 泄漏 vs arena 延迟归还的判据,但 **#100 的 cadence-sweep workaround(v0.10.2 已 ship)从未入文档**,也无 grow-only high-water latch / drop-fat-state / `GCCountKB` 语义陷阱的内容。即"上一次 #100 修复就漏了文档同步",本次 #105 若再不补,文档会与 `pool_wangshu.go` 里**两个**并存 workaround 都脱节。该节第 85 行"真泄漏判据是 pin 槽随请求单调增长……而非 arena 字节滞后"在 grow-only latch 场景下**不充分**——backing slab latch 既非 pin 泄漏、也非可自愈的"字节滞后",是第三种形态,需要补判据。

## Missing Docs or Signals
- `reference/lua-backend.md` "wangshu 内存模型与 GC 触发点"节缺三块,且彼此关联:
  1. **#100 cadence-sweep workaround(v0.10.2 已 ship)未文档化**:`Return` 每 256 次返还跑一次 `collectgarbage("collect")` 推进 auto-GC,绕过 boundary-dominated 负载下 safepoint 稀少导致的 auto-GC 饿死(对应 wangshu#9 pacing)。
  2. **#105 grow-only high-water latch 未文档化**:arena 翻倍只增不还、`sweep` 只回 freelist 不交还 backing slab,fat state 被池缓存后 high-water 被 latch;`InitialArenaBytes`/`MaxArenaBytes` 是 dead param(对应 wangshu#11)。需把第 85 行的"真泄漏判据"补成三态:pin 单调增(入参泄漏)/ arena 字节滞后(可自愈,等下次 safepoint)/ backing slab latch(不自愈,需 drop state)。
  3. **`GCCountKB` 语义陷阱未文档化**:它是 `bump − freeBytes`(活跃量,会被 sweep 拉回),不是高水位;门面与 core 注释口径分叉;用它做 drop 判据必须在 reset/sweep **之前**采样。这一条同时纠正本系列前篇把 `GCCountKB` 当"真实资源量代理"的不精确表述。
- `pool_wangshu.go` 里**两个顺序耦合的 workaround**(cadence sweep + drop-fat-state)缺一处文档承载它们的执行顺序约束(drop 采样必须早于 sweep),否则后续重排 `Return` 易静默回归。两处均已标 `REMOVE once wangshu#11/#9 lands`,文档侧应镜像这个"临时性 + 上游 issue 绑定"关系。

## Promotion Candidates
1. **(最高优先,doc-gap)** `reference/lua-backend.md` "wangshu 内存模型与 GC 触发点"节——补齐 #100 cadence-sweep(已 ship 但漏文档)+ #105 grow-only high-water latch(wangshu#11)两块事实,并把第 85 行单一"真泄漏判据"升级为**三态判据**(pin 单调增 / arena 字节滞后可自愈 / backing slab latch 不自愈)。这是本次 + 上次两轮修复共同的文档缺口。
2. `reference/lua-backend.md` 新增 **`GCCountKB` 语义陷阱**小节:语义 = `bump − freeBytes`(活跃量,sweep 后回落)≠ 高水位;门面/core 注释口径分叉须读实现确认;作 drop 判据须 sweep 前采样。同时纠正 `wangshu-pin-table-input-leak-fix.md` 把它当资源量代理的表述。
3. `reference/lua-backend.md` Pool 节或新增 **drop-fat-state 止血契约**:`Return` 在 reset/sweep **前**采样 `GCCountKB`,超 `arenaDropThresholdKB`(=1024,16× 默认 64KB initial arena)则不回池让 Go runtime 回收 fat backing slab;`dropFatCount` 计数器;**显式记录"drop 采样必须早于 cadence sweep"的顺序约束**与两 workaround 的相互耦合;标注随 wangshu#11/#9 落地移除。
4. `guides/investigation-to-fix-testing.md` 或复刻后端 checklist——沉淀方法论两条:(a) **跨 issue 根因归属:不顺 follow-up 措辞合并,回读被引用 issue 正文范围独立判定**;(b) **临时止血阈值用 probe 测试实测标定 fixture**(本次用 probe 测"多大 composite 把 arena 推过阈值":10万 float64≈1032KB 刚越界、100 元素≈16KB 安全),阈值取 16× 默认 initial arena 让 steady-state 绝不误触发,而非拍脑袋取值。

## Follow-up
- recorder 阶段:优先推进 Promotion #1(GC 节三态判据 + 两 workaround 文档化),这是连续两轮(#100/#105)累积的缺口;#2/#3(GCCountKB 语义 + drop-fat-state 顺序约束)紧随。
- 上游:跟踪 wangshu#11(arena backing release + 激活 `InitialArenaBytes`/`MaxArenaBytes` cap + 暴露 high-water/Cap 观测 API);落地后按 `REMOVE once wangshu#11/#9 lands` 标记拆除 `pool_wangshu.go` 两个 workaround,并同步删文档临时节。
- 代码健壮性:`Return` 内部任何重排都可能废掉 drop(采样须早于 sweep)——考虑加注释级断言或测试钉住"drop 采样在 sweep 之前"的相对顺序,防静默回归。
- 验证已做(写入备查):3 个测试钉死 fat-drop + 5 元组 invariant、lean 不误触发、并发 drop 平衡;两 build tag 全绿、race 干净、lint 0 issues。云端升级后观察 RSS 是否不再单调爬升、回到 `minIdle + in-flight` 上界。

## 后续纠偏注记(2026-06,本系列第四篇覆写)

本篇 Promotion #1 / #3 中"drop-fat-state 与 cadence-sweep 均为临时止血,wangshu#11/#9 落地后整体移除"的论断,在 v0.2.0-rc3 partial fix 下**不再成立**:
- wangshu#9 真等价解(host-callable API 三选一),cadence-sweep 已**整体拆除**,改用 `MaybeCollectNow()`
- wangshu#11 仅 partial:`Arena.Compact()` 解 transient peak 自愈、但 bump 不回退、sustained-fat live set 仍 latch;完整 copy-compact GC 是 maintainer follow-up 且**不会在 v0.2.0 系列里到来**
- drop-fat-state **不能拆,只能换判据**:从 `GCCountKB`(reset 前活跃量代理)→ `ArenaCapKB`(post-Compact cap 真高水位)。判据迁移的副产品是"采样必须早于 sweep"的顺序耦合天然消失——本篇重点警告过的脆弱约束在新设计下已不存在
- 本篇中"backing slab latch 不自愈"的笼统判据需细化为 (c1) transient peak 自愈 + (c2) sustained-fat 仍 latch 两子情形

详见 reflection 第四篇 `wangshu-v020rc3-upgrade-and-workaround-refactor.md`。本篇主体内容作为历史记录保留,不动。
