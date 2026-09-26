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

- **递归深度上限 1000，超限就放弃整个扫描**。值来自 Lua 或 JSON 时是无环的，但 Go 与 Java 的自定义算子可以交出自引用 map。按层截断在「一个 map 有两个 key 指向自己」时是指数级的，所以超限返回 `scanTooDeep` 并立刻终止整次扫描（`TestValidateValueSelfReferencingMapTerminates` 锁定）。上限取 Go `encoding/json` 的 `startDetectingCyclesAfter`，三方一致，保证同一深度上的 NaN 三方同时拒绝或同时放过。C++ 的 `Variant` 是值树，不可能自引用，但用同一个上限保持对等。
- **三方各抽一份共享实现**：Go 两种 frame 本来就共用 `validateValue`；Java 新建 `FrameValues`（原来 `DataFrame` / `ColumnFrame` 各有一份相同的 `checkValue`），C++ 新建 `src/dataframe/frame_values.hpp`（原来 `row_frame.cpp` / `column_frame.cpp` 各一份）。
- **pine-go `writeJSON` 先编码到 buffer**。原实现先 `WriteHeader(status)` 再流式编码，编码失败时客户端收到的是「200 + 空 body」。改为先编码、失败则回 500 + 错误 body；成功路径用同一个 `json.Encoder` 写 buffer，字节与流式输出完全相同（`TestWriteJSON_BufferedBytesMatchStreaming` 锁定 HTML 转义与结尾换行）。#210 修完后 `/execute` 已不会再走到编码失败，这一改是防御性的，但同一个 `writeJSON` 也服务所有其他端点。`examples/multi-pipeline` 有一份一样的 `writeJSON`，按「示例受全部生产契约约束」一起改。

## 性能

microbench `BenchmarkApplyOutput_CompositeItemWrites`（新增，每个 item 写一个 2 元素数组 + 一个 2 键 map，1000 item）：行存 +77%、列存 +109%，全部是遍历 map/slice 本身的成本，零新增分配。只写标量的 `ItemWrites` / `Additions` 在 +1%～+3% 以内。把元素判断内联进循环（`nonFiniteElem`）没有带来可测的改善，热点在 map 迭代器，不在函数调用。

性能决策以 calibrated fixture 为准（`guides/benchmark-hygiene.md`）：`BenchmarkCalibrated` 三个变体 6 轮 A/B（同机、交替跑），全部 `~`（p=0.31），分配数与字节数不变。结论：复合值扫描在生产 proxy 上不可见，microbench 的倍数只说明「写复合值的纯写入路径」这一段变慢了。

## 可复用的判据

- **写入校验对「值的形状」要和序列化器看齐。** 序列化器会下钻复合值，校验也必须下钻；只校验顶层等于把一半的输入空间放给了序列化层，而序列化层恰好是三方最不一致的地方。
- **「只能由 X 产生」这类来源断言要附带可观察性判断。** 原 doc-gap 写「fuzz 的 Lua 脚本不产生 NaN/Inf」，没考虑到默认值 + 算术溢出同样能产生。凡是「这个值只能从哪里来」的论断，都应列出所有能写该值的入口再下结论（与 `must/conventions.md`「跨运行时缺陷动手前必须实测受影响面」同族）。
