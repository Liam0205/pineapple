# [根级配置类型/值校验对等 + 字节级 fixture 按形状枚举（issue #187、#188）]

分支 `fix/187-188-storage-mode-failfast-byte-exact`（基于 `origin/master` = `9b52773a`），单
commit `b0dee3bb`。本次关闭 `doc-gaps.md` 两条开放条目：「运行时层 fail-fast 拒绝非法
`storage_mode`」（#179 残留项）与「字节级对等的校验通道覆盖面太窄」的 (a) 半。

本篇的价值不在「加了两个校验」，而在两条方法论：**issue 自己写的范围也可能是错的**（这已经是
连续第四次），以及**按响应形状枚举 fixture vs 等出事才补**——后者的第一个产出就是一个既存缺陷，
而不是一次确认。

## Task

- #187：给运行时层加 `storage_mode` fail-fast（这是 #179 刻意留下的残留决策项）。
- #188：扩 `fixtures/server_byte_exact/`，让「字节级对等」这个全局声明真的有覆盖面支撑。

## Expected vs Actual

- Expected（#187）：给三方各加一个值白名单，错误文案对齐，收工。
- Actual（#187）：**动手前实测发现类型层的分歧根本不是 `storage_mode` 专属的**，四个根级字符串
  字段全都有，且三方分布各不相同；写测试时又查出第四处分歧（pine-java 根本没有
  `_PINEAPPLE_CREATE_TIME` 这个字段）。范围从「一个字段的值校验」变成「四个字段的类型规则 +
  一个字段的值白名单」。
- Expected（#188）：补几个 fixture，全绿，把 doc-gaps 那条关掉。
- Actual（#188）：第一个新 fixture（`10_validation_error_envelope`）**立刻查出 pine-cpp 一处
  既存输出分歧**，本次一并修了。
- Expected（section 21）：#179 加的 section 21 微调即可。
- Actual：#187 把它断言的契约**反转**了（从「非法值被静默接受」变成「非法值被拒绝」），整段重写，
  并在重写时踩了一个 `set -e` 的坑。

## What Went Wrong

### 1. issue 写的范围比实测范围窄（连续第四次，且这次 issue 是我自己写的）

issue #187 把这件事描述成 `storage_mode` 的值校验问题。实测三方后，类型层的真实分布是：

| 字段 | pine-go | pine-java | pine-cpp |
|---|---|---|---|
| `storage_mode` | 全拒 | `asText()` 强转接受 | 拒（唯一调 throwing `as_string()`） |
| `log_prefix` | 全拒 | 强转接受 | **静默忽略**（`is_string()` 守卫） |
| `_PINEAPPLE_VERSION` | 全拒 | 强转接受 | 静默忽略 |
| `_PINEAPPLE_CREATE_TIME` | 全拒 | 强转接受 | 静默忽略 |

三条实测事实：

- **pine-go 全拒不是 `storage_mode` 的性质，是 `encoding/json` 的性质**——四个字段都声明成
  `string`，任何一个是数字/布尔/数组/对象都会让整份 unmarshal 失败。
- **pine-cpp 与自己不一致**：只有 `storage_mode` 走 throwing 的 `as_string()` 会抛
  `ConfigError`，另三个字段有 `is_string()` 守卫、静默忽略。
- **pine-java 凭空造值**：`asText()` 把 `123` 变成 `"123"`、把数组/对象变成 `""`。也就是说
  pine-go 直接拒绝的配置，在 pine-java 里带着一个编造出来的值跑起来了。

用户在三个方案里选了「统一所有根级字符串字段」。三方现在一律拒绝 present-but-wrong-typed 值；
**JSON `null` 接受并保持默认**，与 Go 一致（把 null 解到 string 字段是 no-op）。

这条教训的形式已经出现四次，每次只是投影方向不同：#180 范围被说小一个量级、#183 同时被说大和
说小、#179 issue 写得细但仍需实测确认（那次确认了）、本次 issue **是我自己写的**、仍然把范围写窄
了。共同点不是「issue 作者不了解代码」，而是**写 issue 时的视角是「症状出现在哪个字段」，而受影响
面的视角是「这个机制作用在哪些字段上」**，两者不是同一个集合。

### 2. 第四处分歧是穷举维度的测试逼出来的，不是读代码读出来的

`_PINEAPPLE_CREATE_TIME` 在 pine-java 里**根本不存在**。后果是不对称的：pine-go 会因为它类型错
而拒绝整份配置，pine-java 连这个字段都不解析、什么都不会发生。

发现路径值得单独记：这条**不是**通过读三方 config 解析代码发现的（读过，没看出来——「没有这个
字段」在阅读时表现为「没有那一行」，比「有一行写错了」难看见得多）。它是**写测试时按「4 字段 ×
6 种 JSON 类型」穷举矩阵，发现 pine-java 那一格无论填什么都不红**才暴露的。

