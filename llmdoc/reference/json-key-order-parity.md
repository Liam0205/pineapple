# Go JSON object key 顺序对等

本文件记录复刻 Go `encoding/json` 的 object key **顺序**时必须遵守的两条规则。
两条都在 issue #183 期间各让一版实现失败过：第一条写错会弄坏响应外层，第二条写错
只是把分歧点从 ASCII key 挪到 emoji key。

与 `llmdoc/reference/number-formatting-parity.md` 的分工：那篇管**数字怎么拼**，
本篇管**key 按什么顺序出**。两者都是 `/execute` 字节级契约的一部分，落点是同一个
Jackson mapper，但规则互不相干。

参照的实现落点：

- pine-java：`pine-java/src/main/java/page/liam/pine/GoFormat.java`（`SortedByUtf8` / `sorted` / `wrapPayload` / `compareUtf8`，以及 `createGoCompatMapper` 里的序列化器注册）
- 调用点：`pine-java/src/main/java/page/liam/pine/PineServer.java`、`pine-java/src/main/java/page/liam/pine/RunCli.java`（两个入口共用同一 comparator）
- pine-cpp：`pine-cpp/src/config/json_writer.cpp`（`std::sort`，无需改动，见下）
- Go 侧规则来源：`encoding/json` 对 map 与 struct 的两条不同路径

## 规则一：Go 对 map 排序、对 struct 保持声明顺序

`encoding/json` 不是「一律排序」：

- `map[string]any` — 按 key 排序输出
- struct — 按**字段声明顺序**输出，不排序

pine-go 的 `/execute` 响应外层（envelope）是 struct
`executeResponse{Common, Items, Warnings, Trace, Error}`
（`pine-go/pkg/server/server.go:655`），里面的 common / items payload 才是 map。
所以 envelope 的 key 顺序固定为声明顺序，只有 payload 内部排序。实测：

```go
{"common":{"a":2,"z":1},"items":[1],"error":"boom"}
```

`error` 声明在 `items` 之后，所以它在后面；`common` 内部的 `a`/`z` 才排序。

**Java 里 Map 与 struct 没有类型层面的区分，所以这条二分必须显式建模。**
`GoFormat.SortedByUtf8` 是这个显式标记：payload 用 `GoFormat.sorted` /
`GoFormat.wrapPayload` 包起来，envelope 保持普通 `LinkedHashMap` 按 put 顺序输出。
`wrapPayload` 递归下钻（Go 在每一层都排，list 里嵌的 map 也要排）。

直觉上「给 `Map.class` 注册一个排序序列化器」是错的：issue #183 第一版就是这么写的，
所有 Map 都排序，把 `error` 排到了 `items` 前面，cross-validate 从 55 掉到 54，
失败的是 `fixtures/server_byte_exact/02_partial_error_keeps_partial_result.json`。

## 规则二：排序键是 UTF-8 字节序，不是 `String.compareTo`

Go 比的是 Go string，即 **UTF-8 字节序**。Java `String.compareTo` 比 UTF-16
code unit，两者在 BMP 之外相反：

```
U+FFFD   UTF-16 fffd        UTF-8 ef bf bd
U+10000  UTF-16 d800 dc00   UTF-8 f0 90 80 80
```

`compareTo(U+FFFD, U+10000)` 是 `+1`，UTF-8 字节序是 `-1`。

推论：**Jackson 的 `SerializationFeature.ORDER_MAP_ENTRIES_BY_KEYS` 不能用**——
它按 map 的 natural ordering 排，即 `String.compareTo`。同理任何基于 `TreeMap`
的排序 helper 也不能用。用它们只是把分歧点从 ASCII key 挪到 emoji key。

正确实现是 `GoFormat.compareUtf8`：两串都在 ASCII 区间时两种顺序一致，走快路径
不建字节数组；一旦遇到非 ASCII 字符就落到 `Arrays.compareUnsigned` 比 UTF-8 字节。

## pine-cpp：走 Variant writer 的路径天然满足，手写 JSON 的路径不满足

