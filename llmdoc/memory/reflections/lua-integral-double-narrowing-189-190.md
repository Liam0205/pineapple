# [pine-java 把整数值 double 窄化成 Long 导致同一 float64 两种拼写（issue #189 + #190）]

分支 `fix/189-190-number-spelling-parity`（基于 `origin/master` = `a9830fca`），单 commit
`7c4540c1`（初版修复与文档），以及审计第一轮后的 `a39a950d`（修 `GoFormat.sprint` 按装箱类型分派）。**文件数与增删行数不在此复述**——本行原本写死的数字在下一个 commit 就过期了，要数就跑 `git show --stat`。

## Task

#189（nightly diff-fuzz，seed=1655185644 round 7005）与 #190（在 #187/#188 审计期间撞到
并保存的用例）**是同一个缺陷**。症状是同一个 float64 在 go 与 java 上拼写不同：

```
go   4611686018427388000
java 4611686018427387904
```

`(-2147483648)^2` 恰好等于 `2^62`。Go 的 `encoding/json` 输出 `strconv` 的最短往返，
pine-java 输出精确整数。目标：三运行时字节一致，并给这条区间补上回归门。

## Expected vs Actual

- Expected：数字拼写分歧 → 落在格式化器上，改 `GoFormat.formatJsonNumber` 的某个阈值。
  上一次同族问题（#180）就是这么修的，`reference/number-formatting-parity.md` 也是为那次
  立的。
- Actual：**格式化器完全正确**。用探针实测 `formatJsonNumber` 在 2^53、2^53+2、2^62、
  1e16/1e17/1e20 上与 Go `encoding/json` 逐位一致。真正的缺陷在**上游一层**：值在到达
  格式化器之前就已经不是 double 了。

`TransformByLua.fromLua`：

```java
if (d == Math.floor(d) && !Double.isInfinite(d) && d >= Long.MIN_VALUE && d <= Long.MAX_VALUE) {
    return (long) d;   // 无 2^53 上界
}
```

整数值 double 被窄化成 `Long`，于是**根本没走浮点格式化路径**，Jackson 直接打印精确整数。
而 double↔long 在 2^53 以上不再无损。

**pine-java 是三方里唯一这么做的**：`pine-go/operators/lua/pool_gopher_lua.go:475` 对所有
Lua number 一律 `return float64(x), nil`、**没有整数分支**（`pool_wangshu.go` 同理走
`wangshu.Number(float64(x))`）；`pine-cpp/src/lua/lua_bridge.cpp:204` 用
`Variant(lua_tonumber(...))` 也是 double。所以 `Long` 从来不是跨运行时契约，是 pine-java
的内部实现细节。

修法：去掉窄化、一律 `return v.todouble()`。2^53 以下**毫无变化**——`formatJsonNumber`
打印整数值 double 时不带小数点（`42.0` → `42`），实测对照过 42/0/−1/1e15/2^53/2^62/
负 2^62/1.5 全部与旧 long 路径同串。

## What Went Wrong

### 1. #175 扫过这个函数、检查清单结构性地看不见这一维（最值得记的一条）

`git log -S 'return (long) d;'` 查明窄化由 `81c1a36c`（2026-05-18）引入。而 issue #175
的三个 commit（2026-07-23）**都带着这行**——#175 修的正是 `fromLua` 的标量派发
（`isnumber()`/`isstring()` coercion → type tag），改动点距这行只有三行。
`skip-field-lazy-input-and-pool-baseline-keys.md` 的 Follow-up 还明确写下：

> pine-java `TransformByLua.java` 的 `is*()` 派发点现已全部为 type-tag 派发（table-key
> check / fromLua / snapshotKeys），三处闭环；下次再触碰该文件时不需要额外扫。

这条声明本身没写错：`is*()` 派发点确实清零了。错的是它被当成了「这个函数已审完」。

