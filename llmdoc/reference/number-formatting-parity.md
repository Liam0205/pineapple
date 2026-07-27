# 跨语言数值格式化实测事实

本文件记录复刻 Go 数值格式化时**必须实测、不能靠推理**的两条事实。两条都是 issue #180 期间用探针实测得到的，各自让一轮实现失败过。

适用场景：在 pine-cpp / pine-java（或任何第四运行时）里复刻 Go `strconv` / `encoding/json` 的 double 输出，或修改已有的格式化路径。

参照的实现落点：

- pine-java：`pine-java/src/main/java/page/liam/pine/GoFormat.java`（`formatJsonNumber`）
- pine-cpp：`pine-cpp/src/config/json.cpp`（`go_format_json_number` / `go_json_to_fixed` / `go_json_to_scientific`）
- Go 侧规则来源：`encoding/json` 的 `floatEncoder`

## 事实一：C++ `std::to_chars` 只有 `chars_format::scientific` 保证最短往返

Go `strconv.FormatFloat(d, 'f'|'e', -1, 64)` 的 `precision = -1` 意为「能往返的最少位数」。C++ 侧对应关系不是逐个 format 平移：

- `chars_format::fixed` 打印的是**精确值**，不是最短往返
- **不带 format 参数的默认 overload，在量级大到不需要指数时也退化成精确打印**——这条最反直觉，issue #180 第一轮就是按「默认 overload = 最短往返」写的，改完仍剩 1 个分歧
- 只有 `chars_format::scientific` 保证最短往返

实测（`d = 1.0000000000000002e20`）：

```
to_chars 默认 overload     -> 100000000000000016384      精确值
chars_format::fixed        -> 100000000000000016384      精确值
chars_format::scientific   -> 1.0000000000000002e+20     最短往返
Go FormatFloat(d,'f',-1,64)-> 100000000000000020000
```

因此 pine-cpp 的 `go_format_json_number` 统一从 `chars_format::scientific` 取数字串，再由 `go_json_to_fixed` / `go_json_to_scientific` 自己摆小数点：两种输出形式（平铺小数 / 科学计数）**共用同一个数字来源**，避免两条形式各自取数字导致的精度分叉。

## 事实二：Go `encoding/json` 的指数格式相对 `strconv` 有一处不对称

`strconv.FormatFloat(d, 'e', -1, 64)` 把指数补到至少两位（`1e-07`），而 `encoding/json` **只对负指数去掉一个前导零**，正指数与三位数指数一概不动。

实测：

```
strconv 'e': 1e-07  -> json: 1e-7      去掉一个前导零
strconv 'e': 1e-09  -> json: 1e-9      去掉一个前导零
strconv 'e': 1e+21  -> json: 1e+21     不动
strconv 'e': 1e+100 -> json: 1e+100    不动
strconv 'e': 1e-100 -> json: 1e-100    三位数，不动
```

靠推理很容易写成两边都 trim 或都不 trim，两种都错。这条规则不对称到没法从文档反推，必须跑一次 Go 程序对照。

## 纪律

两处实现的注释都显式标注了 "verified against encoding/json rather than inferred"。给这类跨语言等价函数写实现时，注释要说明**依据是实测还是推理**——下一个改这段代码的人需要知道哪些常量不能靠「看起来更合理」去调整。

更一般的判据：跨语言标准库里名字/角色对应的「等价函数」，其隐含语义常常不等价（`to_chars` 默认 overload 与 `FormatFloat(-1)` 名义上都是「合理默认」，实际一个精确值一个最短往返）。这类差异只能实测。

## 相关

- 格式化入口划分与消费链：`llmdoc/architecture/dag-engine.md`「跨运行时格式兼容（GoFormat）」节
- 哪条校验通道能钉住字节级数字格式：`llmdoc/guides/ci-quality-baseline.md`「校验通道能钉住的属性」节
- 完整过程记录：`llmdoc/memory/reflections/json-number-format-parity-180.md`

## 非有限值（NaN / ±Inf）：刻意不对等，记为 accepted difference

