# Nightly diff-fuzz #210：复合值里的 NaN/±Inf 绕过写入校验

日期：2026-09-26。nightly differential-fuzz 报 1 个 go vs java 分歧（2026-09-13，column 分层 1 失败）。一次任务修完：三运行时写入校验递归扫描复合值、pine-go HTTP 响应改为先编码再写状态码、两条 cross-validate error fixture（行存 + 列存）。

## 结论先行

| | pine-go | pine-java | pine-cpp |
|---|---|---|---|
| Lua 返回标量 `inf` | 写入时报错 | 同左，字节一致 | 同左，字节一致 |
| Lua 返回 `{inf, inf}`（修前） | 序列化失败 `json: unsupported value: +Inf`，CLI rc=1 | 输出 `["Infinity","Infinity"]` | 输出 `[inf,inf]`（非法 JSON） |
| 修后 | 三方都在写入时报 `item[i] write: field "f": NaN/Inf is not a valid JSON value` | | |

触发链：fuzz 给 `item_score` 配 `item_defaults = 1.7976931348623157e+308`（float64 最大值，来自 `differential-fuzz.py:74` 的边界值池），Lua `{item_score * 2, …}` 溢出成 `+Inf`。三方写入校验（Go `validateValue`、Java `checkValue`、C++ `validate_value`）对 `[]any` / `map` / `List` / `Map` / array / object 直接放行，所以复合值里的非有限数一路带到序列化，三方在序列化层的行为本来就各不相同（`reference/number-formatting-parity.md`「非有限值」节）。

## 过程里真正起作用的动作

**1. 先把 artifact 精简到一个算子。** 原 case 有 `data_parallel: 2`、skip 控制、`recall_resource` 和列存。去掉全部无关维度后（单算子、2 个 item、行存与列存各一份），分歧照样出现，而且标量 / 数组 / map 三种返回值的结果一眼分开。这张 3×3 的表（返回形状 × 运行时）直接给出了根因位置：标量被拦、复合值不被拦。

**2. 修前先写清楚「写入校验」到底是哪一刻。** 用户追问「写入校验具体是什么时刻」。答案是算子输出合并进 frame 的 `ApplyOutput` / `applyOutput` / `apply_output`，由调度器在每个算子执行完后调用；不是 `SetItem` 调用时，也不是序列化时。把这个时刻说清楚后，范围也随之确定：要改的是这一处的三类写入（common、逐元素 item、新增 item），批量列写入只装 `float64` 不可能带复合值。`Frame.SetCommon` 绕过 `ApplyOutput`，但它只装请求数据，属于请求解析层那条另立的分歧。

**3. 请求来源实测了一遍，没有凭推理结案。** doc-gaps 的「非有限值进入复合 shuffle salt」条目原本写「只能由 Lua `return {0/0, x}` 产生」。修完后这个来源堵住了，但条目不能直接关：实测 `{"k": [1e400, 2]}` 发给 shuffle，Java 仍把它解析成 `[Infinity, 2]` 并给出与 Go / C++ 不同的结果（后两者在解析阶段拒绝请求）。条目改为「Lua 来源已堵、请求来源随请求解析层那条走」。

## 设计取舍