`pine-cpp/src/config/json_writer.cpp:33,54` 与 `json_writer.hpp:170`（递归那处，
「每一层都排」的实现点）的 `std::sort` 配 `std::string` 的 `<` 就是字节序，天然与 Go
一致，含 BMP 之外的 key。`pine-cpp/tests/test_json.cpp` 的 "nested objects all sort
keys (L5)" 用例钉着这条。**只要响应是由 `Variant` 经 writer 序列化出来的，就不用管。**

但 `/stats` 不走 writer：`server.cpp` 的 `handle_stats` 用字符串拼接手写 JSON，于是
key 顺序就是源码里的书写顺序。issue #183 期间实测发现它顶层输出
`operators, scheduler, server, http, resources`，而 Go 排序后是
`http, operators, resources, scheduler, server`；`server` 子树同样是书写顺序而 Go 排序。
两处都已改为收集进 `std::map` 再按迭代序输出。

教训：**「这个运行时天然满足」这句话只对某条代码路径成立，不对整个运行时成立。**
issue #183 的标题与最初的任务描述都说只有 pine-java 错——对 `/execute` 是对的，对
`/stats` 不对，而这一点是加了校验检查之后才暴露的，不是读代码读出来的。

改动 C++ writer 时注意不要把 `std::sort` 换成任何按 code point / 宽字符比较的写法；
新增手写 JSON 的端点时，要么走 writer，要么自己保证 key 按字节序输出。

## 覆盖面：哪些响应位置已对齐

| 位置 | Go 类型 | 规则 | 已对齐 |
|---|---|---|---|
| `/execute` envelope | `executeResponse` struct | 保持声明顺序 | 是 |
| `/execute` `common` / `items` | `map[string]any` | 排序 | 是 |
| `/execute` `trace[]` 条目 | `traceEntry` struct | 保持声明顺序 | 是 |
| `/execute` `trace[].input_snapshot` / `output_snapshot` | `map[string]any` | 排序 | 是 |
| `output_snapshot.item_writes` | `map[int]map[string]any` | 按 key 的**字符串**形式排序（10 在 1 与 2 之间） | 是 |
| `/stats` 顶层 | `map[string]any` | 排序 | 是 |
| `/stats` `scheduler` | `SchedulerStatsSnapshot` struct | 保持声明顺序 | 是 |
| `/stats` `server` / `http` / `resources` / `operator_detail` | map | 排序 | 是 |

`/stats` 同一棵树里两条规则都出现，所以 Java 侧是**逐分支**按 Go 类型包装的，不是
整棵树一刀切——`GoFormat.sortedShallow` 就是为此存在（只排自己这一层，不下降）。
一刀切会把 `scheduler` 这个 struct 也排掉。

## 哪条通道能钉住它

- `scripts/cross-validate/09-raw-byte.sh` — 现在字节不等即失败（归一化回落已删），能钉住 key 顺序。
  但它走 CLI，**看不到 trace**（CLI 输出只有 common/items），也看不到 `/stats`
- `scripts/cross-validate/06-server-http.sh` — 走 HTTP，是唯一能钉住 trace 快照与 `/stats`
  key 顺序的通道。它原先打印 `sorted(trace[0].keys())`，把待测维度本身排掉了；现已改为用
  `object_pairs_hook` 比 key 序列与嵌套结构，并给算子开 `debug`、把请求补到 12 个 item 且
  加非排序的额外 common key —— 缺任何一项检查都会对着改坏的实现继续绿（单 key 的快照没有
  顺序可错，<10 个 item 也测不出 int key 的字符串排序）
- `scripts/cross-validate/14-byte-exact-execute.sh` — 直接 `==` 响应体
- `scripts/differential-fuzz.py` 的 `key_order_signature()` — 用 `object_pairs_hook` 从原文读 key 顺序单独比对，绕开 `normalize_json` 的 `sort_keys=True`

细节见 `llmdoc/guides/ci-quality-baseline.md`「校验通道能钉住的属性」节。

## 相关

- 数字字面量拼写：`llmdoc/reference/number-formatting-parity.md`
- 序列化器入口与消费链：`llmdoc/architecture/dag-engine.md`「跨运行时格式兼容（GoFormat）」节
- 完整过程记录：`llmdoc/memory/reflections/json-key-order-parity-183.md`