Go 的 `encoding/json` 对非有限 float64 直接报 `UnsupportedValueError`，**根本不产出字节**。
所以这里没有"与 Go 一致"这个选项，三运行时各自的取舍如下：

| 运行时 | 输出 | 是否合法 JSON |
|---|---|---|
| pine-go | 拒绝序列化（error） | — |
| pine-cpp | `inf` / `-inf` / `nan`（裸 token） | 否 |
| pine-java | `"Infinity"` / `"-Infinity"` / `"NaN"`（带引号字符串） | 是 |

**为什么不统一**：正常路径上三者都不会走到这里——写入侧有 NaN/Inf 校验
（pine-cpp 在 `engine.cpp` 的 `validate_output`、pine-go 在 `row_frame.go`）。
唯一能绕过校验的入口是**请求里直接带非有限数值**，而这条路上 pine-go 与
pine-cpp 都在解析阶段就拒绝整个请求（C++ 的 `from_chars` 返回
`result_out_of_range`），只有 Jackson 会把 `1e400` 静默 coerce 成 `Infinity`。
也就是说分歧的成因在**请求解析层**，不在数字格式化层，修格式化不能消除它。

**pine-java 选带引号字符串**的理由是：那条路上已经不可能与 Go 字节对等
（Go 会拒绝请求），剩下唯一可争取的属性是"响应仍是可解析的 JSON"。曾经有一版让
序列化器无条件走 `formatJsonNumber` 的输出，结果写出裸 `+Inf`，把整份响应变成
不可解析——比不对等更糟。`GoFormat.formatJsonNumber` 现在对非有限输入直接抛
`IllegalArgumentException`，逼调用方自己决定，而不是编造一个"看起来像 Go"的表示。

**pine-cpp 保留 `inf` / `nan`** 是为了不在修 #180 时顺带改动既有行为——它与
`ab2dfd5f` 之前逐字节一致。修复只解决了一个真实缺陷：`to_chars` 对非有限输入
**返回成功**并写出 `inf`，导致这些字母流进 decompose 逻辑被当成尾数/指数数字，
输出 `i.nfe+02`（既不合法也不是原来的 `inf`）。

**若要真正统一**，得先解决请求解析层的分歧（让 pine-java 也拒绝非有限请求），
那是独立议题，不在 #180 范围内。`metrics_collector.cpp` 的 `/stats` 路径曾被标为「理论上可序列化非有限指标值、未实测」——现已查清：那两处调用点（`metrics_collector.cpp:195,208`）走的就是 `go_format_json_number`，而该函数现在对非有限输入前置返回 `nan` / `inf`，与上表 pine-cpp 一行一致。也就是说 `/stats` 不构成额外暴露面，行为与 `/execute` 相同。

## Java 侧最短往返：为什么最终选了「不优化」的实现

`GoFormat.shortestRoundTrip` 从 precision 1 逐位向上试，返回第一个能往返的候选。
**没有快速路径，刻意如此**，这是三轮审查换来的结论：

前后加过两版快速路径（「若少一位就无法往返，则 `Double.toString` 已最短，直接返回」），
逻辑本身正确、输出也始终与 Go 一致，但每一版都有一整类输入被守卫无声排除：

| 版本 | 声称 | 实测 |
|---|---|---|
| v1 | 「normal double 首次尝试即命中」 | 恰好相反，normal 是最慢的一类，约 90× |
| v2 | 「快速路径覆盖 normal double」 | 整数值 double **0% 命中**（`Double.toString(1.0)` = `"1.0"`，尾随零被算作有效位） |
| v3 | 「前导零计数只是放宽搜索范围」 | `[0.001, 1)` **0% 命中**，8065 ns/value vs `[1,1000)` 的 1104 |

三次的共同机制是**基准样本恰好避开了会反驳注释的那个区间**：
`nextDouble()*1000` 有 99.9% 落在 `|d| ≥ 1`，也就是快速路径唯一真正生效的地方。
所以「测出来是快的」和「注释说的那类输入是快的」是两件事。

