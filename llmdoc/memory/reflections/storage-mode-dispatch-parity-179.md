# [storage_mode 分派规则跨运行时对齐（issue #179）]

分支 `fix/179-storage-mode-fallback`（基于 `origin/master` = `39ef8d3f`），单 commit
`90982071`。本次关闭 `doc-gaps.md` 里「`storage_mode` 非法值兜底跨运行时分歧」条目，但
留下一个残留决策项（fail-fast）。

本篇的价值不在「修了个分派分支」，而在**哪一类属性根本不能靠跨运行时通道钉住**，以及
由此推出的「门只能放哪里」。

## Task

修 issue #179：`storage_mode` 非法值在三个运行时选中不同的物理存储。任务目标是让
pine-java 与 pine-cpp 对齐 pine-go 的行为（即 issue 给的两个选项里选「统一兜底」那个），
并补上能钉住这条规则的回归门。

## Expected vs Actual

- Expected：三处分派点各改一行精确匹配，补三份单测。
- Actual：改动本身确实小（实现侧 pine-java `Frame.create` + pine-cpp `make_frame` 各一处），
  但两件事超出预期：
  1. **既存的 C++ 单测在主动断言错误行为**，改对实现后它变红——仓库里有一条测试把分歧
     钉住了，还附注释解释它。
  2. **参考实现（pine-go）自己的 `default` 分支完全没有测试**。定义这条契约的那一侧没被
     钉住，另两侧各自漂了不同方向。

## 分派规则（三方现状，本次统一后）

pine-go `NewFrame` 是 `switch` + `default: newRowFrame`，所以**只有字面量 `"column"`
精确匹配才走列存**，其余一律行存。修改前另两侧各自不同、且方向不同：

| 运行时 | 修改前 | `"Column"` | `"colunm"` |
|---|---|---|---|
| pine-go | `switch` + `default: newRowFrame` | row（基准） | row（基准） |
| pine-java | `"column".equalsIgnoreCase(storageMode)` | **column** ≠ Go | row |
| pine-cpp | `if (storage_mode == "row") ... else ColumnFrame` | **column** ≠ Go | **column** ≠ Go |

两侧都改成 `== "column"` / `"column".equals(...)`、其余落 row。

## What Went Wrong

### 1. 这个分歧对所有端到端通道结构性不可见 —— 这决定了门只能放在单测

两件事实都是实测确认的，不是推断：

- 七个 `storage_mode` 变体（`row` / `column` / `colunm` / `Column` / `COLUMN` / 空 /
  `unknown`）在三个运行时上输出**逐字节相同**。这不是巧合：行列存输出对等本来就是设计
  契约，cross-validate section 4 就在断言它。
- `/stats` 与 `/dag` **都不含任何 storage 字段**（起 server curl 实测过）。

于是：**没有任何从进程外部可观察的信号能区分选中了哪个 store**。分歧只改变内存与性能
形状，不改变任何一个响应字节。这直接推出结论——回归门只能落在各运行时自己的 factory
单测上，任何跨运行时通道都放不住它。

这条推理是「先问哪条通道能看见这个属性」那条纪律的一次正面应用（上一个任务 #183 把它
写进了 `guides/ci-quality-baseline.md`）。区别在于：#183 是修完才发现通道看不见，这次是
动手前先算清可见性、再决定门放哪。

### 2. 既存单测把分歧钉住了，而不是抓住它

`pine-cpp/tests/test_row_frame.cpp` 原有一行：

```cpp
CHECK(dynamic_cast<RowFrame*>(fallback.get()) == nullptr);  // defaults to column
```

改对实现后这条用例**变红**。也就是说仓库里有一条测试在断言错误行为，还带注释解释为什么
应该这样。这比「没有测试」更糟：**它让改对的人以为自己改坏了**，第一反应会是把实现改回去。

教训：**发现某条既存断言与参考实现相反时，先确认参考实现是哪一方，不要顺着测试改回去。**
判据是「这条断言引用的那个运行时，实测行为是什么」，而不是「注释怎么说」。

### 3. 参考实现自己没被钉住

`NewFrame` 的 `default: newRowFrame` 是这条契约的**定义方**，但
`pine-go/internal/dataframe/` 下没有任何用例覆盖非法值走 default。规则只隐含在 switch
的结构里。这就是「另两侧各漂一个方向」能长期存在的条件之一：谁都没有一个可引用的
断言点。

本次给三方补了单测，同一组 12 个值（含两个真实分歧过的 `"Column"` / `"colunm"`，另有
带空格、复数、截断等形式），每条都做了 mutation 验证会红。