**#175 的检查项是「派发方式对不对」，这行的问题是「派发之后的类型转换对不对」——同一函数、
同一屏、不同维度。** 那次的 grep 清单（找 `is*()` 调用）在构造上不可能命中一个
`(long) d` 强转。

这与仓库已有的「一条声明的每份副本都要清理」（#183 的 `/stats` 漏两轮、#179 的五处反向
注释）是**不同的失效模式**：那条是同一维度散落多个位置，这条是同一位置承载多个维度。

### 2. 与 #180 的关系同型

#180 立了 `reference/number-formatting-parity.md` 专管「数字怎么拼」，但那篇整体假设
**值以 double 到达格式化器**。「值在到达格式化器之前就被换成了另一种类型」是它覆盖不到
的失效面。本次正是这一类，所以按那篇的索引去找，会一路查格式化器、查不到东西。

### 3. 六处断言冻结的是装箱类型而不是契约

`TransformByLuaTypeIdentityTest`（两处 + 数组元素一处）、`TransformByLuaBaselineTest`
两处、`TransformByLuaCompilerBackendTest` 一处，写的是
`assertInstanceOf(Long.class, ...)` / `assertEquals(42L, ...)`。

Go 侧没有整数分支，`Long` 从来不是契约。这些断言把内部实现细节冻住了，反过来让**正确的
修复"打破测试"**。已改为断言数值与序列化形式：

```java
assertInstanceOf(Double.class, intOut);
assertEquals(42.0, intOut);
assertEquals("42", GoFormat.formatJsonNumber((Double) intOut));
```

新增 `integralDoubleAbove2Pow53KeepsGoSpelling` 直接钉 `"4611686018427388000"`。

值得注意的是这六处断言**部分来自 #175**——#175 为了钉住类型身份而用 `assertInstanceOf`
钉 Java class（见那篇 reflection 的 Task 段），当时是对的手段（要区分 `"42"` 与 `42`），
但把 number 分支的 box 类型一起钉住了，超出了它要保护的属性。

### 4. `FixtureTest.assertValueEquals` 的比较逻辑有缺陷（不是数据问题）

它对 `Number` 做数值比较（`1e-9` 容差），但 **List/Map 落到 `String.valueOf`**，于是
`[10.0, 15.0]` 与 fixture 字面量 `[10, 15]` 仅因装箱格式化就失败。修法是对 List/Map
递归调用 `assertValueEquals`——也就是对容器做它对标量早就在做的事。

**而 Go 的 fixture runner 用 `fmt.Sprintf("%v")`**（`pine-go/integration/fixture_test.go:146`
等三处、`pipeline_fixture_test.go:37`），float64 `10` 打印成 `10`——所以 Go 一直通过。

同一份 fixture、同一个值、两个 runner 的比较精度不同。Go 因为 `%v` 恰好抹掉 int/float
差异而看不见问题，Java 因为 `Double.toString` 带 `.0` 而炸；两边都在读同一份"期望值"。

第一反应会是「改 fixture 期望值」，那是错的：值没变，变的只是 Java 侧的装箱类型，
Go 侧一直返回 float64。

### 5. 一条自找的过程教训：长跑 fuzz 期间重编译

在长跑 differential-fuzz 的**同时**重编译了 Java，导致 8 个假分歧（`java_rc=1`、空输出），
全部是读到半写状态的 `target/classes`。一度以为是自己的修改引入了新缺陷，逐个复跑才确认
全是构建竞争。

## Root Cause

1. **缺陷的根因**：pine-java 单方面引入了一个「整数值 double → Long」的表示转换，而这个
   转换在 2^53 以上不无损，且**没有对应的跨运行时契约**（另两方都是无条件 double）。
   引入时（`81c1a36c`）大概是为了让小整数序列化得好看，但序列化层其实已经处理了这件事
   （`formatJsonNumber` 对整数值 double 不打小数点），所以这个转换从一开始就是纯粹的
   多余风险。

