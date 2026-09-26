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

- **递归深度上限 1000，超限只跳过那一棵子树**。首版是「超限就放弃整次扫描」，本意是防自引用 map 的指数爆炸；本地盲审（r1-first）实测指出它和 Go 的随机 map 遍历叠加后，`{deep: <1001 层>, bad: inf}` 这种值在 pine-go 内有时被拒、有时放行（20 次里 2 次写入报错、18 次序列化失败）——单引擎非确定性，只用 Lua 就能触发。改为：深度 ≥ 1000 的复合值不下钻、兄弟照常检查（结论只取决于值），自引用起初改由「祖先路径上的同一对象」判环跳过。第二轮增量盲审（r2-incr）又实测出两处挂起：(1) 祖先判环只挡直接自环，按路径深度优先仍会枚举全部简单路径——k 个 map 排成环、每个两次指向下一个，路径数 2^k，Go 与 Java 在 k=40 都跑不完，而首版「整次放弃」恰好顺带防住了这种爆炸；(2) pine-go 新增的指针/接口解包循环不计深度也不判环，`var x any; x = &x` 会永久死循环。最终做法：按对象身份（Go：map 指针 / slice 数据指针加长度 / 指针，外加 `reflect.Type`，理由见下文 r3-incr 一段；Java：`IdentityHashMap`）记下每个复合值被扫到时的**最浅深度**，以相同或更深的深度再次遇到就跳过——后代已经用至少同样多的深度预算检查过，不可能再找到新东西。这样任意共享或成环结构都不再做路径枚举（每个复合值最多按遇到它的不同深度各扫一次，不是严格线性——第三轮盲审 r3-incr 指出过这句的原始写法过强），结论仍只取决于值；从更浅深度再遇到时会重扫（`TestValidateValueSharedSubvalueRescannedFromShallowerDepth` 锁定），保证深度语义与纯树遍历一致。Go 的记录前 16 项放在栈上的内联数组里，常见的小 table 不分配；Java 第一个复合值记在字段里、第二个起才分配 `IdentityHashMap`（最初「不含嵌套的叶子不记录」的省分配写法让被大量引用的共享叶子每次都整片重扫，r3-incr 实测 459 ms，已改成叶子也记录）。r3-incr 还查出 pine-go 的身份键缺类型：`&arr` 与 `&arr[0]`、`s` 与 `s[0][:1]` 地址和长度都相同，先扫窄的那个后宽的被当成已扫过，放进 map 后结论随遍历顺序变化；身份键加上 `reflect.Type` 后修掉。注释与本文最初写成「与 `encoding/json` 判环口径一致、本来就含类型」，之后两轮盲审先后指出这句站不住：r4-incr 读了 `encoding/json/encode.go`，发现那里 map / slice 的判环 key 不带类型；r5-incr 又指出本机 Go 1.27 默认启用 `GOEXPERIMENT=jsonv2`，`encode.go` 根本不参与编译，实际生效的 v2 实现 key 是 `{type, ptr, len}`，带类型——而 `go.mod` 声明的 1.26.2（CI 用的版本）走的又是 v1。也就是说「标准库怎么判环」随 Go 版本与构建实验开关而变，拿它当依据的描述每次都会过期。最终注释与文档都不再引用标准库的判环细节，只说明本扫描的键为什么必须带类型。另外 r5-incr 实测：v1 的粗 key 并非「对判环无害」，一个无环的值（同址同长、类型不同的两个引用，外层嵌套超过 1000 层）在 `nojsonv2` 下会被误报 `encountered a cycle`。C++ 的 `Variant` 是值树不需要。三方用同一组边界测试锁定（1000 层内的 NaN 被拒、1001 层的不被检查、超深兄弟不掩盖浅层 NaN），Go / Java 另锁环形共享图与自指指针在 5 秒内结束。
- **同一次审查的两条小问题**：pine-go 的 reflect 分支原来只认内建 `float64`，命名浮点类型（`type score float64`）、指针元素、数组都漏检，现在按 `encoding/json` 的方式解指针/接口、按 kind 判浮点；新增的稳定文档写了「三运行时」，按 `must/conventions.md`「禁止硬编码定量描述」改为列名字。
- **终审阶段的两轮小问题**：r7-final 指出 `must/conventions.md` 仍引用已删掉的 `ColumnFrame.checkValue`、两处测试注释还写着「ancestor check」；r8-final 指出 pine-go 的 reflect 扫描不进入 struct（`[]any{struct{X float64}{+Inf}}` 写入放行、`json.Marshal` 才失败）。后者按 `encoding/json` 的字段规则补了 struct 分支：导出字段与嵌入字段（嵌入的未导出类型也要进，它的导出字段会被提升），跳过标签恰为 `-` 的字段（`-,` 是字段名叫 `-`，要扫）；两条「会编码」的规则在 json v1（`GOEXPERIMENT=nojsonv2`）与 v2 下实测一致。只有自定义 Go 算子能构造出 struct；C++ 的 `Variant` 没有 struct。
- **第三轮终审（r9-final）又翻出两条，方向相反**：(1) r8 补的 struct 分支和 r1 补的 reflect 分支把扫描范围做得比 `encoding/json` **宽**——自带 `MarshalJSON` / `MarshalText` 的类型（编码器调方法，可能把 Inf 编成字符串）、嵌入的未导出非 struct（编码器忽略）、被外层同名字段遮住的提升字段（编码器丢弃），这些值以前能写入能编码，修完反而在写入时被拒，是回退。修法：带 marshaler 的类型（含指针接收者）不下钻、嵌入字段只放行 struct / struct 指针；字段遮蔽规则不复刻，注释写明是偏保守的误拒。(2) pine-java 只下钻 `Map` / `List`，嵌套的 `double[]` / `Object[]` 被当成「非数」放行，而响应 mapper 逐元素序列化，输出 `[[1,"Infinity"]]`；此前 reflection 写的「POJO 本来就走 `unsupported value type`」只对顶层值成立。现在嵌套 Java 数组按 list 扫描（`Object[]` 走身份记录，自指数组同样终止），嵌套 POJO 仍不检查。
- **第四轮终审（r10-final）证明上一条只修了「点名的那一种」**：r9 指出嵌套数组，修的时候只加了数组，没有按「Jackson 会展开什么」穷举；r10 实测嵌套的 `LinkedHashSet` / `ArrayDeque` / `Map.values()` / `AtomicReference` 照样把 NaN 带到 `[["NaN"]]`（重要级，正是 #210 在 pine-java 上的原症状）。这次先写探针让 `GoFormat.marshalJson` 逐个序列化候选类型、看哪些被展开，再据此定清单：任意 `Collection`、`Map.Entry`、`AtomicReference`（按内容）、`DoubleAdder` / `DoubleAccumulator`（按数字）；`Iterator` 与非 `Collection` 的 `Iterable` 也会被展开，但遍历可能消耗它或执行任意代码，刻意不扫，写进注释。同一轮的小问题：pine-go reflect 分支对 `[]float32` 这类带类型切片逐元素 `Interface()` 装箱，128 维 embedding 每次检查 128 次分配，而本文「零新增分配」只在 `[]any` / `map[string]any` 上测过；改为元素是无 marshaler 的浮点时直接 `Float()` 读取（map 复用一个元素槽），`TestValidateValueTypedFloatSlicesDoNotAllocatePerElement` 与 `BenchmarkApplyOutput_TypedFloatSliceItemWrites` 锁定。
- **r11-incr 查出注释里的前提是错的**：r10 修复的 javadoc 写「BigDecimal / BigInteger 写出时总是有限」，但 `GoFormat.wrap(v, true)` 在 payload 路径上把所有非 Double/Float 的 Number 转成 double，`new BigDecimal("1e400")` 写出为 `"Infinity"`。判断改为一律取 `doubleValue()`，也就是按写出时的值判断，不再逐类列举。同一轮还指出 `number-formatting-parity.md` 里「唯一入口」「只扫描嵌套 Java 数组」两处没跟着 r9/r10 更新，和另两份文档互相矛盾——**改一条跨文档的事实时，先 `grep` 出全部陈述处一起改**。r12-incr 接着指出新注释「每个 Number 写出时都会转成 double」又只对了一半：`GoFormat.wrap` 只进 Map / List，Set / `Map.Entry` / 数组 / `AtomicReference` 里的 `BigDecimal("1e400")` 写成 `1E+400`。保留与容器无关的统一规则（按 `doubleValue()` 判断），把在这些容器里的拒绝写明为刻意的保守选择并用测试锁定，没有去改 `wrap` 的下钻范围——那会改变响应字节，超出本任务。教训同上：给「写出时会怎样」下结论前，按容器 × 值类型的矩阵跑一遍 `GoFormat.marshalJson`，不要只测 List。
- **三方各抽一份共享实现**：Go 两种 frame 本来就共用 `validateValue`；Java 新建 `FrameValues`（原来 `DataFrame` / `ColumnFrame` 各有一份相同的 `checkValue`），C++ 新建 `src/dataframe/frame_values.hpp`（原来 `row_frame.cpp` / `column_frame.cpp` 各一份）。
- **pine-go `writeJSON` 先编码到 buffer**。原实现先 `WriteHeader(status)` 再流式编码，编码失败时客户端收到的是「200 + 空 body」。改为先编码、失败则回 500 + 错误 body；成功路径用同一个 `json.Encoder` 写 buffer，字节与流式输出完全相同（`TestWriteJSON_BufferedBytesMatchStreaming` 锁定 HTML 转义与结尾换行）。#210 修完后 `/execute` 已不会再走到编码失败，这一改是防御性的，但同一个 `writeJSON` 也服务所有其他端点。`examples/multi-pipeline` 有一份一样的 `writeJSON`，按「示例受全部生产契约约束」一起改。