推论：**当分歧的形式是「某一侧缺一整块」时，读代码对比不可靠，穷举维度的测试可靠。** 缺失在
阅读中是负空间，在矩阵里是一个空格子。

已补上 pine-java 侧的 `pineappleCreateTime`——纯 metadata、不参与任何行为，存在的唯一理由是让
类型规则在四个字段上统一。

### 3. 按形状枚举 fixture 的第一个产出是缺陷，不是确认

原有 8 个 byte_exact fixture **全部**是「出事才补」的（#180 补数字格式、#183 补三个转义）。也就是
说「字节级对等」这个全局声明，背后只有「已经出过事的那些形状」的覆盖。

新增 4 个按响应形状**事先枚举**：校验错误 envelope、深层嵌套（map-in-list-in-map）、null 值出现在
各个位置、过滤后多 item 投影。

`10_validation_error_envelope` 立刻红：pine-cpp 在校验错误时输出
`{"common":{},"items":[]}`，而 pine-go / pine-java 输出 `null`。根因是
`pine-cpp/src/server/server.cpp` 的 `execute_with_trace` 在执行后**无条件**把 `has_result` 置
true；pine-go 侧 `Execute` 在校验错误时返回 `nil, &ValidationError{}`，`resp.Common` / `Items`
为 nil，marshal 成 `null`。

修法是**只在校验错误分支清掉 `has_result` 与 `result`**，不把那次赋值整体移走——因为
**部分执行错误仍然必须带 result**（管道中途 `ExecutionError` 要带上失败前已写入的字段，
`02_partial_error_keeps_partial_result` 钉住这条）。两条路径的语义相反，是同一个字段承载的。

这就是「等出事才补」这种做法的代价的直接证据：它只覆盖已经出过事的形状，而**按形状枚举第一轮就
会撞到从没出过事、但一直错着的形状**。

### 4. section 21 因为契约反转而整段重写

section 21 是 #179 加的，断言「非法值被**静默接受**且三方输出字节相同」。#187 把这个契约反转了。
处理方式：整段重写为断言拒绝对等，**旧断言作为历史保留在注释里而不是删掉**——因为 #187 之前的
git 历史、注释和文档仍然那样描述，读到旧提交的人需要这个上下文。

同时保留了 #179 那条自陈：**仍然有效的那一半**（合法值走哪个物理存储，对进程外部依然不可观察）
写清楚门在哪三个单测；新增的那一半（拒绝对等）指向三个新的 validation 单测文件。

### 5. `set -e` 下故意跑必须失败的用例，未加保护的命令替换会静默中止整个脚本

`scripts/cross-validate/_env.sh` 设了 `set -e`，而重写后的 section 21 **故意**跑一批必须非零退出
的用例。未加保护的 `go_out=$(...)` 在第一个「预期失败」的用例上就直接中止整个脚本，**连 rc 都没
来得及读**。

表现极具误导性：脚本静默跑完、一条 pass/fail 都不打，看起来像是循环没进去或者变量拼错，而不是
像「有个命令失败了」。改成 `rc=0; out=$(...) || rc=$?` 之后正常。

判据：**一个断言「必须失败」的校验段，它的每一次命令替换都要带 `|| rc=$?`。** `set -e` 与
「非零退出是预期结果」这两件事天生冲突，而冲突的表现是静默失败而非报错。

### 6. Go 白名单常量重复定义（刻意，且必须记）

`pine-go/internal/config/types.go` 里的 `StorageModeRow` / `StorageModeColumn` 是**重复定义**，
不是从 `internal/dataframe` import 的——因为 `internal/dataframe` import `internal/config`，
反向 import 会成环。已在代码注释里写明「两处必须同步」。

这一条属于「结构约束逼出的重复」，与 #179 那条「三处分派点必须同时改」是同类账目：`storage_mode`
现在有**七个**解释点（三处分派 + 三处解析 + 一处值白名单），且白名单一侧的常量还是重复的。

### 7. 磁盘配额被前几轮审计的 scratch 副本打爆

跑测试时 `Disk quota exceeded`。原因是 **#180 / #183 / #179 三轮 blind review 遗留的 reviewer
scratch 副本共 17GB**。机制：每轮 blind review 的 reviewer 都被要求 `cp -a` 一份仓库快照到自己的
scratch 目录（因为外部清理进程会删原快照），而这些副本从来没有人清理。清掉 17GB 才跑得动测试。

量级换算：单份副本 1–1.7GB，#183 跑了 14 轮、#179 跑了 6 轮，累积就是这个数。这是
close-local-code-review 工作流的**运维成本**，不是偶发事故：**N 轮审计之后必然打爆配额**，只是
N 多大取决于配额。

