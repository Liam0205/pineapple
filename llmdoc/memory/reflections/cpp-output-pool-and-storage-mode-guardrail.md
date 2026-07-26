# [pine-cpp OperatorOutput 复用（#122）+ 列存护栏 fixture 与 storage_mode 文档（#160）]

## Task

- 分支 `feat/122-160-cpp-output-pool-storage-docs`（基于 `origin/master` = `7612e185`），8 个 commit 收两个 issue，外加三个途中发现的 nightly benchmark 接线缺陷：
- **issue #122** — 把 pine-go #119 的 `OperatorOutput` 池化收益移植到 pine-cpp（`8a955816`）。`node_body` 原来每个节点 `OperatorOutput out;` 新建，改为 `thread_local OperatorOutput tls_out; tls_out.reset(); OperatorOutput& out = tls_out;`，配套新增 `OperatorOutput::reset()`（`pine-cpp/include/pine/pine.hpp:493`，清九个成员 + 两个 has_ 标志）。回归门两个（`4d9cd374`）：新文件 `pine-cpp/tests/test_output_pool.cpp` 是引擎级跨算子泄漏测试（仿 pine-go `scheduler_test.go` 的 `inspectOutputOp` 模式），`test_operator_output.cpp` 追加容量保持测试。
- **issue #160** — 第一部分文档（`74a677dc`）：`doc/guide_pipeline.md` / `-en.md` 新增「Flow 级配置」节 + README 指针；第二部分 fixture（`25fd80a6`）：新增 `transform_heavy_1000` 合成列存护栏 fixture（1 个 `recall_static` + 8 个链式 `transform_normalize`，钉 `storage_mode=column`）。所有 calibrated fixture 都声明 `storage_mode=row`，列存批量列访问路径此前没有任何 fixture 看着。
- **途中缺陷**：nightly benchmark 报表路径不匹配致 artifact 长期为空但 job 全绿（`e3cb34b3`）、`--modes` 是空转开关（`cd42dd4d`）、`bench-compare.py` 只认 11 列 legacy 格式（`61f27647`）、`bench-generate-fixtures.py` 既存 ruff 违规清理（`5b4a8be7`）。
- 另开 issue **#179** 记录 `storage_mode` 非法值兜底的跨运行时分歧——**只是记录，本次未修**。

## Expected vs Actual

- Expected：移植 pine-go 的池化做法 → 加两个回归门并用 mutation 验证有牙 → 补 fixture 和文档 → 全套验证过。
- Actual：主线按预期完成，但 mutation 验证阶段连踩两坑，各花掉可观时间：
  - 第一坑（**假阴性 mutation**）：为验证容量保持测试有牙，把 `reset()` 里 `common_writes_.clear(); item_writes_.clear();` 改成 `common_writes_ = {}; item_writes_ = {};`，预期变红，**结果 1 passed / 12 assertions 全绿**。我一度怀疑 build 依赖追踪没重编译测试 TU、或断言写法不对，准备重写断言。真实原因：`item_writes_ = {}` 绑定到 `operator=(std::initializer_list)` 转发给 `assign()`，标准库 `assign` **从不缩减 capacity** —— 与 `clear()` 在容量语义上完全等价。那根本不是有效 mutation，测试没红是**正确行为**。换成真正的移动赋值 `item_writes_ = std::vector<ItemWrite>{}` 后立刻红（`0 == 256`）。
  - 第二坑（**`git checkout --` 吞未提交工作**）：第一轮用 `git checkout -- pine-cpp/src/runtime/engine.cpp` 恢复 mutation，同时 revert 掉尚未提交的 `thread_local` 改动，导致「242 passed / mutation 没抓到」这个结论建立在假基线上。改用 `cp /tmp/122-engine-backup.cpp` 恢复才修正。
- 另外 commit 单域隔离第一次提交就破了，需要事后拆分（详见下）。

## What Went Wrong

