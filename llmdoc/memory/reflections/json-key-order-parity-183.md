# [JSON object key 顺序跨运行时对等修复（issue #183）]

分支 `fix/183-json-key-ordering-parity`（基于 `origin/master` = `75543a72`）。初版两个 commit（下列），审计过程中又加了若干修复/加固 commit。**这里不再写具体数字**——本行先后被改过两次都是因为数字过期：第一次写「两个 commit」（漏掉审计产生的），第二次写「共 8 个」，而那次提交本身就是第 9 个，写下的瞬间就错了。commit 数随审计轮次增长，属于不该写进复盘的量；要数就 `git rev-list --count`：
`3d92e968 fix(java)` 修实现 + `c1ae534c test(ci)` 补守门。本次同时关掉 `doc-gaps.md` 里
「issue #183」条目与「字节级对等校验通道覆盖面太窄」条目的 (b) 分支。

## Task

修 issue #183：Go `encoding/json` 对 map key 排序输出，pine-java Jackson 序列化
`LinkedHashMap` 保留插入顺序，`/execute` 响应的 key 顺序不一致。`/execute` 输出是
字节级契约，所以顺序本身就是分歧。另外补上让这个维度可见的校验通道。

## Expected vs Actual

- Expected（来自 issue 标题与本任务的 goal 措辞）：pine-java 与 pine-cpp 两侧都要
  对齐 pine-go；实现上给 Jackson 装一个 Map 排序即可。
- Actual：
  - `/execute` 上**只有 pine-java 错**，pine-cpp 本来就对（`std::string` 的 `<` 就是字节序，
    连 BMP 外字符都天然与 Go 一致）。
  - **但 `/stats` 上 pine-cpp 也错，是审计第二、三轮才查出来的。** 那条路径是 `server.cpp`
    手写字符串拼接、不走 `Variant` writer，顶层、`server` 子树、`operators` 三处都按书写顺序
    输出，最终一并修了（`88ed6924` + `09c508cd`，`server.cpp` 净 +57/-18）。
    **本节最初写的是「本次没有改 pine-cpp」——那是在修 `/stats` 之前写下的，事后没跟着更正，
    直到第四轮审计把它连同 `index.md` 里复制的那份摘要一起抓出来。**
  - 实现不能「给所有 Map 排序」——Go 的规则是 map 排序、struct 保序，全排会弄坏
    响应外层（envelope），第一版就这么错了一次。
  - 排序 comparator 不能用 `String.compareTo`，也不能用 Jackson 自带的
    `SerializationFeature.ORDER_MAP_ENTRIES_BY_KEYS`，两者都是 UTF-16 序。
  - 加了检查之后 fuzz 仍然对着故意改坏的序列化器 1000/1000 全绿，根因在**生成器**，
    不在检查。

## What Went Wrong

### 1. 受影响范围来自任务措辞，不来自实测（与 #180 同类错误的镜像）

三方实测（同一 flow，key 声明顺序 `c10, c2, c1`）：

```
GO   {"common":{"c1":"z","c10":"x","c2":"y"},...}
CPP  {"common":{"c1":"z","c10":"x","c2":"y"},...}   <- 本来就对
JAVA {"common":{"c10":"x","c2":"y","c1":"z"},...}   <- 唯一错的
```

pine-cpp `json_writer.cpp:33,54` 早就有 `std::sort`，且 `test_json.cpp` 的
"nested objects all sort keys (L5)" 用例一直钉着这条。BMP 外 key 也实测过三方全同
（`{"z":3,"�":1,"\U00010000":2}`）。

这与 #180 那次「标题说 1e20、实测 15/20 分歧」是同一类错误的两个方向：那次范围被
说小，这次被说大。**结论一致：动手前先做三方定向探针，issue 标题与任务描述都只是
症状线索，不是范围声明。**

### 2. 第一版把 envelope 也排序了，弄坏 partial-error fixture

最初给 `Map.class` 注册序列化器，于是**所有** Map 都排序，包括响应外层。
cross-validate 从 55 掉到 54：