2. **为什么它活过了 #175**：审计的检查维度决定了它的盲区。#175 的维度是「派发谓词」，
   这行的维度是「派发后的类型转换」。审完一个维度后写下「这个文件闭环」，把维度级的结论
   升格成了文件级的结论。**函数是审计单位、维度不是**——一个函数可以在维度 A 上闭环、
   在维度 B 上完全没被看过。

3. **为什么按 #180 的索引找不到**：`reference/number-formatting-parity.md` 的作用域是
   「double 怎么变成字符串」，隐含前提是「值已经是 double」。这个前提没写下来，读者会把
   它当成「所有数字拼写问题的入口」。

4. **测试没能拦住的根因**：断言冻的是内部表示。`assertInstanceOf(Long.class, ...)` 不是
   在保护任何外部可观察属性——外部可观察的只有序列化出来的那串字节。冻内部表示的断言
   在实现正确化时会变红，产生「修对了反而红」的信号反转。

5. **`FixtureTest` 缺陷的根因**：共享 fixture 的两个 runner 各自实现了比较函数，且宽松度
   不同。Go 的 `%v` 抹掉 int/float 之分，Java 只对标量做数值比较、容器落字符串化。
   宽松的那一侧长期绿灯，让 fixture 看起来是三方共用的同一道门，实际上门槛不一样高。


### 第二处根因（审计第一轮才暴露）：格式化器按**宿主语言的装箱类型**分派

去掉窄化后，Lua 产出的整数值改以 `Double` 到达，暴露出 `GoFormat.sprint` 按装箱类型分派：
`Long`/`Integer` 直接返回 `Long.toString`，只有 `Double` 分支应用 Go 的「≥ 1e6 切 `%g`」规则。
后果是**同一次比较的两侧走了不同规则**——`filter_condition` 的 `value: 2000000`（Jackson 解成
`Integer` → `"2000000"`）不再匹配 Lua 产出的 2000000（`Double` → `"2e+06"`）：Go 与 pine-cpp
都过滤掉该 item，Java 留着。**原来的窄化把两侧都变成整数装箱，偶然掩盖了这个不对称。**

我第一次修错了：保留了「1e6 以上仍按整数装箱输出」的分支，理由是整数装箱有精确十进制形式。
实测仍分歧——问题不在精度，而在**两侧是否遵循同一套规则**。

**Java 的装箱类型追踪的是「值从哪里解析来的」，不是「参照运行时认为它的静态类型是什么」**，
所以不能充当后者的代理。这是本次两处根因共同的形状：**把宿主语言的类型系统当成跨运行时契约的代理**。
窄化那处是 `long` 冒充「整数」，`sprint` 那处是装箱类型冒充「静态类型」。

审计第二轮进一步指出：我给这处写的注释断言「Go 没有整数分支」是**假的**——`transform_size` 写的
`in.ItemCount()` 是真 Go `int`、不经 `encoding/json`，Go 的 `%v` 对它原样打印。那条路径上
pine-cpp 与 pine-java 一致而 **Go 是异类**，已记入 `memory/doc-gaps.md`。注释已改为陈述真实理由：
Java 无法重建那个区分，因为装箱类型不是静态类型的代理。
## Missing Docs or Signals

- **`reference/number-formatting-parity.md` 没写自己的前置条件**。它只管「double 怎么拼」，
  前提是值以 double 到达格式化器。「值在到达之前被换成别的类型」这个失效面既不在这篇里，
  也不在任何别处，本次就是这一类。
- **`guides/cross-layer-validation.md` 第 8 节列了各层比对器行为**（Go operator-fixture
  用 `%v`、Java `FixtureTest` 对非 Number 字符串化），但只把它当成「该层能钉住什么属性」
  来讲，**没讲「同一份 fixture 被两个宽松度不同的 runner 读」这件事本身是隐藏变量**——
  宽松的一侧通过不代表契约成立，且严格的一侧会因为无关的表示变更而炸。
