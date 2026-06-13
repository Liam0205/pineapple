# [wangshu SetGlobal 入参方向 pin-table 泄漏修复复盘]

## Task
- 用户报告云端服务 v0.10.0 启动后稳态内存显著高于旧版,问 v0.10.0 引入的 wangshu Lua 后端是否泄漏。
- 调查定位、给出根因归属(是 pineapple 没遵守 wangshu 的 pin-table 所有权契约,还是 wangshu 自身 bug)、修复并补回归测试。
- 结论:确实泄漏,在 pineapple 这一侧;wangshu 行为符合其文档契约。

## Expected vs Actual
- Expected:v0.10.0 翻默认为 wangshu(`9b28719`)前已经过双 tag 全绿 + calibrated/itemlua 端到端验证,内存上界本应是 `minIdle + in-flight 实例数`,与 uptime 无关(见 `reference/lua-backend.md` Pool 复用模型),不该随运行时间线性爬升。
- Actual:
  - `wangshuEngine.SetGlobal`(`pine-go/operators/lua/pool_wangshu.go`)走 `makeArrayTable` / `makeMapTable` 用 `st.NewTable()` 造复合 table,交给 `st.SetGlobal` 后再无人 `Release()`。wangshu 契约里 `NewTable()` 返回的 Value 占一个 pin 槽(GC root),`SetGlobal` 只拷 GCRef 不接管所有权 → 每次灌全局 table 都漏一个 pin 槽,arena 随请求数线性增长。
  - 最小复现三组对照坐实两个独立因素:(a) 当前形态不 Release,10k 次涨 ~10MB arena 线性;(b) 加 Release 但不强制 GC,仍涨;(c) Release + 每轮 `collectgarbage("collect")`,arena 持平 baseline。
  - 修复后 bump 0.10.0 → 0.10.1,全套 bump-version.sh 1-11 绿。

## What Went Wrong
- 复刻 wangshu 后端时只系统性审计了"实现路径 + backend-specific pool 计数器测试套"(上一篇 `wangshu-backend-callinto-and-default-flip.md` 的教训),**没把"宿主侧资源所有权契约"列入审计维度**。`cb58e08`(ForEach key/val 返回值方向漏 Release)和 `477dacd`(SetGlobal 入参方向漏 Release)是同一个 footgun 的对称两面;第一次修返回值方向时没顺手想到入参方向也有同款 pin 所有权。
- 回归测试此前完全缺位,且不是偶然:pool 单元测试套钉的是 borrow/return/create/reuse/active 5 元组计数器不变量,pin 槽泄漏**不破坏任何计数器不变量**——借了就还、计数全平,所以测试全绿但生产线性泄漏。这是"测试覆盖了可观测代理指标(借还计数),却没覆盖真正的资源量(arena/pin 占用)"的典型盲区。
- 内存曲线解读容易误判:即便正确 Release,wangshu 的 safepoint 只在 opcode 执行路径(RET/CALL/NEWTABLE/CLOSURE)触发,**宿主侧 NewTable/SetGlobal/Release 不触发 GC**,arena 归还延迟到下次脚本执行的 safepoint。稳态前必然有一段爬坡,若不知道这点,会把"正常的延迟归还爬坡"和"真泄漏"混为一谈。

## Root Cause
- **复刻后端的审计维度不全**:把"复刻实现 + 复刻 backend-specific 测试"当成了完整 checklist,漏掉了"逐一审计每个跨宿主↔VM 边界的资源所有权方向"。NaN-boxing + pin-table 内存模型把"忘 Release"从 Go GC 下的无害(LValue 由 Go GC 兜底)变成了硬泄漏,而 pineapple 侧没有任何文档承载这套所有权契约的**入参方向**——`reference/lua-backend.md` 的 CallInto 节只讲了 dst(返回值方向)需 Release,SetGlobal 灌 table 这条入参路径是文档空白。
- **测试断言层级错位**:5 元组计数器是"借还生命周期"的代理,不是"内存占用"的代理。资源泄漏类缺陷必须有直接观测真实资源量的断言(采样 `GCCountKB()` / arena 字节),计数器不变量无论多严都钉不住。
- **内存模型事实未沉淀**:wangshu safepoint 只在 opcode 路径触发、宿主 API 不触发 GC——这是嵌入者解读内存曲线和理解"Release 后为何不立即回落"的关键事实,但不在任何 pineapple 文档里,定位时只能回读 wangshu 库源码(`internal/crescent/alloc.go` + `collector.go` + `host.go`)才确认。

