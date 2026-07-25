# [pine-cpp lazy OperatorInput 泄漏 skip 字段 + pine-java LuaPool.snapshotKeys coercion 派发]

## Task

- 分支 `fix/174-177-lua-pool-numeric-keys` 一次收两个相关 issue（commits `b1735fa4` / `3c2d977d` / `a4439a9c`）：
- **issue #174** — nightly diff-fuzz `seed=3040224764 divergence_000085` 抓到 Go/C++ 分歧。真实分歧点在 op_1 `reorder_shuffle_by_salt`：该算子的盐来自遍历 `metadata.common_input` 用 `input.common(field)` 拼接。本 shape 里 `common_input=['_skip_branch','level']`、`skip=['_skip_branch']`。pine-go/pine-java 在 `buildInput` 时把 skip 字段从 materialized `common` map 里剔除——算子读到 `nil`；pine-cpp 的 `OperatorInput` 是 lazy proxy，`common(field)` 直落 `frame_->common(field)` 返回原值。盐差 → order 差 → 下游 pagination 挑到不同 item 集合 → op_3 `transform_resource_lookup` 在 C++ 触发运行时错误、Go/Java 静默产出空输出。修复 `b1735fa4`：`compute_input_field_spec` 本就在算 `skip_set` 用于三桶排除，把它作为 `excluded_common` 存进 spec 并在 `OperatorInput::common` 读侧 gate；`common_keys()` 只列三个桶所以隐式已对。fixture `3c2d977d` 用专门的 shape 钉住三运行时字节级一致。
- **issue #177** — #175 的姊妹。`TransformByLua.LuaPool.snapshotKeys` 用 `k.isstring()` 过滤 baseline key，luaj 的 `isstring()` 是 Lua coercion 语义：`LuaInteger.isstring()` 无条件返回 true。脚本写 `_G[42]=...` 时数字键被以幻影字符串 `"42"` 收进 `baselineKeys`，`resetToBaseline` 走 `g.set("42", NIL)` 清的是 STRING 槽 `_G["42"]`，Lua 语义下独立的数字槽 `_G[42]` 保留、跨 borrow 泄漏。修复 `a4439a9c`：`k.type() == LuaValue.TSTRING`。**净可观测行为对所有四个 Lua host 均一致**（pine-go gopher-lua / pine-go wangshu / pine-java / pine-cpp 的 baseline snapshot 都只覆盖字符串键，wangshu godoc 明文写「限定：仅快照字符串 key」），Java 是"用错谓词得到对的行为"，本次改机制不改行为，并加 `TransformByLuaBaselineTest` 钉住 leak-then-reuse + stdlib 生存契约。

## Expected vs Actual

- Expected：本地跑 fuzz artifact `divergence_000085/` 复现 → 三引擎各跑一遍 → 找到分歧点 → 修 + 加 fixture。
- Actual：artifact 里 `info.txt` 记录的 divergence 是**运行时错误文案不一致**（C++ 在 op_3 报错、Go/Java 无错）——这是**下游首个可观测症状**而非真正的分歧点，我照着这个"错误对等"线索先追了一小时 `filter_paginate` 索引语义、`nullable_item` 校验都不对症。改用**从流水线末端逐算子截断**的策略后一步暴露 op_1 的 order 分歧，然后才走到真正的 `OperatorInput::common` skip 字段泄漏。issue #177 本身修法简单，但意外发现是 issue #175 补 fuzz 时提到过的姊妹坑（当时已归为 "另开 issue"），趁开分支同修。

## What Went Wrong

- **artifact 里的 divergence 记录会诱导追错方向**：`divergent_pair=cpp:go` 加 `rc` 不等 → 直觉认为是错误处理路径分歧。实际这是分歧在下游被 filter_paginate + resource_lookup 放大成"一侧命中错误路径、另一侧命中空集合"的**放大结果**。真正的差异（op_1 输出的 item 顺序不同）在被投影的 error/rc 视图里完全隐形，只在中间 frame 才可见。
- **lazy proxy 与 materialized input 在 skip 语义上会静默漂移**：pine-go/pine-java 在 `buildInput` 阶段构造 materialized `common` map，skip 字段是**在构造时**被剔除的；pine-cpp 的 `OperatorInput` 是 frame + spec 的 lazy proxy，`common(field)` 直读 frame。跨运行时约定"skip 字段对算子不可见"如果不同时在 proxy 的读路径写下同一排除集合，就只在 materializer 一侧生效。`compute_input_field_spec` 本地已经用 `skip_set` 做三桶的 filter，但没把它带进 spec 供读路径消费——同一个语义两处需要，只做了一处。
- **snapshotKeys 沿用 fromLua 的旧派发方式**：#175 收拾 `fromLua` 时应当已经把整个文件 grep 一遍所有 `is*()` 调用；当时 `snapshotKeys` 被判为"另开 issue"未立刻处理，导致同一文件里现在有三处 type-tag 分派（table-key check、fromLua、snapshotKeys）都做对了、`isstring()` coercion 派发已清零——收敛路径应当在 #175 时一次完成，拆两轮是分支纪律代价（可接受，因为 snapshotKeys 是行为等价的机制修正，不属 #175 的值破坏修复范围）。

## Root Cause