- **mutation 没红被误判成「测试没牙」**：这是本次最重要的一条。`X = {}` 与 `X.clear()` 在 vector 容量语义上等价，构造的 mutation 不改变被测语义。差点因为这个假阴性把一个本来正确的断言重写坏。已把该结论写进 `test_operator_output.cpp` 注释防止后人重蹈。
- **`git checkout -- <file>` 用作 mutation 恢复手段，吞掉未提交改动**：这是同类错误第二次。`llmdoc/memory/reflections/lua-type-tag-dispatch-and-fuzz-blindspot.md:21` 已记录 #175 时期对已提交文件用 `git stash` 得到 no-op、结果跑的其实是修好的代码的同型事故。上一篇的结论是「改用 `git checkout <base> -- <file>`」，本次证明这条结论本身不够安全——只要工作区有未提交改动，任何 git 恢复命令都会连带作用。
- **commit 单域隔离在第一次提交时就破了**：`fix(ci): restore nightly benchmark artifacts by aligning report paths` 把 `--modes` 的修复（`config_for_mode` 函数）一起扫进去了——两者都在 `scripts/bench-cross-runtime.sh` 里，`git add` 整个文件就带上了。补救：`git reset --soft HEAD~1` + `git stash` 把脚本回到 HEAD 状态、只重新施加报表路径改动、提交、再从 `/tmp` 备份恢复完整版本，拆成两个 commit。
- **动长期没人碰的文件必然承担它的既存 lint 债**：`bench-generate-fixtures.py` 有 11 个既存 ruff 违规（unused `os` import + 10 处 E501），一直没人碰所以没触发；为加 fixture 改了它，pre-commit hook 立刻把整个文件违规全报出来，必须先清理才能提交。`bench-compare.py` 同样（`re` / `sys` 两个 unused import，`git show HEAD:` 确认既存）。清理单独走了 `5b4a8be7`。
- **调查报告的实施建议被当结论会走弯路**：investigator 报告（`.llmdoc-tmp/160-storage-mode-investigation.md`）建议「加一份只差 `storage_mode: row` 的对照 fixture」，理由是 `--modes` 坏了所以双 config 是唯一办法。实际把 `--modes` 修好后就不需要对照 fixture ——`storage_mode` 是根级 config 字段，在 WORK_DIR 物化一份改写该字段的副本传给 server 即可，运行时无关，不需要给三个 server 加 flag（报告认为改动面大）。
- **issue 标题措辞会带错方向**：#160 标题写 "a column-favorable **calibrated** fixture"，但 calibrated 的实质特征是每个算子带 `bench_profile`（真实流量画像 + 依赖 bench stub 算子），新增的合成 fixture 不可能是 calibrated。issue body 自己纠正为 "synthetic guardrail"。按标题做会污染 `benchmark-hygiene.md` 里「calibrated 是性能决策唯一裁判」的判据。
- **想引用性能倍数时发现口径打架**：同一个 transform-heavy 实验，llmdoc 记 `~30%`（2.7 vs 3.9ms），PR #155 描述记 `~37%`（3.56 vs 5.68ms），两组绝对值也不同；都是 Apple M5 Pro 上 pine-go 的 Go microbench，而 README Benchmark 表是 Linux 2C/4G 三引擎 HTTP e2e，两者口径不可混排（`benchmark-hygiene.md` 的「测量路径对称性」正禁这个）。#156/#157 又连续改动过三轮。最终决定用户文档**完全不写倍数**，只写定性判据 + 指向 `pine-go/benchmarks/bench_storage_ab_test.go` 的可复现入口。
- **文档里第一版复现命令跑不通**：`cd pine-go && go test -tags pine_bench -bench=BenchmarkStorageAB ./benchmarks/` 报 `main module does not contain package .../benchmarks` —— `benchmarks/` 是**独立 module**，必须 `cd pine-go/benchmarks` 再跑。改成 `cd pine-go/benchmarks && go test -tags pine_bench -bench=BenchmarkStorageAB -run='^$' ./...` 实测通过才提交。`standard-workflow.md` 已有「文档命令须真实执行过」的要求，本次是它救了一个错误命令，作正面案例记录。
- **microbench 形状直接搬成 e2e fixture 会抹掉被测成本**：Go microbench 的 `transformHeavyConfig` 用空 `flow_contract`（测引擎内部，合理）。作为 HTTP e2e fixture，空 `item_output` 会让 `ToResult` 把每个 item 投影成 `{}`（`projectMap` 只拷列出的字段，空列表就是空输出、不回退成「返回全部字段」），序列化成本被抹掉，整条 transform 链变成没人读的死写入。新 fixture 给了 `{"item_output": ["item_id", "item_score_n7"]}` 把链尾字段投影出去。

## Root Cause