### 4. 三处注释同时说反话，其中一处在被修的那个文件里活下来了

`row_frame.cpp:425` 与 `include/pine/frame.hpp:114` 都写着 "Unknown values fall back to
`column` — mirrors pine-go NewFrame behavior"，而 pine-go 的兜底是 **row**。这条注释既与
自己实现的意图相反，也与它引用的那个运行时相反——是一条双向错的注释，本次都改了。

**但 `frame.hpp:24-25` 的类文件头注释里还有第三份同样的错误表述**（"storage_mode falls
back to `column` when unrecognised"）——初版没改到，而它就在被修的同一个文件里、只差 90 行。

**已由 `0ac95035`（与本篇同一次提交）修掉**，HEAD 上 `frame.hpp` 已是正确表述。补注是审计第三轮
要求的：第二轮曾把这条说明插进上面那句引文的中间，导致同一句话里「已修」与「未改」并存、引文被腰斩。
根因是按 grep 命中的「分派点附近注释」清理，没有对同一文件通读一遍。

教训：**修一条错误表述时，先在同文件内搜完这条表述的所有拷贝再收工**——同一个错误声明
在一个文件里出现两次是常见的（文件头概览 + 函数处说明）。这与 #183「`/stats` 漏两轮」
同型：都是按「issue 举的那个点」清理，而不是按「这条声明出现在哪里」清理。

### 5. 新增的 cross-validate section 21 抓不到本次修的缺陷，脚本注释里明说了

section 21 只能钉「非法值被静默接受 + 三方输出字节相同」。实测过：把 C++ 的 fallback 改
回去，section 21 **仍然全绿 7/7**。脚本注释显式写了这一点、写了为什么（输出对等 + 无可
观察信号）、以及真正的门在哪三个单测文件里。

这是 #183 反复吃到的教训的正面应用：那次有一条 gate 既抓不到缺陷、又会对正确输出误报，
最后被删掉。这次是**先承认通道的边界，再把门放到能放的地方**，而不是留一个看起来覆盖了、
其实恒绿的检查。section 21 仍然有价值，但价值不是「抓 #179」：它守的是「未来某一侧单独
加上『拒绝非法值』时会立刻红」这条负空间。

### 6. 保留静默兜底而非 fail-fast，理由与残留决策

issue #179 自己倾向「在各运行时配置加载层显式拒绝非法值（fail fast）」。本次**没有**这么做：
Go 的 `default` 分支接受一切，只在 Java/C++ 侧拒绝就是引入一个新的跨运行时分歧。fail-fast
需要三方同时改（含 Go），属于独立决策，应该留在 doc-gaps 里而不是在一个对齐任务里悄悄做掉。

补一个本次查到的上下文：**Apple DSL 侧已经有 fail-fast**——`apple/flow.py:369` 的
`_VALID_STORAGE_MODES` 校验在编译期就拒绝非法值。所以分歧只对**手写 JSON 配置**成立。
这既解释了它为何长期没露头（走 DSL 生成的配置永远合法），也说明运行时层的 fail-fast 是第二
道防线而非唯一防线——这一点应该写进将来那个 fail-fast 决策的输入里。

### 7. 正面对比：这次范围判断对了

前两个任务（#180、#183）都栽在「按 issue 标题假定受影响范围」：#180 范围被说小一个量级，
#183 被说大（`/execute` 上只有一侧错）又同时说小（漏了 `/stats`）。这次 issue 已经把三方
分派点和实测结果列全了，动手前仍然把三处实现读了一遍并端到端实测确认，结论与 issue 一致、
没有额外位置。

值得记的判据：**issue 写得细并不免除实测，但实测确认之后就可以按它的范围推进**，不必每次
都假定范围被说小了。上一条纪律是「issue 标题不是范围声明」，本次补上它的另一半：实测是
用来确认或推翻范围的，确认了就往前走。

## Root Cause

1. **契约规则隐含在语言结构里（`switch` 的 `default` 分支），没有断言点也没有文档落点。**
   隐含规则无法被引用，复刻方只能各自解读「兜底应该是什么」，于是解读出两个不同方向。
2. **属性的可观察面为空，所有跨运行时通道结构性看不见它。** 行列存输出对等是设计契约
   （section 4 断言），这条契约恰好把分派分歧完全吸收掉，没有端点导出 storage 字段。
   这类属性的门只能放在实现内部。
3. **既存测试把错误行为固化了**，附带注释提供了错误的合理化解释，抬高了发现成本并给
   「改对」制造反向信号。
4. **同一错误声明在同一文件里有多份拷贝**，按分派点邻域清理漏掉了文件头那份。

