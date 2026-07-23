# [Lua 类型标签派发修复与 differential fuzz 值级盲区]

## Task

- issue #175 / 分支 `fix/175-lua-string-number-coercion`（`51f0ee0d..fb095b7b`）：pine-java `TransformByLua.fromLua` 用 luaj 的 `isnumber()`/`isstring()` 派发 Lua 标量，而这两个方法实现的是 Lua **coercion 语义**不是类型判定——`LuaString.isnumber()` 对任何长得像数字的字符串返回 true，`LuaNumber.isstring()` 对每个数字返回 true。数字形字符串因此走进 number 分支：类型身份丢失（`"42"`→42、`"007"`→7），超过 2^53 的值被 `todouble()` 往返破坏（`"1777288596209286259"` → `1777288596209286144`）。只有 Java 错：Go gopher-lua 用 Go type switch、Go wangshu 用 kind 标签（已查 v0.2.0 源码：`v.kind==kNumber`，**不是** coercion）、C++ 用 `lua_type()` switch，三家都按真实类型标签派发。
- 修复 `c2263dd0`：改为 `v.type() == TNUMBER/TSTRING` 派发——同一函数里 table-key 检查早就在用 `k.type() != TSTRING`，正确先例离出错处只有一屏距离。交换检查顺序不可行（反方向 `LuaNumber.isstring()` 同样为 true），type tag 是唯一无歧义派发。
- 回归覆盖分层（`bf318e83` / `f8d2cf31`）：Java 单测 `TransformByLuaTypeIdentityTest` 用 `assertInstanceOf` 钉住返回的 Java **class**；operator fixture 加 4 个危险值 case 并用 `_comment` 键写明层边界；pipeline fixture `fixtures/pipelines/lua_string_number_identity.json` 走 cross-validate section 3/9 的类型保持 JSON 比对，双 seed item 证明逐 item 保持。
- fuzz 修复（`cd8e50a0` → `1a162cf5`）：第一版只加危险字符串与 identity 函数入池；第二版让生成器 ~40% 轮次发出 flow_contract 投影全部累计输出、item-mode Lua 轮次在上游存在 name/tag 字段时 25% 概率强制 identity 直通。探测能力端到端验证：pre-fix Java + 新生成器在真实生成轮上与 Go 分歧（seed 424242 round 2066，normalize_json 不等）；fixed Java 一致；500 轮新鲜 fuzz 零假阳性。
- 流程：close-local-code-review 跑了 5 轮（盲审者在固定 commit 快照 clone 中工作，无继承上下文）：run1 全量（2 minor）→ run2 增量（1 minor）→ run3 全量（3 minor）→ run4 增量（0）→ run5 终局全量（0/0/0）+ 历史补充对账全部 6 个 finding + 终局审计者 PASS 回执（`.code-review/closure-receipt-175-run5.md`）。`fb095b7b` bump v0.10.15。

## Expected vs Actual

- Expected：一行级派发修复 + 常规回归测试，一轮收敛。
- Actual：修复本身确实小（10 行 diff），但补 fuzz 回归网时暴露出一个**结构性盲区**——differential fuzz 生成的配置从不带 flow_contract，三引擎把 common/items 全投影成 `{}`，差分比对看得见退出码、错误文案、item 数量、顺序，**唯独从未看见任何计算出来的字段值**。整类值破坏 bug 从构造上就不可见，30k+ 轮历史 fuzz 无论跑多少都不可能抓到 #175。第一次 fuzz 补丁（`cd8e50a0`）因此无效，第二次（`1a162cf5`）才真正修通。

## What Went Wrong

- **luaj 的 is* 家族是 coercion 查询不是类型谓词**，而三个宿主库的同名 API 语义相反：gopher-lua type switch = 标签、wangshu `Is*` = 标签（`v.kind==kNumber`）、luaj `is*` = **coercion**。长得一样的 API、相反的语义，写 Java bridge 时按 Go 侧直觉照搬就中招。唯一无歧义的派发是 `type()`/`typename()`。
- **正确先例就在同一函数里没被类比**：`fromLua` 的 table-key 分支早就用 `k.type() != TSTRING`，标量分支却用 `isnumber()`/`isstring()`——一屏之内两套派发共存多时，历轮 parity 审计都没发现（值破坏只在数字形字符串 + 大整数才可观测）。
- **第一次 fuzz 补丁测错了指标**：`cd8e50a0` 只把三个危险字符串加进 EDGE_SCALARS 和 tag/name 字段池、identity Lua 函数入池，实测触发率 ~0.25%；而且就算"命中"的轮次也**没有任何可见信号**——投影盲区把值全吞了。coordinator 起初测的是"形状命中率"（14/1000 轮出现了危险形状）就以为有效，后来测"有效可见率"（0/1000 轮的值出现在被比对的输出里）才发现两个指标因投影盲区完全脱节。
- **红-绿双向验证时踩了 stash 空操作坑**：验证回归测试对旧代码确实变红时，先试了 `git stash` 已提交文件——stash 对已提交内容是 no-op（`No local changes to save`），随后跑的其实是修好的代码却以为在测旧代码。正确做法是 `git checkout <base> -- <file>` 取出基线版本 + 重新编译，红-绿两个方向都实跑确认（本次 `bf318e83` 的 fixture 就是按这个方式验证 pre-fix Java 红、post-fix 三运行时绿）。
- 一个 review finding（175-run1-M2）判定为 pre-existing/超出本次范围：`TransformByLua.java:429` 的 `snapshotKeys` 仍用 coercion 的 `isstring()`——数字全局键（脚本显式写 `_G[42]=...`）会被 coerce 成 `"42"` 进 baselineKeys，`resetToBaseline` 的 `g.set("42", NIL)` 清的是字符串槽、清不掉 Lua 语义下独立的数字槽 `_G[42]`，pool 状态隔离可能跨 borrow 泄漏数字全局变量。触发面窄（正常算子路径只产生字符串键），是真实但独立的潜伏问题，应另开 GitHub issue。