- **假阴性 mutation 的根因**：mutation testing 的隐含前提是「mutation 确实改变了被测语义」，而这个前提从不被显式检查。`X = {}` 看起来比 `X.clear()` 更激烈，直觉上是「更彻底的清空」，但在 vector 容量语义上二者相同。当测试断言的正是 capacity 时，这条 mutation 的语义 delta 为零。缺的是一步「先证明 mutation 改变了行为，再判断测试有没有牙」。
- **git 恢复吞改动的根因**：mutation 验证的操作形状是「在有未提交改动的工作区上临时改一个文件、再恢复」。git 的恢复语义单位是 HEAD/index，不区分「我刚加的 mutation」和「我还没提交的正经改动」，两者在工作区里没有边界。上一篇的修正（换 `git checkout <base> -- <file>`）只解决了「取到的是不是基线」，没解决「恢复会不会连带 revert」。
- **commit 隔离破功的根因**：单域隔离纪律的执行单位是 commit，但 `git add` 的操作单位是文件。当同一文件承载两个不相干域的改动时，纪律与工具粒度错位，`git add <file>` 就是漏洞。
- **既存 lint 债突然暴露的根因**：pre-commit hook 是 staged-file 粒度的整文件检查（`ci-quality-baseline.md:106`），不是 diff 粒度。这是刻意设计（避免历史污染逐点漏过），代价就是首次触碰旧文件要一次性还债。
- **文档倍数口径打架的根因**：性能数字随优化轮次移动（#155/#156/#157 三轮），而写进文档的数字不会跟着动。倍数是活的，判据是稳定的。

## Missing Docs or Signals

- **mutation 验证配方缺「先证伪无效 mutation」这一步**：`guides/ci-quality-baseline.md:169` 已有 red-before/green-after 配方（用于 differential-fuzz 探测能力），但没写「测试没红有两种解释：测试没牙 or mutation 没改变语义，必须先排除后者」。本次差点因此改坏正确断言。
- **mutation / 临时改动的恢复手段没有硬规则**：两篇 reflection（`lua-type-tag-dispatch-and-fuzz-blindspot.md`、本篇）记的是同一类事故的两个变体，但都停留在 memory 层，没有升级成 guide 里的稳定条目，所以第二次仍然踩。
- **clang-format 实际没有自动守门**：`make fmt-check` 里包含 clang-format 检查，但 `grep -rn "clang-format\|fmt-check" .github/workflows/` **无任何命中**——CI 的 `cpp-lint` job（`.github/workflows/ci.yml:249`）只做 `-Werror` 严格构建、trailing whitespace/tab/trailing-newline 卫生检查、相邻字符串字面量拼接排查，**不跑 clang-format**。本机也没装 clang-format，所以 C++ 格式只由 pre-commit hook（本地、可绕）守着，CI 层是缺口。`ci-quality-baseline.md:96-100` 描述 clang-format 是 C++ lint 工具，读起来像有 CI 覆盖，实际没有。`redis-resourcemanager-migration-and-pine-python-removal.md` 记过「clang-format commit 阶段无 gate」，本次进一步确认 CI 阶段也无 gate。
- **`llmdoc/memory/doc-gaps.md` 不存在**：memory 下只有 `decisions/` 和 `reflections/`。CI 覆盖缺口这类「不属某次任务、需要跨任务累积」的条目当前没有落点，只能塞进某篇 reflection。
- **「microbench 形状搬 e2e 需重查投影/序列化」没有文档化**：`benchmark-hygiene.md` 有「测量路径对称性」和「microbench 访问模式戒律」，但讲的是不同测量路径的结果不可互推；缺的是**搬运形状时要重新检查哪一段成本**——microbench 不关心的投影/序列化恰好是 e2e 的主要成本。
- **`fix-output-projection-semantics.md` 记的 projectMap 空列表语义没有出现在 fixture 编写视角**：这条语义（空 `item_output` = 空输出，不回退返回全部字段）在写 fixture 时是个陷阱，但只在那篇修复复盘里，fixture 作者不会去读。

## Promotion Candidates