- **#174 根因**：pine-cpp 采用 lazy proxy 是 P2 性能优化的一部分（避免 O(N×M) eager reify），Go/Java 也已经跟进 lazy 路径。但 pine-go/pine-java 的"lazy"是**建 materialized common map** + item 侧 lazy——common 一侧仍是 eager 且天然剔除 skip；pine-cpp 是 common + item **两侧都 lazy**，跨运行时"lazy 化"的语义差异没有被显式写出来。真正的跨运行时契约是"skip / template 字段不进算子的 OperatorInput.common"，需要在**每种实现方式**里各自表达一次：materializer 侧靠构造时剔除、proxy 侧靠读路径 gate。
- **#177 根因**：把 luaj `is*()` 家族当类型谓词用——与 #175 是同一个 API 语义陷阱在 `snapshotKeys` 上的第二次登场。#175 的反思里已经指认过这个坑，`snapshotKeys` 被判 pre-existing 留作 follow-up，未在同分支一并修（当时的选择理由：单域 fix 分支纪律 + 触发面窄 + 独立可测，合理但导致本次需要另起一轮）。
- **artifact 追踪走弯路的根因**：`info.txt` 只记录了最终的 divergence 类型（rc 差异 / 错误文案差异），没有中间 frame dump——把注意力锚定在末端信号上是自然反应，需要显式的"逐算子截断向前定位"步骤来对冲。

## Missing Docs or Signals

- **fuzz artifact triage 步骤没有 playbook**：`guides/ci-quality-baseline.md` 的 differential-fuzz 节记录了怎么跑 fuzz、怎么加维度，但没写"artifact 到手怎么定位到真正分歧点"。当下游算子把差异放大成不同的错误路径时，`info.txt` 的 rc/文案分歧记录会指错方向。
- **lazy proxy vs materialized 的对等规则未文档化**：`architecture/dag-engine.md` 第 482 行说三运行时都用 lazy proxy，`reference/operator-contract.md` 第 367-375 行讲了 pine-cpp OperatorInput 是 lazy proxy 避免 eager reify——但**"pine-go/pine-java 的 common 侧其实是 eager materialize、pine-cpp 是 common+item 都 lazy"**这个实现差异没写下来，导致后续给 spec 加字段（skip / template / defaults）时容易只在一侧生效。这是"跨运行时同一契约，实现方式不同 → 契约的每种实现里都要各自表达一次"的通用模式，没有专门的 guide 讲这个。
- **Lua pool baseline "只覆盖字符串键"的跨运行时契约缺少集中记载**：wangshu godoc 明文写了「限定：仅快照字符串 key」，pine-go gopher-lua / pine-java / pine-cpp 都在同一契约下（数字/表/函数键的 `_G[k]` 跨 borrow 会泄漏，"非典型、实际场景不存在"是四家共识），但 `reference/lua-backend.md` 只提到 baseline 重置契约、没写"限定字符串键"这一负空间，也没跨到 pine-java/pine-cpp 侧对齐。契约缺失让 Java 用错的谓词达到对的行为长期无人发现。

## Promotion Candidates

- 进 `guides/ci-quality-baseline.md`（differential-fuzz 节）：加 **fuzz artifact triage playbook**——(1) 从 `divergence_NNNNNN/info.txt` 读 divergent_pair + rc；(2) 三引擎各跑一遍原案；(3) 若末端错误/rc 分歧与直觉不符，**从流水线末端向前逐算子截断**定位真正的 frame 分歧点，不要被下游放大的错误路径误导。本次省下的一小时可复用。
- 进 `architecture/dag-engine.md` 或 `reference/operator-contract.md`（OperatorInput 投影层）：**lazy proxy vs materialized 的实现差异要显式写下**——pine-go/pine-java 的 `common` 侧是 eager materialize（构造时剔除 skip/template），`item` 侧是 lazy；pine-cpp 是 `common`+`item` **两侧都 lazy**。跨运行时契约"skip / template / 排除字段不进算子可见的 OperatorInput"需要**在每种实现方式里各自表达一次**：materializer 走构造剔除、proxy 走读路径 gate（`excluded_common` set）。给 spec 加新的排除维度时逐一核对每种实现。
- 进 `reference/lua-backend.md`（新增 pool baseline 契约节或跨到 pine-java 侧）：**Lua pool baseline 快照跨四运行时统一契约是"仅覆盖字符串键"**——wangshu godoc 有权威表述、pine-go gopher-lua / pine-java / pine-cpp 净行为一致，`_G[42]`/`_G[true]` 类数字/布尔全局键跨 borrow 泄漏是**已文档化的负空间**，非缺陷。审计任一实现时先按此契约核对，机制层面（谓词选择）只要正确性一致即可，行为无需 byte-exact。
- 仅留 memory：#174 的 `excluded_common` set 需要 `skip ∪ common_input_skip ∪ common_input_template` 三桶并集（`compute_input_field_spec` 已经在算这个，只是原来没存进 spec）；#177 修改本身行为中立，是把"用错谓词得到对的行为"改成"用对谓词得到对的行为"，重要的是消除同一文件里最后一处 coercion 派发——检索到本篇即可。

## Follow-up

- 由 recorder 把前三条 promotion 写进对应稳定文档并同步 `index.md`；lazy proxy 实现差异那条建议放在 `architecture/dag-engine.md` "BuildInput 语义"节，与现有 lazy proxy 描述并列一段"实现差异清单"。
- 下次 nightly artifact 复现走弯路超过 30 分钟时，条件反射式切"从末端逐算子截断"策略，不要继续深挖末端错误路径。
- pine-java `TransformByLua.java` 的 `is*()` 派发点现已全部为 type-tag 派发（table-key check / fromLua / snapshotKeys），三处闭环；下次再触碰该文件时不需要额外扫。若 pine-java 后续加新 Lua bridge 代码，仍需 grep 全部 `is*()` 调用逐个判定 coercion-or-tag（与 #175 反思同款要求）。
