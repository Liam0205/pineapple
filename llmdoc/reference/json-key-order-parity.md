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

## pine-cpp：key 顺序按路径而定，key 转义三条路径原本都不满足

`pine-cpp/src/config/json_writer.cpp:33,54` 与 `json_writer.hpp` 里递归那处（
「每一层都排」的实现点）的 `std::sort` 配 `std::string` 的 `<` 就是字节序，天然与 Go
一致，含 BMP 之外的 key。`pine-cpp/tests/test_json.cpp` 的 "nested objects all sort
keys (L5)" 用例钉着这条。**只要响应是由 `Variant` 经 writer 序列化出来的，key 顺序就不用管。**

但 **key 的转义**曾是另一回事：`write_json_value` 的 value 分支走 `detail::write_go_string`（含 Go 的 HTML-safe 转义 `<` → `\u003c`、`>`、`&`，以及 U+2028/U+2029），而三处 key 是直接交给 RapidJSON 的 `Key()`，它不做这些转义。于是同一个字符出现在 **value** 里三方一致、出现在**key** 里就分歧（Go/Java 出 `a\u003cb`，C++ 出裸 `a<b`）。已改为 `detail::write_go_key`，由 `fixtures/server_byte_exact/08_html_chars_in_keys.json`、`test_json.cpp` 的
"object KEYS get Go's HTML-safe escaping" 用例双向钉住。

这条是审计第八轮发现的，机制值得记：**同一个字符串属性（转义规则）在 key 与 value 两条路径上各实现一次，只有一条被审过。** 仓库里原有 `fixtures/pipelines/html_chars_passthrough.json` 只覆盖value 侧，key 侧无任何 fixture，且 fuzzer 的字段名池只有 `[a-z_]`，所以三条通道全都看不见。

**第九轮又在同一形状上找到第二处，于是改成消除重复而不是补齐重复。** Go 的字符串转义规则在
pine-cpp 里原本有**三份**独立实现：

| 实现 | 服务的路径 | #183 前缺什么 |
|---|---|---|
| `detail::write_go_string` | `Variant` writer 的 value | （完整） |
| `server.cpp` 的 `json_escape` | 手写 JSON：trace `name`、`/stats` 的 key、error/warning 文案 | HTML-safe、U+2028/U+2029 |
| `metrics_collector.cpp` 的 `json_escape_str` | `/stats.resources` | HTML-safe、U+2028/U+2029、**以及所有控制字符** |

第八轮修了 writer 的 key 分支，第九轮就在 `server.cpp` 那份上重现了同一个缺陷——算子名
`a<b&c>d` 在 `trace[].name` 与 `/stats.operators` 的 key 上都出裸字节。**补齐第二份只会等第三份
再犯**，所以后两份现在都改为委托 `write_go_string` 并剥掉它加的引号，仓库里只剩一份规则实现。

**第十轮证明「合并」本身也要挑对合并目标。** 第九轮把三份转义合并到 `write_go_string`，但那份
实现缺 `\b` 与 `\f` 的两字符形式（Go 输出 `\b`/`\f`，它输出 6 字符的十六进制）。key 原先走
RapidJSON 的 `Key()`，而 `Key()` 的转义表**是有** `b`/`f` 的——所以合并动作把一处**本来正确**的
路径改坏了，用一个转义分歧换掉了另一个。已补齐两个 case。

教训：**合并到某一份实现之前，先验证那一份对完整规则表都正确，而不只是对当前发现的那几个字符。**
第八、九轮的 fixture 是按「上一轮恰好发现的字符类」挑的（HTML-specials、BMP 外），不是按 Go 的
转义表挑的；第十轮补的 `09_control_chars_in_keys.json` 才是照表覆盖：五个两字符形式、十六进制里
含大于 9 的数字（Java 曾输出大写 `\u000B` 而 Go 是小写）、最低与最高控制字符、行/段分隔符、
引号与反斜杠。

另外同轮清掉两处**不可达**的重复实现（`json.cpp` 的 `dump_impl` 131 行、`server.cpp` 的
`jsonvalue_to_string` 50 行）。`dump_impl` 的 key 分支是 `out += key;`——完全不转义。它们不可达，
但下一个要写序列化器的人会先找到它。「只有一份实现」这句话现在对整棵树成立，而不只对可达集成立。

`trace[].duration_ms` 原本用 `snprintf("%g")`（6 位有效数字），已改走 `go_format_json_number`。
**可达性实测过，比初判窄得多**：`duration_ms` 由 `duration_us / 1000.0` 得来，即微秒分辨率、
最多三位小数，所以 `%g` 只在整数部分到四位数（即算子慢于 **1000 ms**）时才截断——`1000.001`
会变成 `1000`，`1234.567` 会变成 `1234.57`。4 万次迭代的 bench 算子只有 4.27 ms，三位有效数字，
两种写法输出相同。所以这条修复是对的，但**没有任何跨引擎通道能让它变红**：要让它红需要一个
故意跑过 1 秒的算子，对校验套件太慢。改由 `test_json.cpp` 的
"trace duration magnitudes match Go" 直接钉格式化函数本身。

纪律：**同一个字符串属性（转义、排序、数字拼写）在一个运行时里出现第二份实现时，第一反应应该是
合并，而不是把规则抄一遍。** 抄一遍等于把「两处必须同步」这个约束交给未来的人记住，而本任务连续
两轮证明了没人记得住。