```
Go:   {"common":{...},"items":[...],"error":"..."}
Java: {"common":{...},"error":"...","items":[...]}   <- error 被排到 items 前面
```

根因：Go 的规则不是「全排」。`encoding/json` **对 map 排序、对 struct 保持声明
顺序**。pine-go 的响应外层是 struct `executeResponse{Common, Items, Warnings,
Trace, Error}`（`pine-go/pkg/server/server.go:655`），里面的 payload 才是
`map[string]any`。实测确认：

```go
{"common":{"a":2,"z":1},"items":[1],"error":"boom"}   // struct 保序，内层 map 排序
```

Java 里 Map 和 struct 没有类型层面的区分，所以必须显式建模：新增
`GoFormat.SortedByUtf8` 包装类型，payload 包、envelope 不包；`wrapPayload` 递归
下钻（Go 在每一层都排，list 里嵌的 map 也要排）。

### 3. `String.compareTo` 与 `ORDER_MAP_ENTRIES_BY_KEYS` 都是错的排序键

Jackson 有现成的 `SerializationFeature.ORDER_MAP_ENTRIES_BY_KEYS`，但它按 map 的
natural ordering 排，即 `String.compareTo`，即 **UTF-16 code unit 序**。Go 比的是
Go string，即 **UTF-8 字节序**。两者在 BMP 外相反：

```
U+FFFD   UTF-16 fffd        UTF-8 ef bf bd
U+10000  UTF-16 d800 dc00   UTF-8 f0 90 80 80
```

`compareTo(U+FFFD, U+10000) = +1`，UTF-8 字节序是 `-1`。用内置 feature 只会把分歧点
从 ASCII key 挪到 emoji key。实现改为 `GoFormat.compareUtf8`：ASCII 快路径（两序
一致时不建字节数组），遇非 ASCII 落到 `Arrays.compareUnsigned` 比 UTF-8 字节。

**这条上一轮 doc-gaps 已经预警过，预警生效了**——没有重新踩一遍。

### 4. `PineServer` 里早有一个只覆盖一半的旧修复

`PineServer.java` 有本地 `sortMapKeys` / `sortItemKeys` / `sortListElements`（用
`TreeMap`）。所以 **HTTP 路径本来就排序、只有 `RunCli` 路径没排**——这解释了为什么
14 号 byte-exact 通道（走 HTTP）一直是绿的，而 CLI 复现是红的。而且那套 helper 用
`TreeMap` = `String.compareTo`，带着 UTF-16 的 bug。

本次把三个 helper 删掉，统一到序列化器一处 comparator，两个入口共用。

教训：**同一契约有两份实现时，先找有没有已存在的「半份」**，否则会在半份之上再叠
一层，并让「哪条路径是对的」更难说清。

### 5. 三处守门盲点，第三处最值得记

- **09 号通道**：标题写 "no normalization"，实际字节比较失败后回落 `normalize_json`
  再比、相等就打 `[W]` 计 pass。这个回落**就是为了容忍 #183 而存在的**，于是「字节
  对等通道」检不出唯一它在容忍的那个字节差异。删掉回落后 91/91 全绿，把 Java
  序列化器改回去立刻红。
- **fuzz 归一化**：`sort_keys=True` 抹掉 key 顺序整维度。新增 `key_order_signature()`，
  用 `object_pairs_hook` 从原文读出顺序单独比，值比对仍保留浮点容差与 item 顺序归一化。
- **fuzz 生成器只发已排好序的 flow_contract**（最值得记）：加完 ordering 检查后，对着
  故意改坏的序列化器**仍然 1000/1000 全绿**。查下去发现 `differential-fuzz.py` 生成
  flow_contract 时写 `sorted(common_outputs)` / `sorted(item_outputs)`，声明顺序永远
  等于排序后顺序，插入顺序输出恰好与 Go 一致。改成 shuffle 后 mutant 60 轮红 6 轮。

提炼：**只会生成「已经是期望形状」的输入的生成器，检不出关于形状的 bug。** 这与 #180
复盘里「benchmark 取样恰好避开会反驳注释的区间」、以及 `04_number_precision.json`
「名字像覆盖精度、实则只覆盖已对区间」是同一个失败模式——样本回避了反例。

