# [metrics.Provider 契约定义与 Help 文案补门（issue #193）]

分支 `fix/193-metrics-provider-contract`（基于 `master` = `733094e5`），单 commit `67890029`。
**文件数与增删行数不在此复述**——要数就跑 `git show --stat 67890029`。

本篇的价值不在「补了一堆注释」，而在三条方法论：

1. **错误的问题陈述会导向正确执行的错误修法**——原 issue 把定位写在「扩展点缺少说明」上，
   而那件事早就有文档；按错定位修会做重且修不到真缺口。
2. **无人可见的属性必然腐烂**——`# HELP` 从不进入任何输出、无出厂 provider 消费，于是 15 处
   分歧悄悄累积到无人察觉。
3. **先分清有意设计再判缺陷**——六条候选「缺陷」里四条一旦承认是有意设计就塌缩成一条「缺文档」。

## Task

给 `metrics.Provider` 这条面向下游的集成缝补齐契约，并处理 issue #193 点名的 Help 文案分歧。

关键前提在动手前就定下来了：**内置 `Collector` 只聚合 count + sum、默认 provider 丢弃一切，
两者都是有意设计，不是缺陷。** 真正缺的是接口从未说明下游必须知道什么，以及一处文档断言了
不存在的保障。

## Expected vs Actual

- Expected（issue 原定位）：扩展点缺少说明，补一节讲「这里有个扩展点」。
- Actual：**这个定位是错的。** `doc/api.md:76` 与 `design_doc/08_observability.md:265-315` 早有
  用户面文档和约 80 行参考实现。真正的空白是 histogram/buckets 那一支（示例把
  `NewHistogram` 省略成「类似…」，而它恰是唯一涉及桶与单位的方法）和**并发语义**。

- Expected（Help 分歧）：改 issue 点名的 5 条 operator 级 Help，server 层「很可能同型」。
- Actual：扫全部 14 条 `pine_*` 后实测 **15 处分歧**——pine-java 九条 engine metrics 全不同、
  外加 server 层五条；**pine-cpp 也有一条不同（`pine_dag_operators_executed`），而 issue
  明确判为「Go/C++ 一致」**。全部对齐 pine-go。

- Expected（失效断言）：以为有两处文档断言假保障。
- Actual：实测只有一处（`llmdoc/reference/metrics-observability.md:35`）。另两处
  （`design_doc/08_observability.md:266` 的 section-16 断言、`guides/ci-quality-baseline.md:378`
  的 section-13 描述）**核对后准确**，已在 commit message 里明确记为「准确、不必重查」。

## What Went Wrong

### 1. issue 自己写窄了，而写它的人是我（连续第五次）

issue 说「5 条 operator 级、server 层未比对、很可能同型」，实测 15 处，且含一条 issue 明确
判为一致的。这是 `must/conventions.md`「跨运行时缺陷动手前必须实测受影响面」的又一次复现，
**本次的「症状 vs 机制」分野特别清楚**，可以直接当那条纪律的教学例子：

- 症状是「Java 的 Help 比 Go 短」
- 机制是「三方各自独立声明 Help 字符串，且没有任何通道能看见它」

按症状修 → 只改 issue 点名的 5 条。按机制修 → 扫全部 14 条 **并补一道门**，因为机制本身
（各自声明 + 无人可见）在修完这一批之后仍然成立。

### 2. 一处文档断言了不存在的保障

`metrics-observability.md:35` 原文：同名指标**与桶**通过 cross-validate metrics-parity section
保证一致。实测：桶没有任何守护。`scripts/cross-validate/13-metrics-parity.sh` 读的是 `/stats`，
数据由 `runtime.Stats` 供给，与 `metrics.Provider` 是**分离的两套机制**；它断言的是「算子从
启动即可见且计数为零」这类 `Stats` 行为，与 histogram 的桶无关。三方桶数组目前逐值相同，但
那是源码巧合。

处理：改为陈述事实，并把能力边界补进**既有的**「双通道观测模型」节，不新开一个竞争性的节。

### 3. 并发是唯一的正确性问题，而三方接口一个字没提