- **没有任何地方写「断言不要冻装箱类型」**。#175 用 `assertInstanceOf` 是对的（它要保护
  string-vs-number 类型身份），但没有边界说明：哪些 class 断言在保护契约、哪些只是在
  冻内部表示。结果 number 分支的 `Long` 被顺带冻住。
- **fuzz 失败缺复现命令**（本次已实现）。原来 seed 只出现在开头 header，`FAIL:` 行附近
  没有；保存的 divergence 目录在 `/tmp` 会被回收。**#190 能被诊断纯粹是靠那份残留副本。**
- **「长跑 fuzz 期间禁止触碰构建产物」没有落点**。`guides/benchmark-hygiene.md` 有 bench
  的同类纪律（zombie 进程、并行污染），fuzz 侧没有对应条目。

## Promotion Candidates

- **进 `must/conventions.md`（紧邻「跨运行时缺陷动手前必须实测受影响面」）：修一个函数里
  的缺陷时，检查维度要覆盖该函数的所有职责，不只是当前 issue 的那一维。** 审计结论要
  写成「函数 F 在维度 D 上已闭环」，不能写成「函数 F 已闭环」。与既有的「一条声明的每份
  副本都要清理」（#183 `/stats`、#179 五处注释）是**不同失效模式**：那条是同一维度多个
  位置，这条是同一位置多个维度。案例：#175 扫的是 `is*()` 派发谓词、漏的是三行外的
  `(long) d` 强转，且当时明文写下「下次触碰不需额外扫」。

- **进 `reference/number-formatting-parity.md`：补一条作用域声明。** 本篇只覆盖「double
  如何变成字符串」，前提是值以 double 到达格式化器；**值在到达格式化器之前被换成另一种
  类型**是本篇覆盖不到的失效面（issue #189/#190：`fromLua` 把整数值 double 窄化成 `Long`，
  Jackson 直接打印精确整数、根本不走浮点路径）。并写下跨运行时事实：三方 Lua bridge 的
  number 出口一律是 double，无整数分支（`pool_gopher_lua.go:475` / `pool_wangshu.go:508`
  / `lua_bridge.cpp:204`），`Long` 不是契约。

- **进 `guides/cross-layer-validation.md`（第 8 节扩写）：跨运行时共享 fixture 时，各
  runner 的比较宽松度是隐藏变量。** Go fixture runner 用 `fmt.Sprintf("%v")`（int/float
  不可分）、Java `FixtureTest.assertValueEquals` 对 `Number` 数值比较但容器落
  `String.valueOf`（`Double.toString` 带 `.0`）。同一份 fixture 因此不是同一道门：宽松侧
  长期绿灯（Go 从未看见本次问题），严格侧会因无关的表示变更而炸。判据：**共享 fixture 上
  的失败，先判断是值不同还是两个 runner 的比较口径不同**；后者要修比较函数，不是改期望值。

- **进 `guides/ci-quality-baseline.md`（differential-fuzz 节）两条**：
  - **fuzz 失败必须自带复现命令**（已实现）：`REPRODUCE:` 两行，失败/unstable round 号
    列表 + 可直接粘贴的完整命令。理由：seed 是每次随机、`/tmp` 的 divergence 目录会被
    回收，#190 能被诊断靠的是残留副本。
  - **长跑 differential-fuzz 期间不得触碰任何构建产物**：本次并行重编译 Java 产生 8 个
    假分歧（`java_rc=1` + 空输出，读到半写的 `target/classes`），一度被误判为新引入的
    缺陷。必须改代码就先停 fuzz，或用独立构建目录/快照。与 `guides/benchmark-hygiene.md`
    的 bench 噪声纪律同族。