### 6. 自己又犯一次「加了检查但门是关着的」

`key_order_signature` 最初被 gate 在 `strict_order` 上，理由是「key 顺序只在 item
顺序确定时才有意义」。这是错的，而且 `strict_order` 只在管道以唯一键排序结尾时为
true，所以大部分轮次检查是关着的。key 顺序与 item 顺序是**互相独立**的确定性维度。
改成不 gate：item 顺序不确定时把各 item 的 key 序列当 multiset 比，每个 item 自己的
key 顺序仍然精确比。

这与 #180 复盘里「perf 快路径三次都漏掉一整类输入」同类：**新加的守卫条件本身没被
验证过覆盖面**。判据应当是「关掉这个条件后检查还会红吗 / 这个条件为 true 的轮次占
多少」，而不是条件读起来合理。

### 7. 正面经验：前置条件写进 doc-gaps 真的省掉一次返工

`doc-gaps.md` 上一轮写明「(b) 把 09 号通道的归一化回落改成硬失败 —— 它是 #183 的
**前置条件**：先决定通道方案，再修 #183，否则修完没有回归门」。本次照这个顺序做：
先拆回落（得到一条会红的通道），再改实现，结论完全成立。若反序，修完实现后 09 号
通道会因为回落而继续绿，无法证明修对了。

**把「修 X 之前必须先做 Y」显式写进 doc-gaps 是有效的跨任务信号**，值得继续这么写。

## Root Cause

1. **范围与实现方案都被「看起来合理的直觉」带偏，直觉的来源是措辞而非实测。**
   「两个运行时都错」来自任务描述，「给 Map 注册序列化器」来自「Go 排序 map key」这句
   话的字面理解——漏掉了 Go 同时还有「struct 保序」这半条规则。
2. **Java 缺少 Go 的类型区分，跨语言复刻时必须把隐含区分显式化。** Go 靠 map/struct
   两种类型天然分流，Java 两者都是 `Map`，不显式建模就只能二选一，而两种选择各错一半。
3. **守门通道的名字与实际强度不符，且不符的方向正是要检的那个维度。** 09 号的回落是
   为容忍 #183 而加的，等于把「检不出」写进了 gate 本身。
4. **生成器与检查是两个独立环节，只补检查不动生成器等于没补。** 检查覆盖面上限由
   输入分布决定；输入分布恰好落在期望形状里时，检查永远不触发。

## Missing Docs or Signals

- 没有任何稳定文档记录「Go `encoding/json` map 排序 vs struct 保序」这条二分，以及
  「Java 侧必须显式建模该二分」。下一个碰序列化的人一定会先想「直接给 Map 注册不就
  行了」，然后重踩 envelope 那一脚。
- 没有稳定文档记录「UTF-8 字节序 vs UTF-16 code unit 序」以及「Jackson
  `ORDER_MAP_ENTRIES_BY_KEYS` 不能用」。本次靠 doc-gaps 里的临时预警接住，但那条
  预警随 #183 关闭会被移除，信息会丢。
- `guides/ci-quality-baseline.md` 的通道可见性表**已经过期**：09 号的回落已删、fuzz
  已加 ordering 检查、表里「issue #183 因此从未被抓到」的表述需要改成历史叙述。
- 通道能力那节只讲了「归一化会抹掉什么」，没有讲「生成器只发期望形状会抹掉什么」。
  这是第二类不可见性，缺一条并列纪律。

## `/stats` 这条漏了两轮的路径，单独记一笔

`/stats` 在本任务里被漏掉两次，而且两次原因不同：

1. **第一版实现完全没想到它。** 我按 issue 标题理解成「JSON key 顺序」，动手只看了
   `/execute`，而 `/stats` 是另一个 handler。教训：**契约是「响应字节」时，受影响面是
   「所有产出 JSON 的端点」，不是「issue 里举例的那个端点」。**
2. **加了 `/stats` 检查之后仍漏了 `operators` 一整轮。** 检查用的 fixture 算子名
   `copy_score` / `truncate` 恰好已是字典序，于是按管道顺序输出的实现看起来也是对的。