判据：**每轮审计闭环后清理自己那轮的 scratch 副本，或至少在任务结束时统一清一次。**

## Root Cause

1. **写 issue 时的视角是「症状落在哪个字段」，受影响面的视角是「这个机制作用在哪些字段」。**
   两者不是同一个集合，所以 issue 的范围声明系统性偏窄——包括自己写的 issue。
2. **「某一侧缺一整块」这种分歧形式对代码对比阅读不可见。** 缺失在阅读中是负空间；只有穷举
   维度的矩阵能把它变成一个可见的空格子。
3. **fixture 集合按事故增长时，覆盖面永远滞后于声明。** 「字节级对等」是全局声明，而 8 个
   fixture 全部来自已发生的事故，两者的差距就是未被覆盖的形状空间，`10_` 立刻证明了它非空。
4. **同一个字段（`has_result`）承载两条语义相反的路径**（校验错误无结果 / 执行错误带部分结果），
   一处无条件赋值就同时决定了两条路径，其中一条一直是错的。
5. **`set -e` 与「预期非零退出」结构性冲突**，且冲突表现为静默中止，没有任何指向根因的信号。
6. **审计工作流产出的 scratch 副本没有生命周期归属**：创建有明确指令（防外部清理），销毁没有。

## Missing Docs or Signals

- 没有任何纪律文档写下**「实测受影响面」是动手前的必需步骤**。这条教训在四篇 reflection
  里各写了一遍（#180、#183、#179、本篇），措辞不同、都只在 memory 层，所以每次都要重新学。
- 没有文档写下**「按形状枚举 fixture」是一种可执行做法**（枚举什么维度、怎么判断枚举完了、
  哪些形状结构性不能进这条通道）。#188 之前只有「字节通道 fixture 太少」这个现象描述。
- `scripts/cross-validate/` 没有任何地方写**「断言失败的校验段在 `set -e` 下的写法约束」**。
  这是脚本层的通用坑，下一个写「必须非零退出」校验段的人会重踩。
- `architecture/dag-engine.md:477-490` 那张「六个解释点」表**现在过时了**：它描述的是三方解析层
  互不一致的基线状态，而本次已经统一，且解释点变成七个（多了值白名单）。这条必须由 recorder 更新。
- `doc/guide_pipeline{,-en}.md` 关于 `storage_mode` 的表述**现在是错的**：`guide_pipeline.md:176`
  写「手写 JSON 配置不经过这层校验：非法的字符串值会被三个运行时静默接受并落到行存」——#187 之后
  三方都拒绝。这是**用户可见契约变更**，必须一并改。
- close-local-code-review 工作流没有任何地方写 scratch 副本的清理责任与累积量级。

## Promotion Candidates

- **升级成硬规则，不要再记第五遍**（建议进 `must/conventions.md` 或
  `guides/investigation-to-fix-testing.md` 的入口动作节）：**跨运行时缺陷动手前必须实测受影响面，
  issue（含自己写的）的范围描述只作为症状线索**。四次同型教训的清单可以直接引用：#180 说小一个
  量级、#183 同时说大又说小、#179 说准了但仍需实测确认、#187 是自己写的 issue 仍然说窄。判据是
  「这个机制作用在哪些字段/路径上」，不是「症状出现在哪个字段」。
- **进 `guides/ci-quality-baseline.md`**（与既有的「校验通道能钉住的属性」节并列）：
  - 「**fixture 按响应形状枚举，不按事故补**」。理由用 #188 的实测：8 个存量 fixture 全部来自
    事故，按形状枚举的第一个新 fixture 立刻查出既存分歧。配套写清 section 14 的边界——**含
    `trace` 或来自 `/stats` 的响应永远不可能字节稳定**（`duration_ms` 与计数器是真实测量值），
    #183 试过并放弃，加这类 fixture 会得到一个在正确代码上失败的检查。
  - 「**穷举维度的测试会查出读代码查不出的缺口**」。`_PINEAPPLE_CREATE_TIME` 是实例：4 字段 ×
    6 类型的矩阵里 pine-java 那一格怎么填都不红，才暴露出「这个字段根本不存在」。判据：分歧形式
    是「某一侧缺一整块」时，用矩阵不用对读。
- **进 `guides/ci-quality-baseline.md` 的 cross-validate 节（脚本层纪律）**：`_env.sh` 设了
  `set -e`，**断言「必须失败」的校验段每一次命令替换都要写 `rc=0; out=$(...) || rc=$?`**，否则
  第一个预期失败的用例会静默中止整个脚本、连 rc 都读不到，且表现为「一条 pass/fail 都不打」。