## Missing Docs or Signals
- `reference/lua-backend.md` 的 "wangshu 边界 API 契约(CallInto)" 一节只覆盖返回值方向(dst)的 pin 释放,**完全没有入参方向**:`SetGlobal` 灌复合 table 时 `NewTable()` 返回值占 pin 槽、`SetGlobal` 只拷 GCRef 不接管、caller 必须 Release(含嵌套子值与错误路径)。应升级为"双向 pin 所有权契约"。
- `reference/lua-backend.md` 的 "Baseline 重置契约" 一节没提 pool closed 时跳过 reset 的行为(`ac64491`)。
- wangshu safepoint 只在 opcode 执行路径触发、宿主侧 NewTable/SetGlobal/Release 不触发 GC、arena 归还延迟到下次脚本 safepoint —— 这个内存模型事实没有任何文档承载,直接影响内存曲线形状的解读(稳态前的合理爬坡 vs 真泄漏)。
- pool 测试套缺"直接观测真实资源量"的断言类型规约:计数器不变量不覆盖 pin/arena 泄漏,资源类缺陷需要采样真实占用的回归测试(本次 `TestWangshuSetGlobalCompositeNoPinLeak` 采样 `GCCountKB()` 断言增长有界,可作范式)。
- 发现一处**代码注释事实错误(本次未修,避免污染单域 fix 分支)**:`pine-go/operators/lua/pool_wangshu.go` 的 `fromValue`/`tableToGo` 注释(约 448、508 行)写 "cross-validate Section 12 (error parity)",但 error-parity 实际是 Section 5(`scripts/cross-validate/05-error-parity.sh`,命中 `non_string_table_key`),Section 12 是 extensibility-parity,与非字符串 key 无关。该错误注释由 `b21a085` 引入,应单独处理。

## Promotion Candidates
1. `reference/lua-backend.md` —— 把 CallInto 节扩成"**双向 pin 所有权契约**":除现有 dst(返回值方向)Release 外,补入参方向——`SetGlobal` 灌 table 时 `NewTable()` 占 pin 槽、`SetGlobal` 仅拷 GCRef、caller 必须在 SetIndex/Set 之后(含错误路径)立即 Release 嵌套子值;Release 对标量是 no-op 故可无条件调用。这是本次泄漏的根因文档缺口,优先级最高。
2. `reference/lua-backend.md` —— "Baseline 重置契约" 补 pool closed 时跳过 reset 的行为(`ac64491`)。
3. `reference/lua-backend.md` 或内存模型小节 —— 沉淀 "wangshu safepoint 只在 opcode 路径触发、宿主侧不触发 GC、arena 归还延迟到下次脚本 safepoint" 这一嵌入者关键事实,并点明它对内存曲线解读(稳态前合理爬坡)的影响。
4. `guides/investigation-to-fix-testing.md` 或 `reference/lua-backend.md` 测试节 —— 沉淀"资源占用类缺陷需直接观测真实资源量,计数器/借还不变量是代理指标钉不住泄漏"的测试层选择原则,`GCCountKB()` 采样断言为范式;并把"复刻后端时逐一审计每个宿主↔VM 边界的资源所有权方向(入参 + 返回值)"加进复刻后端 checklist(承接上一篇"复刻测试套"教训的下一层)。

## Follow-up
- recorder 阶段:把 1-4 推进 stable docs(重点是 `reference/lua-backend.md` 的双向 pin 所有权契约 + 内存模型 safepoint 事实)。
- 单独处理 `pool_wangshu.go` 约 448、508 行的注释事实错误(Section 12 → Section 5),不并入本次单域 fix 分支。
- 复刻后端审计 checklist 扩项:未来引入第三 Lua 后端(或任何带自管内存模型的嵌入式 VM)时,先逐边界过一遍资源所有权方向,再写实现。
- 内存曲线监控:云端升级到 0.10.1 后观察稳态内存是否回落到预期上界(`minIdle + in-flight`),确认线性爬升消失。