- **进 `guides/investigation-to-fix-testing.md`：断言装箱类型 vs 断言契约。** 类型断言
  （`assertInstanceOf`）只有在被断言的 class 本身是外部可观察契约时才成立；否则它冻的是
  内部表示，实现正确化时会变红，产生「修对了反而红」的信号反转。本次六处 `Long` 断言部分
  来自 #175——那次用 `assertInstanceOf` 保护 string-vs-number 类型身份是对的，但把
  number 分支的 box 类型一起冻了，超出了要保护的属性。判据：写类型断言时说明**这个 class
  为什么是契约**；不能说明就改断值与序列化形式。

- 仅留 memory：`14_integral_float_above_2pow53.json` 的取值理由（2^62 正负、2^53 边界及
  其第一个可表示步长 2^53+2、小整数 42 与小数 1.5 作对照）；它与 #180 的
  `06_number_format_regimes` 是两个方向——那条覆盖「整数展开 vs 科学计数法」，本条覆盖
  「同一 float64 的两种**整数**拼写」。检索到本篇即可。

## Follow-up

- 由 recorder 落地上面五处稳定文档修改（`must/conventions.md` 维度条、
  `reference/number-formatting-parity.md` 作用域声明、`guides/cross-layer-validation.md`
  比较宽松度、`guides/ci-quality-baseline.md` 两条、
  `guides/investigation-to-fix-testing.md` 断言边界），并同步 `index.md`。
- 反查同族：其余两个运行时的 bridge 出口已确认无整数分支；但**仓库里其他「按值域选表示」
  的转换点**没有系统扫过（凡是 `if (整数值) return 整型` 形态的都可能有同一问题）。
  建议下次触碰序列化路径时 grep 一遍 `(long)` / `longValue()` / `static_cast<int64_t>`
  在数据路径上的用法。
- `TransformByLuaTypeIdentityTest` 里现存的 `assertInstanceOf` 还剩 string 分支若干，
  那些**是**契约（`"42"` 必须是 `String`），保留；不要顺手一起改掉。


**一处被接受的回归，写在这里而不是只写「全绿」**：审计第三轮查出 `a39a950d` 在
`transform_size` → `filter_truncate` 的 `top_n: "{{n}}"` 这条路径上，把 pine-java 从「与 Go 一致」
翻到了「与 pine-cpp 一致、与 Go 分歧」——base 上 `sprint(Integer 1000000)` 给 `1000000`（同 Go），
现在给 `1e+06`，于是 ≥ 1e6 的 item 数会报 `cannot coerce "1e+06" to int64` 而 Go 成功。

**两侧无法同时与 Go 一致**：保留装箱类型分支会打破 `filter_condition`，只在 ≥ 1e6 保留同样重新引入
不对称（两种都实测）。选了可达性高得多的 `filter_condition`。教训不在于选得对不对，而在于**我原先
把这个结果写成了「审计发现的既存缺口」**——同一份 doc-gaps 里负零那条明确标了「先于本 range 存在」
并给了 base commit，说明我知道怎么区分，这一条却没标。**接受一个回归是可以的，把它记成不是自己造成的
不行。** 现已改正，并加了 `integralCountAboveOneMillionUsesScientificForm` 钉住当前答案。
## 验证情况（本次已完成）

- 用 #189 原 seed（1655185644）三引擎跑 7010 轮 → **7010 PASS / 0 FAIL**（原第 7005 轮
  分歧）
- #190 保存的用例：三方响应体字节一致
- 新 fixture `fixtures/server_byte_exact/14_integral_float_above_2pow53.json`：
  byte-exact 13/13 两对
- Java / C++ / Go 全套测试、`make lint`、`make codegen-check`、
  `make cross-validate` 57 PASS、`make differential-fuzz` 1000/1000
- mutation 双向验证：把窄化改回去 → 新 fixture 与新单测都变红
- 探针对照 `formatJsonNumber` 与旧 long 路径在 42/0/−1/1e15/2^53/2^62/负 2^62/1.5 上
  同串（证明 2^53 以下零变化）