- **递归深度上限 1000，超限只跳过那一棵子树**。首版是「超限就放弃整次扫描」，本意是防自引用 map 的指数爆炸；本地盲审（r1-first）实测指出它和 Go 的随机 map 遍历叠加后，`{deep: <1001 层>, bad: inf}` 这种值在 pine-go 内有时被拒、有时放行（20 次里 2 次写入报错、18 次序列化失败）——单引擎非确定性，只用 Lua 就能触发。改为：深度 ≥ 1000 的复合值不下钻、兄弟照常检查（结论只取决于值），自引用起初改由「祖先路径上的同一对象」判环跳过。第二轮增量盲审（r2-incr）又实测出两处挂起：(1) 祖先判环只挡直接自环，按路径深度优先仍会枚举全部简单路径——k 个 map 排成环、每个两次指向下一个，路径数 2^k，Go 与 Java 在 k=40 都跑不完，而首版「整次放弃」恰好顺带防住了这种爆炸；(2) pine-go 新增的指针/接口解包循环不计深度也不判环，`var x any; x = &x` 会永久死循环。最终做法：按对象身份（Go：map 指针 / slice 数据指针加长度 / 指针，外加 `reflect.Type`，理由见下文 r3-incr 一段；Java：`IdentityHashMap`）记下每个复合值被扫到时的**最浅深度**，以相同或更深的深度再次遇到就跳过——后代已经用至少同样多的深度预算检查过，不可能再找到新东西。这样任意共享或成环结构都不再做路径枚举（每个复合值最多按遇到它的不同深度各扫一次，不是严格线性——第三轮盲审 r3-incr 指出过这句的原始写法过强），结论仍只取决于值；从更浅深度再遇到时会重扫（`TestValidateValueSharedSubvalueRescannedFromShallowerDepth` 锁定），保证深度语义与纯树遍历一致。Go 的记录前 16 项放在栈上的内联数组里，常见的小 table 不分配；Java 第一个复合值记在字段里、第二个起才分配 `IdentityHashMap`（最初「不含嵌套的叶子不记录」的省分配写法让被大量引用的共享叶子每次都整片重扫，r3-incr 实测 459 ms，已改成叶子也记录）。r3-incr 还查出 pine-go 的身份键缺类型：`&arr` 与 `&arr[0]`、`s` 与 `s[0][:1]` 地址和长度都相同，先扫窄的那个后宽的被当成已扫过，放进 map 后结论随遍历顺序变化；身份键加上 `reflect.Type` 后修掉。第四轮盲审 r4-incr 又核对了 Go 1.27 的 `encoding/json/encode.go`：它判环时只有指针的 key 带类型（`v.Interface()`），map 与 slice 的 key 只是地址（加长度），所以 `s` 与 `s[0][:1]` 在它那里同样同 key——对「判环」无害（撞 key 只会多报环，而同址同长的两个 slice 引用本来就会被逐路径序列化），但对「已扫过就跳过」这种去重有害。最初注释与本文都写成「与 `encoding/json` 判环口径一致、本来就含类型」，是没读源码就下的断言，r4-incr 指出后改正。C++ 的 `Variant` 是值树不需要。三方用同一组边界测试锁定（1000 层内的 NaN 被拒、1001 层的不被检查、超深兄弟不掩盖浅层 NaN），Go / Java 另锁环形共享图与自指指针在 5 秒内结束。
- **同一次审查的两条小问题**：pine-go 的 reflect 分支原来只认内建 `float64`，命名浮点类型（`type score float64`）、指针元素、数组都漏检，现在按 `encoding/json` 的方式解指针/接口、按 kind 判浮点；新增的稳定文档写了「三运行时」，按 `must/conventions.md`「禁止硬编码定量描述」改为列名字。
- **三方各抽一份共享实现**：Go 两种 frame 本来就共用 `validateValue`；Java 新建 `FrameValues`（原来 `DataFrame` / `ColumnFrame` 各有一份相同的 `checkValue`），C++ 新建 `src/dataframe/frame_values.hpp`（原来 `row_frame.cpp` / `column_frame.cpp` 各一份）。
- **pine-go `writeJSON` 先编码到 buffer**。原实现先 `WriteHeader(status)` 再流式编码，编码失败时客户端收到的是「200 + 空 body」。改为先编码、失败则回 500 + 错误 body；成功路径用同一个 `json.Encoder` 写 buffer，字节与流式输出完全相同（`TestWriteJSON_BufferedBytesMatchStreaming` 锁定 HTML 转义与结尾换行）。#210 修完后 `/execute` 已不会再走到编码失败，这一改是防御性的，但同一个 `writeJSON` 也服务所有其他端点。`examples/multi-pipeline` 有一份一样的 `writeJSON`，按「示例受全部生产契约约束」一起改。

## 性能

microbench `BenchmarkApplyOutput_CompositeItemWrites`（新增，每个 item 写一个 2 元素数组 + 一个 2 键 map，1000 item）：行存 +77%、列存 +109%，全部是遍历 map/slice 本身的成本，零新增分配。只写标量的 `ItemWrites` / `Additions` 在 +1%～+3% 以内。把元素判断内联进循环（`nonFiniteElem`）没有带来可测的改善，热点在 map 迭代器，不在函数调用。

性能决策以 calibrated fixture 为准（`guides/benchmark-hygiene.md`）：`BenchmarkCalibrated` 三个变体 6 轮 A/B（同机、交替跑），全部 `~`（p=0.31），分配数与字节数不变。结论：复合值扫描在生产 proxy 上不可见，microbench 的倍数只说明「写复合值的纯写入路径」这一段变慢了。

## 可复用的判据

- **身份去重的键要与「这个引用覆盖哪些数据」一一对应。** 指针地址或 slice 数据指针加长度不足以区分不同引用：同一地址可以是整个数组或它的第一个元素，所以键里要带类型。判环用的键可以更粗（撞键只会多报环），去重用的键不能——不要因为「某个标准库也这么判」就照搬它的键，它解决的问题可能不是你的问题；引用标准库行为前先读一遍它的源码。
- **去掉一道防线前，先列出它顺带防住的东西。** 「超限整次放弃」被当成只是为了截断深度，删掉它时没意识到它同时是对路径爆炸的唯一防护。删改早退、上限、预算这类逻辑时，要对照最坏输入（环、共享子结构、自指指针）逐项确认复杂度。
- **截断/提前返回的结论不能依赖遍历顺序。** 「遇到第一个非 clean 就返回」在只有一种非 clean 结果时没问题，一旦有两种（找到 / 太深），map 的遍历顺序就决定了结论。凡是在无序容器上做带早退的遍历，都要确认多种早退原因之间的优先级与顺序无关。

- **写入校验对「值的形状」要和序列化器看齐。** 序列化器会下钻复合值，校验也必须下钻；只校验顶层等于把一半的输入空间放给了序列化层，而序列化层恰好是三方最不一致的地方。
- **「只能由 X 产生」这类来源断言要附带可观察性判断。** 原 doc-gap 写「fuzz 的 Lua 脚本不产生 NaN/Inf」，没考虑到默认值 + 算术溢出同样能产生。凡是「这个值只能从哪里来」的论断，都应列出所有能写该值的入口再下结论（与 `must/conventions.md`「跨运行时缺陷动手前必须实测受影响面」同族）。