## Missing Docs or Signals

- 没有任何稳定文档记录「只有精确 `"column"` 走列存、其余落 row」。`architecture/dag-engine.md:465`
  只写了「`"row"` 或 `"column"`，默认 `"row"`」，没有说非法值怎么办——而这正是分歧点。
- 没有文档记录「行列存输出对等（section 4）意味着分派缺陷对端到端不可见」。这条推理每次
  碰 storage 相关缺陷都要重做一遍。
- `doc/guide_pipeline{,-en}.md` 说「`storage_mode` 只接受 `"row"` 和 `"column"`，其他值在
  编译期就会被拒绝」——对 Apple DSL 路径是准确的，但用户读到的是全局声明，而手写 JSON
  路径并不拒绝、只是静默落 row。这个层次差别没有落点。
- 缺一条通用纪律：**既存断言与参考实现相反时的处置顺序**（先定基准，再决定改哪边）。

## Promotion Candidates

- **进 `guides/ci-quality-baseline.md`**，与 #183 那两条纪律并列：
  - 「某些属性的外部可观察面为空（输出对等契约把它吸收掉、无端点导出），此时门只能放在
    各运行时实现内部的单测上；跨运行时通道对这类属性恒绿」。判据：先问「哪条通道能看见这个
    属性」，答案是「没有」就不要试图在通道层建门。
  - 「新增校验段若抓不到本次修的缺陷，必须在脚本注释里写明它抓不到什么、为什么、真正的门在
    哪」。section 21 是正面样本，可以直接引用它的注释形式。
- **进 `guides/` 或 `must/`（通用教训）**：既存断言与参考实现相反时，先确认参考实现是哪一方，
  不要顺着测试把实现改回去；配套一条「修错误注释时先在同文件内搜完这条表述的所有拷贝」
  （本次 `frame.hpp` 头注释漏改是直接教训，与 #183 `/stats` 漏两轮同型）。
- **需要稳定文档落点**：`storage_mode` 三方分派规则本身（只有精确 `"column"` 走列存、其余 row；
  Apple DSL 层编译期拒绝非法值、运行时层静默兜底，两层语义不同）。建议扩
  `architecture/dag-engine.md:465` 那节，或与 `decisions/user-docs-no-perf-multipliers.md` 里
  `storage_mode` 文档落点一并处理。
- **`doc-gaps.md` 的 #179 条目：关闭**（结论落上一条稳定文档），但必须留下残留项：
  **「运行时层 fail-fast 拒绝非法 `storage_mode` 仍是未做的独立决策」**——需要三方同时改（含
  pine-go），输入包括「Apple DSL 已有编译期校验，运行时是第二道防线」这一事实，以及
  section 21 会在只改一侧时立刻变红。

## Follow-up

1. ~~修掉 `pine-cpp/include/pine/frame.hpp:24-25` 残留的第三份反向表述（"falls back to
   `column` when unrecognised"）。~~ **已完成**（`0ac95035`，与本篇同一次提交）。这条 follow-up
   写下时就已经过时——审计第二、三轮各抓了它一次，第一次的修法还把说明插进了引文中间。
2. 调 `recorder` 落地上述稳定文档改动：dag-engine.md 分派规则、ci-quality-baseline 两条纪律、
   doc-gaps 关 #179 并写入 fail-fast 残留项。
3. 评估 `doc/guide_pipeline{,-en}.md` 那句「其他值在编译期就会被拒绝」是否要补一句手写 JSON
   路径的行为（属用户可见契约，但改动前应确认要不要把 fail-fast 决策一起定下来，避免文档写
   一遍再改一遍）。

## 验证情况（本次已完成）

- 三方定向探针：七个 `storage_mode` 变体输出逐字节相同；`/stats`、`/dag` 实测无 storage 字段
- `make lint` / `make codegen-check` / `make test` 全过
- `make java-test`、`make cpp-test`、`go test ./...` 全过（**用例数不写死**——#183 复盘里这个
  数字在写下的那次提交就已过期；要数就跑命令）
- `make cross-validate` 全过（新增 section 21）；`make differential-fuzz` 1000/1000
  （row/column 两种形状都覆盖到）
- mutation 四组：Java 改回 `equalsIgnoreCase` ⇒ `everythingElseSelectsTheRowStore` 红；
  C++ 改回 column 兜底 ⇒ `test_row_frame.cpp` 两处红；Go 翻转 `default` 分支 ⇒ 新增 Go 用例红；
  **C++ 改回后 section 21 仍绿** —— 证实了脚本注释里那条自陈