但 `/stats` 不走 writer：`server.cpp` 的 `handle_stats` 用字符串拼接手写 JSON，于是
key 顺序就是源码里的书写顺序。issue #183 期间实测发现它顶层输出
`operators, scheduler, server, http, resources`，而 Go 排序后是
`http, operators, resources, scheduler, server`；`server` 子树同样是书写顺序而 Go 排序；
`operators` 按管道声明顺序而 Go 按算子名排序。
三处都已修：顶层与 `server` 收集进 `std::map` 再按迭代序输出，`operators` 对
`Stats::snapshot()` 返回的 vector 显式 `std::sort`（它的注释写着「ordered by the
pre-init sequence」，即管道声明顺序）。

教训一：**「这个运行时天然满足」这句话只对某条代码路径成立，不对整个运行时成立。**

教训二（本任务里犯了**三次**）：**给 key 顺序写检查时，第一件事是确认所用 fixture 的声明顺序
与字典序不同。** 三次分别是——fuzz 生成器发 `sorted(...)` 的 flow_contract；06 号的算子名
`copy_score`/`truncate` 本就字典序，漏掉 `/stats.operators` 一整轮；14c 的 `common_input`
`["event","expose_duration"]` 本就字典序，漏掉 `input_snapshot`。三次的形式不同（生成器 / 算子名 /
字段声明），机制完全一样：**输入已经是期望的形状，于是检查对着错误实现也是绿的**。写完检查必须
对着 mutant 验证它会红，绿色本身不构成证据。
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
| `/stats` `operators` | `map[string]OpStatsSnapshot` | key 排序；值是 struct，字段保持声明顺序 | 是 |
| `/stats` `server` / `http` / `resources` / `operator_detail` | map | 排序 | 是 |

`/stats` 同一棵树里两条规则都出现，所以 Java 侧是**逐分支**按 Go 类型包装的，不是
整棵树一刀切——`GoFormat.sortedShallow` 就是为此存在（只排自己这一层，不下降）。
一刀切会把 `scheduler` 这个 struct 也排掉。

## 哪条通道能钉住它

- `scripts/cross-validate/09-raw-byte.sh` — 现在字节不等即失败（归一化回落已删），能钉住 key 顺序。
  但它走 CLI，**看不到 trace**（CLI 输出只有 common/items），也看不到 `/stats`
- 单测是转义规则的**快通道**，两侧对称：C++ 由 `test_json.cpp` 的
  "the two-character escape forms match Go exactly" 与 "trace duration magnitudes match Go" 钉；
  Java 由 `GoJsonKeyOrderParityTest.stringEscapingMatchesGoInKeysAndValues` 钉（它是 Java 侧
  唯一能抓住「控制字符大写十六进制」的检查——删掉 `createGoCompatMapper` 的控制字符接管后只有它变红）。
  **转义类回归优先靠单测发现，fixture 是兜底**：只靠 fixture 意味着要跑完整套 cross-validate 才知道
  坏了，而本任务第十轮的 `\b`/`\f` 事故正是这样发现的
- `scripts/cross-validate/06-server-http.sh` — 走 HTTP，是唯一能钉住 `output_snapshot` 与 `/stats`
  key 顺序的通道。它使用的 fixture 算子名会被**重命名成非字典序**——原本是 `copy_score` / `truncate`，
  已经是字典序，于是 `/stats.operators` 按管道顺序输出的实现看起来也是对的，这条检查因此漏了一轮。它原先打印 `sorted(trace[0].keys())`，把待测维度本身排掉了；现已改为用
  `object_pairs_hook` 比 key 序列与嵌套结构，并给算子开 `debug`、把请求补到 12 个 item 且
  加非排序的额外 common key —— 其中**只有 12 个 item 的补齐是真有牙的**——它经由
  `output_snapshot.item_writes` 钉住 int key 的字符串排序。额外的 `*_probe` common key 实测**无效**：
  `snapshotInput` 只输出算子**声明的** `common_input`，请求里多加的 key 到不了 `input_snapshot`；
  而把它们塞进 `common_input` 会破坏 `transform_copy` 的 arity。
  `debug` 开关是必要条件（不开就完全没有快照），但不充分。
  `input_snapshot` 由**另一条检查 [14c]** 钉：换用 `control_op_nil_field_no_crash.json`
  （`ctrl_if` 声明 `["event","expose_duration"]`），并把每个算子的 `common_input` 声明**反转**——
  原声明恰好已是字典序，不反转的话这条检查同样漏。单测
  `traceSnapshotsSortWhileTheTraceEntryKeepsDeclarationOrder` 也覆盖同一属性
- `scripts/cross-validate/14-byte-exact-execute.sh` — 直接 `==` 响应体
- `scripts/differential-fuzz.py` 的 `key_order_signature()` — 用 `object_pairs_hook` 从原文读 key 顺序单独比对，绕开 `normalize_json` 的 `sort_keys=True`

细节见 `llmdoc/guides/ci-quality-baseline.md`「校验通道能钉住的属性」节。

## 相关

- 数字字面量拼写：`llmdoc/reference/number-formatting-parity.md`
- 序列化器入口与消费链：`llmdoc/architecture/dag-engine.md`「跨运行时格式兼容（GoFormat）」节
- 完整过程记录：`llmdoc/memory/reflections/json-key-order-parity-183.md`