- 进 `guides/ci-quality-baseline.md`（red-before/green-after 配方旁边）：**mutation 验证的两步判据**——(1) 先证明 mutation 真的改变了被测语义；(2) 再判断测试有没有牙。「测试没红」的第一解释应当是「mutation 无效」而不是「测试没牙」。具体反例值得写下：`vector = {}` 走 `operator=(initializer_list)` → `assign()`，**不缩减 capacity**，与 `clear()` 容量语义等价；要真正丢容量必须移动赋值 `v = std::vector<T>{}`。
- 进 `guides/standard-workflow.md`（或 `ci-quality-baseline.md` 验证节）：**mutation / 临时改动期间禁止用任何 git 命令做恢复，只能用文件级备份 `cp`**。理由是工作区里 mutation 与未提交的正经改动没有边界，git 的恢复单位是 HEAD/index，必然连带。这是同型事故第二次（#175 的 `git stash` no-op → 本次 `git checkout --` 吞改动），建议升级为硬规则。
- 进 `guides/standard-workflow.md`（commit 纪律节）：**同一文件承载多个域的改动时，`git add <file>` 就是单域隔离纪律的漏洞**。动手前先识别「这个文件我要改两件不相干的事」，先做完一件提交再做下一件；已经混在一起时用「文件备份 + 逐次施加」补救（`reset --soft` + 备份恢复）。
- 进 `guides/ci-quality-baseline.md`（C++ lint 节 + hook 节）：**clang-format 在 CI 里没有对应 job**，`cpp-lint` 只跑 `-Werror` 构建 + 空白/tab/trailing-newline 卫生 + 字面量拼接排查。目前 C++ 格式只由本地 pre-commit hook 守，是真实的 CI 覆盖缺口。同时把「首次触碰长期无人维护的文件会被 hook 逼着还清整文件 lint 债，应预留时间并单独 commit」写成预期而非意外。
- 进 `guides/benchmark-hygiene.md`（fixture 代表性节）：**合成 guardrail fixture 与 calibrated fixture 是两类东西**，前者用于守护某条代码路径不静默退化（本次是列存批量列访问），后者是性能决策的唯一裁判；合成 fixture 的根级 `_comment` 应明写「synthetic, not a performance verdict」。另加**把 in-process microbench 形状搬成 e2e fixture 时必须重查投影/序列化段**：空 `flow_contract` 在 microbench 合理，在 e2e 会让 `projectMap` 把 item 投影成 `{}`、抹掉序列化成本、把整条链变成死写入。
- 进 `guides/benchmark-hygiene.md` 或 `must/conventions.md`（禁止硬编码定量描述节）：**用户文档不写性能倍数，只写定性判据 + 指向可复现入口**。倍数随优化轮次移动（本次同一实验存在 ~30% 与 ~37% 两个口径、绝对值也不同），且 Go microbench 与三引擎 HTTP e2e 的数字不可混排。
- 仅留 memory：`thread_local` 而非 `sync.Pool` 的取舍与 reset 时机——pine-go 用 `sync.Pool` 因为调度器可能把节点交给任意 goroutine；pine-cpp 的 ready-queue 不迁移半完成的节点，`thread_local` 就够，省掉 Get/Put 记账。**reset 必须在 acquire 侧而非 release 侧**：`apply_output` 会 move-extract `added_items_` / `column_writes_`，留下同尺寸的空壳 vector；若在 release 侧归还前不 reset，下一个节点会把空壳当幽灵 item 重放（实测删掉 `tls_out.reset()` 后 recall 的 moved-from 空壳泄进下游 Transform，触发 `Transform must not call [AddItem]` 类型违规——正是该机制的证据）。acquire 侧 reset 顺带覆盖「抛异常的节点留下垃圾」，不需要写 unwind 处理。
- 仅留 memory：调查子代理的 ROI 与边界——investigator 找出三个超出 issue 范围的 nightly 缺陷（报表路径、`--modes` 空转、bench-compare 列数过期），全部复核为真，值得投；但它的**实施建议要当输入而非结论**，尤其当建议含「因为 X 坏了所以只能绕」这类前提时，先问 X 能不能修。

## 验证情况

- C++ doctest 全过（计数以 `make cpp-test` 实际输出为准——本文写作时是 242 个用例，此后审查轮次陆续追加了 SUBCASE 与新用例，硬编码的数字很快就会过期；这正是 `must/conventions.md`「禁止硬编码定量描述」要防的情况，而本文第一版就踩了）。
- 两个 mutation 都确认能抓到：删 `reset()` → 跨算子泄漏测试红；`reset()` 改成真移动赋值 → 容量测试红（`0 == 256`）。
- 新 fixture 在 go / java / cpp 三运行时输出**字节一致**（md5 相同），且 row vs column 输出也字节一致。
- `make lint` / `make test`（Java 315 tests）/ `make codegen-check` / `make cpp-test` 全过。
- `make cross-validate` 55 个 section 全 PASS。
- `make differential-fuzz` 1000 轮：row=587/0，column=413/0。
- `make all` 在 `fmt-check` 挂掉（Error 127，本机没装 clang-format），非代码问题；且该检查在 CI 里没有对应 job。

## Follow-up

1. 评估把「mutation 期间禁用 git 恢复、只用文件备份 `cp`」写进 `guides/standard-workflow.md` 作为硬规则（同型事故第二次）。
2. 评估把「mutation 无效 vs 测试没牙」的两步判据 + `vector = {}` 不缩容的反例写进 `guides/ci-quality-baseline.md` 的 red-before/green-after 配方旁边。
3. 评估把「clang-format 无 CI job」记进 `guides/ci-quality-baseline.md`；若要跨任务累积这类覆盖缺口，需要先建 `llmdoc/memory/doc-gaps.md`（当前不存在）。
4. issue **#179**（`storage_mode` 非法值兜底的跨运行时分歧）**仅记录、未修**，需单独排期。
5. 评估把「合成 guardrail fixture vs calibrated fixture 的分工」与「microbench 形状搬 e2e 需重查投影段」补进 `guides/benchmark-hygiene.md`。