**结论与实测代价**：这条路径挂在 `/execute` 响应序列化上，代价是真实的——实测 30000 个 double 的响应，本实现 103 ms、裸 Jackson 5.5 ms，约 **18.6×**（实测环境：本机 openjdk 26、`nextDouble()` 均匀取样；另一次独立复测同形状得 22–23×，**倍数本身有约 20% 的环境/取样波动，引用时按「约 20×」理解，不要把 18.6 当阈值**）（不是「慢几微秒」，早先注释里那句「不是热点瓶颈」未经测量，已删）。选择接受这个代价的理由不是它便宜，而是三轮审查证明**在这段代码上加快速路径的失败率是 3/3**，每次都以「注释说错自己覆盖哪些输入」的形式出现，而正确性是硬契约、吞吐不是。若这个 18.6× 在实际负载里成为问题，那是一个独立的、有明确验收标准的优化任务（附下面的分区间基准要求），而不是顺手加个守卫。现在的实现无法说错「它覆盖哪些输入」，因为它对所有输入
一视同仁。若将来真要优化，**必须分别基准 `[0.001,1)`、`[1,1000)`、整数值三类**——
只从其中任一类取样都会印证你已有的判断。

## 一个易踩的坑：缩短必须作用在 `Double.toString` 选出的数字上

不能用 `new BigDecimal(double)` + `MathContext` 去舍入**精确二进制展开**：
`MathContext` 在真值上做 HALF_UP，而 Go 报告的是**离该 double 最近的**那位数字。
实测分歧（数量随取样分布变化：按量级定向取样时 200k 中 50+ 例，纯随机 bit pattern 则 8–13 例；机制与下面四个值本身不依赖分布）：

```
bits=431f64571af9dce5   go 2209012388886329.2   舍入精确值 → ...329.3
bits=42e3866babcd3a24   go 171744423733713.12   舍入精确值 → ...713.13
```

`Double.toString` 选的数字本来就是对的，唯一的问题是**可能太多**——所以正确做法是
`new BigDecimal(Double.toString(d))` 再逐位缩短。由
`digitsComeFromDoubleToStringNotFromRoundingTheExactValue` 钉住。

## 另一条既存不对等：resource lookup key 上三个运行时互不相同

`formatFloatF`（Java）/ `go_format_lookup_key`（C++）/ `strconv.FormatFloat(d,'f',-1,64)`（Go）
是 `transform_resource_lookup` 的 key 派生函数，与 JSON 输出**是不同的路径**。对次正规值
三者输出互不相同（实测 `Double.MIN_VALUE`）：

| 运行时 | 输出 | 长度 |
|---|---|---|
| pine-go | `0.000...005`（完整平铺） | 326 |
| pine-java | `0.000...049`（`Double.toString` 非最短，多一位） | 327 |
| pine-cpp | `5e-324` | 6 |

C++ 那支的成因单独说一下，因为不看代码想不到：`go_format_lookup_key` 用 `char buf[64]`
配 `to_chars(chars_format::fixed)`，326 字符**装不下**，`to_chars` 返回
`value_too_large`，于是走了 `go_format_g` 兜底——而那个兜底会输出科学计数法。也就是说
它不是「少了几位」，是整个格式换了。

**可达性**：请求里带 `5e-324` 能活着到这里（Jackson 解析出 bits=1），所以不是理论问题。

**为什么不在 #180 里修**：#180 的范围是 JSON 输出字节；key 派生函数是另一条路径，
且改它对任何已经按当前形式建过索引的数据是行为变更。现状由
`formatFloatFSubnormalDivergenceIsPinnedNotFixed` 钉住 Java 侧的 327，
使后续修它必须显式改掉一个失败断言。

**后续要收这条时的注意点**：不要只改 Java 的位数——C++ 的 `buf[64]` 必须一起扩，
否则「统一」之后 C++ 仍在输出 `5e-324`。这一点是第九轮审查提出的，它在自己的
finding 之外额外查了 C++ 分支，而我之前的文档只写了 Java vs Go。