`Observe` / `Inc` / `Set` / `Add` / `With` 会被并发调用——`pine-go/internal/runtime/scheduler.go`
的 `go func(idx int)`（写本篇时在第 108 行）并行执行算子，各自写同一 metric。三方接口 grep
`thread|concurrent|goroutine|safe` **全空**。

危险的形状是：**出厂两个实现恰好安全**（`collectorHistogram.Observe` 全程持 `c.mu`、`Nop`
无状态），所以下游写出竞争的 Provider 时，不会被仓库自己的任何测试抓到，失败方式是静默
data race。design_doc 里的 promadapter 示例能跑，纯粹因为 `prometheus.CounterVec` 本身并发
安全——**那是运气，不是契约**。

### 4. 两次自己的脚本 bug

- 批量编辑脚本一处 tuple unpacking 写错，脚本抛错退出、什么都没写。**`assert` 在前救了它**，
  没造成半改状态。教训：批量编辑脚本本身也该先自测，不要把「它要么全做要么不做」寄托在
  运气上。
- 一次 ruff E501 被 pre-commit 拦下。这条是正面记录：pre-commit 在起作用。

## Root Cause

1. **错误的问题陈述会导向正确执行的错误修法。** 原 issue 的定位（「扩展点缺少说明」）读起来
   合理、可执行、也能做出交付物，但做完之后真缺口一个都没补上。grilling 把定位改了两次，
   **比修复本身更有价值**——价值恰在动手之前把问题陈述拧对。
2. **一条跨运行时声明如果没有任何通道能看见它，它必然腐烂。** `# HELP` 不进任何输出（仓库
   不产出 Prometheus 文本格式）、无出厂 provider 读 `Help`（实测计数为 0），所以三方各自
   声明的字符串各自漂移，累积到 15 处而无人察觉。
3. **契约权威没有落点，于是三份副本各自漂移。** 四件签名看不出来的事（并发、duration 单位
   是秒、`HistogramOpts.Buckets` 只是建议且无出厂 provider 读它、`With` 在启动预热时可能不
   跟随 `Observe`）在三方都没写。
4. **「有意设计」和「缺陷」没先分清。** 默认 nop、collector 不读 buckets 曾被我当成缺陷写进
   issue，实际都是有意的（nop 注释明写 `zero overhead`、collector 从设计上就不是直方图后端）。
   混在一起会让工作量与风险都虚高。

## 本次的做法（值得复用的形状）

- **契约权威落在 `pine-go/pkg/metrics/metrics.go` 的 package doc**（"Implementer's contract"
  一节），pine-java / pine-cpp 的接口注释与 `design_doc/08_observability.md` 只留指针、不复述，
  于是副本无法漂移。这是把 `must/conventions.md` 已有的 codegen 单向对齐模式（Go 是 source of
  truth）**套用到接口契约文档上**。
- **并发那一条在三方各自重复一次**，理由写在注释里：它影响正确性而非精度，值得付这份重复
  的代价；其余三条只留指针。
- **`scripts/check-metrics-help-parity.py` 接入 `make lint`**：纯文本扫描、不需构建，pine-go
  是 source of truth。mutation 验证过——注入分歧变红、恢复变绿、`make lint` 端到端也验过。
  脚本自带一条 fail-fast：pine-go 侧扫不到任何 `pine_*` 时直接失败并提示「声明形状是不是变了」，
  防止正则失配退化成恒绿。
- **顺带核对的两处准确断言写进 commit message**，标注「已核对、下一个读者不必重推」。这比
  沉默地放过它们更省下游成本。
- 补完 promadapter 示例的 `NewHistogram`；用户面 `doc/api.md` / `api-en.md` 加指针不复述；
  pine-java metrics 包 7 个零注释文件全部补上（含 4 个核心接口 `Counter` / `Gauge` /
  `Histogram` / `Provider`）。

## Missing Docs or Signals

- **没有任何纪律写下「无人可见的属性必须补一个能看见它的检查，否则不要声称它一致」。**
  `guides/ci-quality-baseline.md` 已有的「先确定属性有没有外部可观察信号，再决定门放哪一层」
  （#179）只讲了一半：属性不可观察时**不要**在跨运行时通道上钉它。本次是缺的那另一半：
  不可观察**不等于**不需要门，门可以下沉到更便宜的层（源码文本扫描接 lint，代价极低）。
  两条应写成一对，不要各自孤立。