## 性能

microbench `BenchmarkApplyOutput_CompositeItemWrites`（新增，每个 item 写一个 2 元素数组 + 一个 2 键 map，1000 item）：行存 +77%、列存 +109%，全部是遍历 map/slice 本身的成本，零新增分配。「零新增分配」最初只在 `[]any` / `map[string]any` 上测过，带类型的 `[]float32` 其实逐元素装箱，r10-final 查出后修掉；`BenchmarkApplyOutput_TypedFloatSliceItemWrites`（每 item 一个 128 维 `[]float32`）现在行存 0 allocs/op。只写标量的 `ItemWrites` / `Additions` 在 +1%～+3% 以内。把元素判断内联进循环（`nonFiniteElem`）没有带来可测的改善，热点在 map 迭代器，不在函数调用。

性能决策以 calibrated fixture 为准（`guides/benchmark-hygiene.md`）：`BenchmarkCalibrated` 三个变体 6 轮 A/B（同机、交替跑），全部 `~`（p=0.31），分配数与字节数不变。结论：复合值扫描在生产 proxy 上不可见，microbench 的倍数只说明「写复合值的纯写入路径」这一段变慢了。

## 可复用的判据

- **身份去重的键要与「这个引用覆盖哪些数据」一一对应。** 指针地址或 slice 数据指针加长度不足以区分不同引用：同一地址可以是整个数组或它的第一个元素，所以键里要带类型。不要因为「某个标准库也这么判」就照搬它的键：它解决的问题可能不是你的问题，而且它本身也可能有缺陷。引用标准库行为作为依据时，先确认**实际编译进构建的是哪份源码**（`go list -f '{{.GoFiles}}' <pkg>`，Go 的 `GOEXPERIMENT` 与 build tag 会换掉整份实现），并写明适用的版本；能不依赖它时就别依赖。
- **去掉一道防线前，先列出它顺带防住的东西。** 「超限整次放弃」被当成只是为了截断深度，删掉它时没意识到它同时是对路径爆炸的唯一防护。删改早退、上限、预算这类逻辑时，要对照最坏输入（环、共享子结构、自指指针）逐项确认复杂度。
- **截断/提前返回的结论不能依赖遍历顺序。** 「遇到第一个非 clean 就返回」在只有一种非 clean 结果时没问题，一旦有两种（找到 / 太深），map 的遍历顺序就决定了结论。凡是在无序容器上做带早退的遍历，都要确认多种早退原因之间的优先级与顺序无关。

- **写入校验对「值的形状」要和序列化器看齐——两个方向都要。** 序列化器会下钻复合值，校验也必须下钻；只校验顶层等于把一半的输入空间放给了序列化层，而序列化层恰好是三方最不一致的地方。反过来，校验下钻到序列化器不会看的地方（有自定义 marshaler 的类型、编码器忽略的字段）就会拒绝合法值。补一个分支时，要同时列出「序列化器会编码而我没查」和「我查了而序列化器不编码」两张清单，r8 / r9 两轮终审恰好各踩一边。清单要靠探针对照序列化器实测得出，不要只补审查点名的那一个类型（r9 → r10 的教训）；「零分配」这类性能结论也要按值的形状分别测。
- **「只能由 X 产生」这类来源断言要附带可观察性判断。** 原 doc-gap 写「fuzz 的 Lua 脚本不产生 NaN/Inf」，没考虑到默认值 + 算术溢出同样能产生。凡是「这个值只能从哪里来」的论断，都应列出所有能写该值的入口再下结论（与 `must/conventions.md`「跨运行时缺陷动手前必须实测受影响面」同族）。
