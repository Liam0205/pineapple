# [wangshu 后端引入、CallInto 反馈闭环与默认翻转复盘]

## Task
- 参考 wangshu 仓的 Makefile 工程基建更新本仓。
- 引入 wangshu 作为 pine-go 可选 Lua 后端,通过 build tag 控制。
- 用 realistic_*_calibrated 系列做 wangshu vs gopher-lua 端到端对比;胜出则翻默认。
- 任务跨 wangshu v0.1.3 → v0.1.4,持续 30+ commits,期间用户两次关键质疑塑造了最终结论。

## Expected vs Actual
- Expected:wangshu README 称 simple 9.0x faster,引入后 isolated/calibrated 都应明显胜出,直接翻默认。
- Actual:
  - 在 v0.1.3 上,isolated bench 实测 wangshu 反而比 gopher 慢 6-24%,与 README 头条数字方向相反。
  - 根因定位到 `Call` 返回值在 `state.go:557` 与 `wangshu.go:371` 双重拷贝(每次固定 72B/2 allocs),与脚本无关。提 issue #8 给作者,30 分钟内被采纳为 v0.1.4 `CallInto` 零分配边界路径。
  - CallInto 后,纯 VM 6x 胜出、isolated item-mode 时间-12.5%/分配-21.5%(L5 时间-27%/分配-35%);**但 calibrated 端到端三个变体全部统计持平**(p=0.21~0.84)。
  - 决策:虽 calibrated 持平,boundary-dominated 隔离负载明确胜出 + 全套件零回归,**翻默认**(`!lua_gopher` = wangshu)。

## What Went Wrong
- 最初准备直接信 README 头条数字。如果只跑 calibrated 不做 isolated 对照,会得出反向结论(wangshu 持平),从而错过 boundary cost 这条线。
- 复刻 wangshu 后端时只复刻了实现路径(Backend/Pool/Engine),**没复刻 backend-specific 的测试套**——gopher 侧 `pool_gopher_lua_test.go` 在 `//go:build !lua_wangshu` 下钉住 borrow/return/create/reuse/active 5 元组不变量,wangshu 侧从 commit e882665 起一直缺这层覆盖,直到这次审计才补上 `pool_wangshu_test.go`。
- `TestRefreshDefersCloseWhileBorrowed` 偶发失败时,第一反应是怀疑翻转引入的回归。实证两个 tag 下都偶发,与 Lua 后端无关——这是测试本身的同步点 bug,与本次任务无关但顺手暴露。
- 翻默认时 build tag 极性反转,涉及 5 个 tag 文件 + 3 处 doc comment + Makefile + script + bench 注释,容易遗漏。

## Root Cause
- **测量路径不对等**:wangshu 官方 baseline 测 PureVM 路径(`prog.Run(st)`,脚本内部硬编码数据,完全不跨边界);LuaOp 真实路径每次 `SetGlobal + Call + 读返回值`。两者数量级在不同维度上,9x 是 PureVM 上限,嵌入实际场景不可达。README 头条数字对嵌入者会误导。
- **测试覆盖盲点**:复刻后端时假设"实现等价 → 测试也等价",未审计被 build tag 隔离掉的、与新后端绑定的测试是否需要复制一份。这是 reflection 中"inventory 幻觉"的近亲——可称为"测量盲点"。
- **flaky 测试根因**:`refreshLoop` 顺序是 `fetcher(counter++) → value.Swap → old.release()`,但测试用 `counter >= 2` 作为 refresh 完成信号——counter 递增在最早,前置点和真正完成点之间任何代码都是窗口。事件驱动同步(channel + select + 超时)优于"前置增量轮询 + 立即检查标志"。
- **calibrated 持平的形状原因**:17 个 transform_by_lua 全是 common-mode 单次调用(每请求 17 次边界),CallInto 省 34 allocs/请求,在 34770 allocs 背景里只有 -0.11%——不可见。补的 itemlua 变体(per-item lua,3000 次/请求)仍持平,逐 op 删除归因量化:3000 次 per-item Lua 调用只给 ~30ms 请求加 <1ms(<3%),pipeline 框架(38-op DAG、stub I/O、3000-item DataFrame)主导 ~97%。

## Missing Docs or Signals
- benchmark 卫生指南缺"测量路径对称性"原则:比较两个 VM 时,两侧必须跑同样的 Go↔VM 边界数据传递,否则 PureVM-vs-Embedded 会得出反向结论。这次没翻车是因为坚持了 isolated 对照,但没有显式约束。
- 复刻后端的"backend-specific 测试也要复刻"未在任何文档中沉淀。新后端 borrow/return/reuse/active/create 5 元组应有专门测试,build tag 隔离粒度需对称。
- design_doc/13_lua_vs_go_benchmark.md 已严重过时(旧 Apple M5/Go 1.24 数据、仅二分 Lua-vs-Go、无三方 + CallInto 数据)。
- `overview/project-overview.md` 中 Lua 后端章节未明确写默认 = wangshu,opt-in lua_gopher 的契约。
- "持平不劣化即可翻默认"的决策模式与 perf-evolution-roadmap 第三步"胜出才翻"的措辞不完全一致。
- calibrated fixture 没有对应的 Apple DSL 源,这次给它派生 itemlua 变体是用 Python 程序化复制 + 局部修改的(避免手写 JSON 在 metadata/契约层面漏标记),但 DSL 源缺失是独立可改善点。

## Promotion Candidates
1. `memory/decisions/perf-evolution-roadmap.md`:第三步"条件触发"已触发,补充触发记录 + 校准事实 #2 的 itemlua 第二证据点 + 修正"翻默认"门槛措辞为"裁判 fixture 不劣化 + 受影响场景显著胜出 + 全套件零回归"。
2. `overview/project-overview.md`:补 Lua 后端章节,说明默认 = wangshu、opt-in `lua_gopher` = gopher-lua、CallInto 零分配边界路径、两后端字节级对等通过同一测试套验证。
3. `guides/benchmark-hygiene.md`:新增"测量路径对称性"——两 VM 比较必须跑同样的 Go↔VM 边界数据传递。
4. `reference/operator-contract.md` 或新建 `reference/lua-backend.md`:wangshu/gopher-lua 选择契约、build tag 语义(`!lua_gopher` = wangshu 默认)、CallInto dst 复用契约(下次进 VM 前消费完)、pool 计数器 5 元组 + 双层 warm/sync.Pool 复用模型(两后端共享相同语义)。
5. `design_doc/13_lua_vs_go_benchmark.md`(非 llmdoc):重写——三方对比 + CallInto 影响 + 负载形状结论。
6. **新模板候选**:对外 issue 反馈闭环规范(英文标题、源码行级根因、可执行方向、自包含可复现包)——这次同人项目也按此规范化对外动作,作者 30 分钟内采纳。可作为 `guides/upstream-feedback-template.md` 沉淀。

## Follow-up
- recorder 阶段:把 1-4、6 推进 stable docs;5 由 design_doc 团队独立处理。
- 翻默认后续:观察 wangshu 后续版本(若再出现新边界优化),按相同对照流程评估,无需重新决策门槛。
- 测试覆盖审计:扫一遍是否还有其他 build tag 下"复刻后端但未复刻测试套"的盲点(如未来引入第三 backend 时,这条经验需先入 checklist)。
- calibrated DSL 源回填:若需频繁演化 calibrated fixture(itemlua 之外再加变体),优先补 Apple DSL 源而非继续派生 fixture。