- **`must/conventions.md` 的 codegen 单向对齐那条只写了 codegen**，没写这个模式对接口契约
  文档同样适用。本次是第一个 codegen 之外的应用点。
- **没有文档写下「先分清有意设计再判缺陷」是审计的第一步。** 本次六条候选缺陷里四条塌缩为
  一条缺文档，靠的是承认「默认 nop / collector 不读 buckets」是设计意图。
- **文档里「由 X 保证 / 由 section N 锁定」这类断言没有任何核对纪律。** 本次一假两真，真的
  那两处都能追到具体 section 与其断言内容；假的那处名字看起来相关（metrics-parity），实际
  断言的是另一套机制。

## Promotion Candidates

- **进 `guides/ci-quality-baseline.md`，紧邻既有的「先确定属性有没有外部可观察信号，再决定门
  放哪一层」写成一对**：**无人可见的属性必然腐烂。** 判据——一条跨运行时声明如果没有任何通道
  能看见它，要么补一个能看见它的检查，要么不要声称它一致。实测依据用本次：`# HELP` 从不出现
  在任何输出、无出厂 provider 消费，15 处分歧悄悄累积；补门的代价是一个纯文本扫描脚本接
  `make lint`（不需构建）。与 #179 那条的关系必须写清：#179 是「不可观察 → 别在跨运行时通道上
  钉」，本条是「不可观察 → 门下沉到更便宜的层，而不是不设门」。
- **进 `must/conventions.md` 的「Codegen 单向对齐方向」条目做一句扩展**：source-of-truth 单向
  对齐**不只适用于 codegen**。本次把同一模式用在**接口契约文档**上——`pine-go/pkg/metrics/metrics.go`
  的 package doc 是唯一权威副本，pine-java / pine-cpp / design_doc 只留指针、不复述，所以副本
  无法漂移。例外写清：影响正确性而非精度的那一条（并发）刻意在三方各重复一次。
- **作为 `must/conventions.md`「跨运行时缺陷动手前必须实测受影响面」的补充例证，不要新开一条**：
  本次「症状 = Java Help 短 / 机制 = 三方各自声明 + 无人可见」的分野特别干净，且按症状修与按
  机制修会产生结构不同的两种交付物（改 5 条 vs 扫 14 条并补门）。清单可从四次同型扩到五次
  （#180 / #183 / #179 / #187 / #193），且**#187 与 #193 两次都是自己写的 issue**。
- **进 `guides/investigation-to-fix-testing.md`**：两条并列，都关于「动手之前」。
  - **文档里「由 X 保证 / 由 section N 锁定」这类断言必须能被追到具体脚本行。** 判据：写下这类
    断言（或读到它并打算依赖它）时，打开那个 section 确认它真的断言了这个属性，而不是名字看
    起来相关。本次 `metrics-parity` 名字完全相关、断言的却是另一套机制（`/stats` ←
    `runtime.Stats`）。
  - **grilling 先于动手，尤其当 issue 是自己写的。** 自己写的 issue 最容易带着自己的错误前提，
    而错误的问题陈述会导向**正确执行的错误修法**——那种失败最难发现，因为交付物看起来是完成的。
    本次 grilling 把定位改了两次，比修复本身更有价值。
- **进 `guides/investigation-to-fix-testing.md` 或 `must/conventions.md`（偏窄，可与上一条合并）**：
  **审计的第一步是分清「有意设计」与「缺陷」。** 判据：一个「缺陷」如果在源码注释或设计文档里
  有明确的设计意图声明（本次是 nop 的 `zero overhead`、collector 的非直方图后端定位），它就不是
  缺陷，缺的是文档。本次六条候选里四条这样塌缩，工作量与风险大幅下降。**先分清能避免修掉正确
  的东西。**