pine-cpp 在 `/execute` 对、`/stats` 错，是因为两条路径的序列化机制不同：前者走 `Variant`
writer（`std::sort` + `std::string` 的 `<`，天然字节序），后者是手写拼接。**「这个运行时
天然满足」只对具体代码路径成立，不对整个运行时成立**——已进 `reference/json-key-order-parity.md`。

## Promotion Candidates

- **必须进稳定文档**：Go map 排序 vs struct 保序、Java 必须显式区分（`SortedByUtf8`
  包装 payload、envelope 不包），落点建议 `architecture/dag-engine.md` 的 GoFormat 节
  （与 #180 新增的 `formatJsonNumber` 入口同一节，都是 JSON 序列化路径的规则），或
  `reference/number-formatting-parity.md` 扩成更宽的「Go JSON 输出对等」参考。
- **必须进稳定文档**：UTF-8 字节序 vs UTF-16 code unit 序、`compareUtf8` 是唯一正确
  排序键、Jackson 内置 feature 不可用。落点同上。
- **进 `guides/ci-quality-baseline.md`**：新纪律「只会生成期望形状输入的生成器，检不出
  关于形状的 bug」，与既有「声称字节级对等的属性必须有一条不归一化的通道覆盖」并列；
  配 red-before/green-after 判据「新加检查后必须用 mutant 验证它真的会红，绿了先怀疑
  生成器而不是被测代码」。另加一条守卫条件纪律：**新增 gate 条件必须量化它为 true 的
  轮次占比**（`strict_order` 那次的直接教训）。
- **必须更新 `guides/ci-quality-baseline.md` 的通道可见性表**：09 号回落已删（现在字节
  不等即失败）、fuzz 已有 `key_order_signature`、生成器已 shuffle flow_contract。当前
  表述过期。
- **`doc-gaps.md` 两条**：#183 条目**关闭**（移除并在稳定文档留结论）；「字节级对等校验
  通道覆盖面太窄」条目的 (b) 已完成，**(a) 继续扩 `fixtures/server_byte_exact/` 仍然
  开放**，条目保留但需改写现状（14 号通道不再是唯一真字节通道，09 号现在也是）。

## Follow-up

1. 调 `recorder` 落地上述稳定文档改动：GoFormat 节补 key 排序两条规则、
   ci-quality-baseline 更新通道表 + 补两条纪律、doc-gaps 关 #183 并改写通道条目。
2. `fixtures/server_byte_exact/` 仍需按 doc-gap (a) 扩覆盖面。本次加了 `07_non_bmp_keys.json`（审计第四轮，为给 `writeValueAsBytes` 那处修复补回归门），数量以 `ls fixtures/server_byte_exact/` 为准（审计中又加了两个），但离覆盖主要响应形状还很远，
   守门增强全部落在 09 号通道与 fuzz 上。
3. 检查 `differential-fuzz.py` 里是否还有其他「生成器只发期望形状」的维度（本次只查了
   flow_contract 的 key 顺序）。这条与 issue #175 记的「flow_contract 投影盲区」是同一
   文件的第二次现身，值得一次专门扫查。

## 验证情况（本次已完成）

- 三方定向探针：`c1/c10/c2` 与 BMP 外 key 两组，go/java/cpp 输出逐字节相同
- `make java-test` / `make cpp-test` 全过（**用例数同样不写死**——「346/新增 10」这个数字在写下它的那次提交里就已过期，因为同一 commit 又加了一个测试；数就跑命令）、`make cpp-test`（用例数以命令输出为准）；`make lint`、
  `make test`、`make codegen-check` 全过
- `make cross-validate` 55/55（09 号通道 91/91，无归一化回落）；
  `make differential-fuzz` 1000/1000
- mutation 双向验证：`compareUtf8` → `String::compareTo` ⇒ 非 BMP 用例红；
  去掉嵌套 wrap ⇒ 深度用例红；删 Map 序列化器 ⇒ 09 号通道立刻红、fuzz 60 轮红 6 轮