## Root Cause

- **派发 bug 的根因**：把宿主库 API 的名字当语义。审计任何运行时的 Lua bridge 时，应 grep 每一个 `is*()` 调用并逐个回答"这是 coercion 还是标签？"——答案取决于宿主库，不能跨库迁移直觉。
- **fuzz 盲区的根因**：生成器从不发 flow_contract，是"生成维度"与"比对面"之间的结构性断层。dag-engine 的投影语义（无 flow_contract → 输出投影为空）是已知设计，但从没有人把它与"fuzzer 比对的是投影后的输出"连起来推出"fuzzer 从不比对值"这个推论。差分测试的探测能力上限由**比对面**决定，不由生成维度决定。
- **第一次补丁无效的根因**：方法论缺失——加 fuzz 维度时只验证了"危险值在某处被生成"，没有验证"信号能到达比对面"。有效性的正确度量是**有效可见率**（危险值出现在被投影、被比对的输出里），不是形状出现率。
- **修复层选择的根因认知**：fixture 比对器决定了 fixture **能**钉住什么——Go operator-fixture 比对器用 `fmt.Sprintf("%v")` 字符串化、Java 的把非 Number 字符串化，所以 operator fixture 天生看不见 `"42"` vs `42` 的类型漂移，只能钉值级对等；Java `PipelineFixtureTest` 对非 Number 对类型敏感；cross-validate 的 normalize_json 保持 string-vs-number 区分。类型身份真正被跨运行时钉住的位置是 pipeline fixture + cross-validate section 3/9。**哪一层钉哪个属性是设计决策，要写下来**——本次用 fixture 内 `_comment` 键固化了这个层边界（并验证 Go/Java 两侧 loader 都容忍未知键）。

## Missing Docs or Signals

- luaj bridge 的 API 语义陷阱无处可查：`reference/lua-backend.md` 只覆盖 pine-go 的两个后端，pine-java 的 LuaJ bridge 没有对应参考，"is* = coercion"这一关键事实只存在于 luaj 源码里。
- fuzz 维度有效性的验证方法论缺失：`guides/ci-quality-baseline.md` 详列了随机化维度清单，但没有"新维度必须证明信号到达比对面"的要求，也没有描述 flow_contract 缺席对比对面的影响。
- fixture 比对器的能力边界（各层字符串化/类型敏感行为）没有任何文档记录——选择在哪层钉类型属性之前，先得知道每层能看见什么。
- 差分比对"看得见什么"从未被显式陈述：错误文案、item 数、顺序在比对面上，字段值不在——这个负空间没人写过，30k 轮的绿灯给了虚假的覆盖信心。

## Promotion Candidates

- 进 `guides/ci-quality-baseline.md`（differential fuzz 节）：**加 fuzz 维度必须验证信号到达比对面**——"危险值在某处被生成"不够，要度量有效可见率（值出现在被投影比对的输出里）而非形状出现率；两个指标会因投影类盲区完全脱节。同时记录现状机制：~40% 轮次发 flow_contract 投影全部累计输出、25% 强制 identity 直通（`1a162cf5`），以及验证配方（pre-fix 二进制 + 新生成器复现真实分歧 → fixed 二进制通过 → N 轮无假阳性）。
- 进 `reference/lua-backend.md`（或新增 Java bridge 小节）：luaj 的 `is*` 家族是 coercion 查询，`type()`/`typename()` 才是无歧义派发；三宿主库对照表（gopher-lua type switch = 标签 / wangshu `Is*` = 标签 / luaj `is*` = coercion）；审计任何 Lua bridge 时 grep 全部 `is*()` 调用逐个判定 coercion-or-tag。
- 进 `guides/cross-layer-validation.md`：**fixture 比对器定义了该层能钉住的属性**——Go fixture_test 字符串化、Java FixtureTest 对非 Number 字符串化、Java PipelineFixtureTest 类型敏感、cross-validate normalize_json 类型保持；为某属性选钉住层时先核对比对器行为，并把层边界写进 fixture（`_comment`）或文档。类型身份类属性只能在 pipeline fixture + cross-validate 层钉住。
- 仅留 memory：红-绿双向验证要用 `git checkout <base> -- <file>` + 重编译，不能用 stash（对已提交文件是静默 no-op，会测到错误版本）；`snapshotKeys` 潜伏问题的细节（数字全局键 coerce 后 resetToBaseline 清错槽）——检索到本篇即可。

## Follow-up

- 为 `TransformByLua.java:429` `snapshotKeys` 的 coercion `isstring()` 另开 GitHub issue：`_G[42]` 类数字全局键使 pool 基线重置失效、状态可跨 borrow 泄漏；触发面窄但属真实缺陷，修法同样是 type tag 判定。
- 由 recorder 把前三条 promotion 写进对应稳定文档并同步 `index.md`。
- 顺手项：下次触碰 pine-java Lua bridge 时，grep 全部剩余 `is*()` 调用（本次已知仅剩 snapshotKeys 一处 coercion 用法）确认没有下一个同类派发。