- **稳定文档已在本次 commit 内同步，recorder 无需重做**：`llmdoc/reference/metrics-observability.md`
  的失效断言已改、能力边界已补进既有「双通道观测模型」节。recorder 侧只需按上面几条更新
  `guides/` 与 `must/`。

## Follow-up

1. 调 `recorder` 落地上述 `guides/` 与 `must/` 改动，重点是「无人可见的属性必然腐烂」与 #179
   那条写成一对，以及 codegen 单向对齐扩展到接口契约文档。
2. `must/conventions.md`「实测受影响面」条目的同型清单从四次更新到五次（加 #193），并把
   「症状 vs 机制」的本次例子写进去。
3. 若将来新增 `pine_*` 指标：`scripts/check-metrics-help-parity.py` 会自动覆盖（前提是声明写法能被正则命中；不能命中时由条数下限与对称性检查报错，而不是静默放过）（按 `pine_[a-z0-9_]+`
   正则扫源码），但**改动指标声明的写法**（Go `Name:`/`Help:` 相邻、Java 位置参数、C++ designated
   initializer 花括号）会让对应正则失配。Go 侧失配当时只有「零命中」保护，审计第二轮证明这远远不够：**Go 的集合是基准**，少一条时对称性检查看不见，真实分歧随之消失。现已加 metric 条数下限断言，并让 Go 侧扫描先剥注释
   现已改为三道门：条数下限、双向名字集合对称、逐值比对，且扫描先剥行注释与块注释。
4. `pine-cpp` 的错误类型分层（`reference/metrics-observability.md` 已记的 known follow-up）与
   本次无关，仍开放。


## 补记：门在 llmdoc 更新阶段被扩展了一次

recorder 把「桶边界无门」记成了开放条目，理由是 `Help` 补了门而桶没有、且桶的后果更重（**每个运行时
建议得不一样，会让下游在三方之间算出的分位数不可比**）。这个判断对，但结论应当再往前一步：既然刚为
`Help` 建好了纯文本扫描的框架，把桶一起扫的边际成本接近零，没有理由留成待办。

于是同一个脚本扩展为同时比对桶数组，doc-gap 随即改为「已解决」。**这里的教训不是「顺手多做一点」，
而是「开放条目的成本估算要在已有基础设施之后做」**——同一件事在建门之前是一项新工作，在建门之后是
几行代码。

实现上那处「刻意的取舍」后来被审计推翻，值得留作教训：我最初比对的是「某运行时声明了哪一批桶数组」
这个集合，而不是把数组映射回 metric 名，理由写的是「三种语言写法差异会让名字关联变脆，而集合比对同样
能抓到漂移」。**后半句是假的**——审计构造出互换两个 metric 数组的 mutation：集合完全不变、每个 metric
的桶都错了、检查全绿。现为按名映射逐值比对，并对两个方向做名字集合对称性检查。

**教训**：给一个取舍写理由时，「同样能抓到」这类等价性声明必须自己先构造反例试一次。我当时只验证了
「改一个桶边界会变红」，没有验证「保持集合不变的分歧会不会变红」——前者是我期望它抓到的，后者才是
这个取舍真正放弃的东西。

顺带又踩了一次上一个任务记过的坑：给 `ci-quality-baseline.md` 改描述时，第一次 `.replace()` **匹配
不到任何东西、静默返回**，而我当时已经在往下走。是随后的 `grep` 核对发现的——这与 #189/#190 那次
「`.replace()` 大小写不匹配、静默无操作」是同一个失效方式。**判据不变且要真的执行：字符串替换之后
必须用独立的 grep 复核，不能相信替换本身。** 那次之后还留下一句已成假陈述的「桶边界目前无门」，
也是靠复核发现的。
## 验证情况（本次已完成）

- `make lint`（含新增的 metrics Help parity 检查）、`make codegen-check` 全过
- Go 测试套、pine-java 测试套、pine-cpp 测试套全过（**用例总数不在此复述**——写下的那次提交
  就会过期，要数就跑命令）
- `make cross-validate` 全 PASS
- `make differential-fuzz` 1000/1000
- mutation：给 pine-java 注入一处 Help 分歧 ⇒ `check-metrics-help-parity.py` 红、`make lint`
  端到端红；恢复后绿