- **进 `guides/investigation-to-fix-testing.md`**：**跨运行时行为分歧可能出现在同一运行时内部**。
  pine-cpp 对四个同类根级字段有三种处理（抛错 / 静默忽略 / 无此字段），所以「pine-cpp 拒绝非法
  类型」这句话在字段粒度上是错的。判据：确认某运行时的行为时，检查它对**同类字段**是否一致，
  不要从一个字段推广到一类字段。这是 #183「某个运行时天然满足只对具体代码路径成立、不对整个
  运行时成立」的同型第二例，可以并列写。
- **需要更新的稳定文档**（recorder 处理）：
  - `architecture/dag-engine.md` 的 `storage_mode` 节——「六个解释点」表按现状重写为七个
    （三分派 + 三解析 + 一白名单），解析层三方已统一为「拒绝 present-but-wrong-typed、接受
    `null`/缺省」，并写下值白名单**刻意放在 config 校验层而不是 frame factory** 的理由（保持
    #179 的 dispatch 规则不变，让非法值不可达而不是把 dispatch 搞复杂）；补 Go 白名单常量在
    `internal/config` 重复定义的原因（import 环）与「两处必须同步」。
  - `doc/guide_pipeline{,-en}.md` 那句「手写 JSON 静默接受非法值并落行存」已经不成立，属用户
    可见契约变更，必须改。
  - `reference/` 或 `architecture/` 需要一处写下**四个根级字符串字段的类型规则**（present 但
    类型错 → 拒绝；`null` 与缺省 → 默认值），因为这条规则现在是三方共同契约，将来加第五个根级
    字符串字段的人必须遵守它。
- **运维说明**（建议进 close-local-code-review 相关文档或 `guides/`）：每轮 blind review 的
  reviewer scratch 副本 1–1.7GB，创建有指令（防外部清理进程删原快照）、销毁无归属，N 轮之后必然
  打爆磁盘配额。本次实测遗留 17GB（#180/#183/#179 三轮累积，#183 单独 14 轮）。判据：审计闭环后
  清理本轮副本，或任务结束时统一清。
- **`doc-gaps.md` 两条开放条目都关闭**：
  - 「运行时层 fail-fast 拒绝非法 `storage_mode`」——已完成，且**范围比条目描述的更宽**：条目的
    「决策输入 (a)」预见到了「不只是加值白名单、还要先统一类型处理」，这次两半一起做了。结论落
    `architecture/dag-engine.md`。
  - 「字节级对等的校验通道覆盖面太窄」——(a) 半完成，新增 4 个按形状枚举的 fixture，且 section 14
    头部现在记录了这条通道覆盖什么、结构性不可能覆盖什么。关闭时**不要在 doc-gaps 里复述 fixture
    数量**（这个数字已经过期两次），只指向 `fixtures/server_byte_exact/` 与 section 14 头注。

## Follow-up

1. 调 `recorder` 落地上述稳定文档改动：`architecture/dag-engine.md` 七个解释点、
   `doc/guide_pipeline{,-en}.md` 用户可见契约、`guides/ci-quality-baseline.md` 三条纪律
   （按形状枚举 fixture / 穷举矩阵 / `set -e` 脚本写法）、`guides/investigation-to-fix-testing.md`
   一条（同一运行时内部分歧），并关掉 doc-gaps 两条开放条目。
2. 决定「实测受影响面」这条要不要从 memory 升到 `must/`。它已经在四篇 reflection 里各存一份，
   继续留在 memory 层等于承认第五次还会犯。
3. 清理 `.code-review/` 下遗留的 reviewer scratch 副本，并把清理责任写进审计工作流。
   **注意：这是删除操作，执行前需要用户确认具体删哪些路径。**
4. 若将来新增第五个根级字符串字段，按本次统一的类型规则处理（present 但类型错 → 拒绝、
   `null`/缺省 → 默认值），并同步四处：pine-go struct tag、pine-java `rootString`、
   pine-cpp config 解析、以及对应的类型矩阵测试。

## 验证情况（本次已完成）

- `make lint` / `make codegen-check` / `make test` 全过
- `make java-test`（新增 3 个用例）、`make cpp-test`（新增 2 个）、`go test ./...` 全过
  （**用例总数不写死**——这个数字在写下的那次提交就会过期，要数就跑命令）
- `make cross-validate` 全 PASS：section 05 error parity 含 2 个新增 error fixture；
  section 14 byte-exact 两对全绿（新增 4 个 fixture）；section 21 validation parity 两对全绿
- `make differential-fuzz` 1000/1000
- mutation 三组，每条新 gate 都对着自己的 mutant 验证会红：
  - Go 白名单改成接受一切 ⇒ 新增 Go 用例红
  - pine-java 去掉 `isTextual` 检查 ⇒ `rootStringFieldsRejectNonStrings` 红
  - pine-cpp 删掉白名单 ⇒ 新增 cpp 用例红 + section 21 红 4 条
