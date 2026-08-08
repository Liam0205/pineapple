# CI 工程质量基线

本指南描述 Pineapple 的 CI 质量检查架构和接入约定。

## 适用范围

当任务涉及以下情况时使用本指南：

- 修改 `.github/workflows/` 中的 CI 配置
- 新增或调整 lint 规则
- 变更覆盖率或 fuzz 配置
- 评估 release gate 触发机制

## CI workflow 架构

`.github/workflows/ci.yml` 包含多个 job（数量与依赖关系以该文件为准，禁止在本指南中硬编码计数）。典型分组：

| Job | 职责 | 依赖 |
|-----|------|------|
| go-lint | golangci-lint | 无 |
| apple-lint | ruff check（apple DSL） | 无 |
| go-test | Go 测试 + 覆盖率 | 无 |
| java-test | Java 测试 + 覆盖率 | 无 |
| apple-test | Apple DSL 测试 + 覆盖率 | 无 |
| cpp-build | pine-cpp Release 构建（4 个可执行文件） | 无 |
| cpp-sanitizer | pine-cpp ASan/UBSan smoke | cpp-build |
| cpp-lint | pine-cpp `-Werror` 严格构建 + 基础卫生检查 | 无 |
| cpp-test | pine-cpp doctest 单测套件 | cpp-build |
| codegen-check | 重生成 + git diff 校验 | 无 |
| fuzz | Go native fuzz 短时运行 | go-test |
| benchmark | Go benchmark + job summary + artifact | go-test |
| cross-validate | 多 section 跨运行时校验 | go-test + java-test + cpp-build |
| differential-fuzz | CI 模式 100 轮三引擎差异模糊测试 | go-test + java-test + cpp-build |

另有独立 nightly / daily workflow：

- **Nightly differential-fuzz**（`.github/workflows/nightly-diff-fuzz.yml`）：nightly 运行三引擎差异比对，固定 10000 轮（手动触发可通过 `inputs.rounds` 覆盖）；不再按工作日/周末分流，CI 通过 shell `timeout` 在 340min 处保护 step 流程，发现分歧或 cancelled 时均会自动创建 GitHub issue
- **Daily sanitized-fuzz**（`.github/workflows/daily-sanitized-fuzz.yml`）：每日运行 pine-cpp 的 ASan/UBSan + TSan 两个 sanitizer-instrumented differential-fuzz pass，与 Nightly differential-fuzz 互补分工——后者用 Release 二进制追求原始吞吐（10k 轮/不同 seed 覆盖），前者用 sanitizer 加持换取"内存/竞态类 bug 在首次触发时就能拿到完整栈"的深度诊断能力。两 pass 均采用两层 timeout 设计：`differential-fuzz.py` 的 `--time-budget-seconds` 是内层 pacing 机制，预算耗尽即停止发起新轮、仍正常输出 `Results:` 汇总（标注为部分覆盖），外层 CI `timeout` 降级为纯 hang 保护（只在进程真正卡死或脚本崩溃时触发）。因此 evaluate step 判定的 incomplete 状态语义单一化：不再包含"慢但健康"的情况，只意味着真实 wedge 或脚本 crash。具体轮数/预算分钟数以 `.github/workflows/daily-sanitized-fuzz.yml` 文件注释为准（禁止在本指南中硬编码，历史标定值已在该文件多次因 runner 吞吐方差重新校准）。
- **Nightly cross-runtime benchmark**（`.github/workflows/nightly-benchmark.yml`）：每日 22:30 UTC+8 运行 `scripts/bench-cross-runtime.sh`，对比 Go/Java/C++ 三运行时在多维矩阵下的最大吞吐：DAG 规模（默认 `5,50,100,200`）× 存储模式（`row,column`）× 算子类型（`cpu,io,mixed`，对应新增的 `transform_bench_cpu` / `transform_bench_sleep` 与 lua 混合管道）× 可选 fan-out 并行度。仅运行最大吞吐阶段（不再有顺序延迟与固定 QPS=500 阶段）。Job 超时提升为 90min，自动下载上次成功 artifact 并通过 `scripts/bench-compare.py` 生成 delta 报告，`scripts/bench-analyze.py` 提供单次运行的多维度分析（runtime ranking / parallelism effect / storage effect）。完成后通过 Bark 推送通知，运行前停止 runner 上非必要服务以提升隔离性

所有质量检查集中在 CI workflow 中。Release workflow 通过 `workflow_run` 依赖 CI 结果，不重复任何检查。

### Agentic workflow 的环境准备脚本（`setup_script`）

`.github/workflows/agentic-pr-review.yml` 与 `agentic-llmdoc-updater.yml` 复用上游 `Lightspeed-Intelligence/agentic-workflow-template`，两者都通过上游的 `setup_script` 输入指向本仓库的 `.github/agentic/setup.sh`。

为什么需要它：agent 跑在裸 `ubuntu-latest` 上，只有 runner 预装工具链。PR #194 的评审明确报告了三项做不到的检查——没有 `ruff`、没有 `golangci-lint`、JDK 不支持 `pom.xml` 的 `release 25`。这三项恰好是最容易查出真缺陷的检查，所以"只能读代码的审查器"是真实的审查能力损失。脚本按最便宜的阶段在前排序，每阶段各自限时，于是某个阶段病态超时只损失它自己那一项能力，不会连带后面的：JDK 25（走 runner toolcache 的 `JAVA_HOME_25_X64`，无下载）→ Go（按 `pine-go/go.mod` 取 toolcache 里最新 1.26.x）→ Python 工具（venv 装 ruff/pytest）→ golangci-lint（钉版本 + 校验和）→ pine-cpp（最后、预算最大）。版本钉的是 **CI 实际解析出来的值**（对着 PR #194 那次运行核过），不是照抄配置文件的字面写法。

改这个脚本前必须知道的三条，都是实测出来而非读代码看出来的：

1. **仓库根目录取 `$PWD`，不能用 `BASH_SOURCE`。** 上游 hook 的调用形式是 `cd "$repo_dir" && bash "$hook"`，而 `$hook` 位于 `.trusted-base`——一个只含这个脚本的 sparse checkout。按脚本位置推导根目录会让每个阶段都找不到 `pine-go/go.mod` / `pine-cpp/` / `scripts/`，结果是"报告所有能力都不可用、其实什么都没装"。现已用三个 marker 文件断言 cwd。
2. **`timeout` 是外部程序，调不了 shell 函数。** 阶段体必须 `export -f` 后在 `bash -c` 里跑，否则每个阶段以 rc=127 结束，症状同上——读脚本完全看不出来，只有真跑一次才会暴露。
3. **工作树必须保持干净，否则 job 直接失败。** 产物一律落 `RUNNER_TEMP`，唯一例外是 C++ 构建用 `pine-cpp/build-tests`（与 `scripts/cpp-test.sh` 对齐，让 agent 的 `make cpp-test` 复用缓存），该路径由 `.gitignore` 的 `pine-cpp/build*/` 覆盖，其下 CMake FetchContent 缓存同样被覆盖。

退出码非零不致命，因此脚本会**点名**列出不可用的阶段——那段文字是 agent 唯一能知道"缺哪项能力"的信号，只给个退出码等于让它猜。**正因如此，脚本自己维护一个总 deadline**（读 `SETUP_HOOK_TIMEOUT`，留一小段余量给汇总本身），预算不够时主动裁剪或跳过后续阶段：各阶段预算之和刻意大于 hook 默认上限（具体数值以 `.github/agentic/setup.sh` 末尾那串 `phase` 调用为准，不在此复述），它们是病态阶段的天花板而非预期耗时；但若总时长真撞到上限，被杀的是整个进程组，点名清单那段根本不会执行——那样恰好在最需要点名的路径上丢掉这个信号。

### 解析外部可覆盖的数值变量：整体校验 + 定进制 + 定量级

`SETUP_HOOK_TIMEOUT`（`.github/agentic/setup.sh`）与 `ATTEMPTS` / `ATTEMPT_TIMEOUT`（`scripts/ci-apt-install.sh`）都是调用方可覆盖的数值型变量，同一个解析问题在审计中连查四轮，每轮一个，因此单列成纪律：

1. **只看后缀不够**：`13m30s`、`1e3s` 都以合法单位结尾，剥掉单位后剩下的却不是数字。必须剥单位后**整体**校验剩余部分。
2. **`[[ $x == +([0-9]) ]]` 通过 ≠ `$(( x ))` 给你期望的值**：该模式接受前导零，而 bash 算术把前导零读作八进制——`010m` 静默变成 480s 而非 600s，`0900s` 直接 `value too great for base`。所有进入算术的分支都要 `10#`。
3. **定住进制之后还要定住量级**：`10#` 不防溢出，位数足够多时回绕成一个**正**的巨大值，于是「预算不足要裁剪」的判断反而认为预算充足。算术前先限位数。
4. **写下「凡是喂进算术的地方都要 `10#`」这类普适纪律时，立刻 grep 本次改动范围内所有的同类点**：这条纪律第一次写下来的那次提交自己就违反了它（`ATTEMPTS=09` 会让 `[[ -lt ]]` 恒假、**静默关掉** mirrorlist 轮转）。

失败形状的共性值得记：以上每一种都不是"报个错停下"，而是**脚本继续跑、行为却与声称的不同**（静默跳过全部阶段 / 不裁剪 / 关掉轮转），与本节开头「坏掉的脚本和诚实报告受限环境的脚本输出一模一样」是同一族。

### 验证这个脚本的手工配方

它是 CI 配置代码，仓库里没有对应测试层，所以验证靠下面这套手工步骤（没有自动化检查会在它回归时报警，见 `memory/doc-gaps.md`）：

1. **用上游真实的 hook 跑，不要模拟"我以为 hook 会怎么做"**：`gh api repos/Lightspeed-Intelligence/agentic-workflow-template/contents/.github/scripts/agentic/run-setup-hook.sh --jq .content | base64 -d > /tmp/run-setup-hook.sh`，然后按它的真实调用形式执行——把脚本放到一个模拟 `.trusted-base` 的目录里、cwd 设为仓库根：`bash /tmp/run-setup-hook.sh ".github/agentic/setup.sh" <trusted-base-dir> "$PWD" <prompt-file> review`。
2. **变异验证工作树洁净断言真的会红**：换一个只做 `touch ./stray` 的脚本，确认 hook 以 exit 1 失败并打印 `环境准备脚本改动了工作树`；再确认真脚本通过。只看到"通过"不能证明这道门是活的。
3. **验证脚本缺失路径**：把 trusted-base 目录换成空目录，确认 hook 打 warning 并 `exit 0`（这正是引入该脚本的那个 PR 自己会走的路径）。

另有一条与直觉相反、下次别改回去：`pr-review` 从 **PR base commit** 读这个脚本，所以引入它的那个 PR 自己享受不到（降级为一条 warning，已对着真 hook 验证过），收益从下一个 PR 开始。`update-llmdoc` 则从事件固定的 checkout 读，没有这个滞后。

安全边界表述按上游口径：脚本来自可信来源只阻断了一类直接注入，**不构成安全边界**（`mvn` 仍会执行 head 工作树的 `pom.xml`）；真正的边界是 job 的只读 token 与不向 agent 进程注入 GitHub/PAT 凭据。脚本本身不接收任何 secret。

Benchmark job 将 `go test -bench` 输出写入 `benchmark.txt`，同时追加到 `$GITHUB_STEP_SUMMARY` 供 PR/CI 页面直接查看；artifact 作为可下载原始结果保留。

## CI apt 依赖安装约定

所有 workflow 中安装 apt 依赖的步骤必须统一走 `scripts/ci-apt-install.sh`，不得回退到裸 `timeout N sudo apt-get install ...` 单发模式。

背景：慢速 Azure archive mirror 曾两次拖垮 CI——#125（2026-06-18）把整段 `timeout` 从 300s 提到 600s 作为"修复"，#164（2026-07-10）同一堵墙被再次撞穿（单个包在 26 KB/s 下载了 433s）。根因是"单发安装 + 整段超时"结构本身没有第二次机会，静态加大超时数值只是把击穿点往后挪。判断准则：先问这个失败模式重试后是否大概率自愈——是则加重试层，否则才考虑调大超时数值。

**但"重试就会换镜像"这个前提本身曾是错的，且错了很久。**（配套教训：修它的第一版**又按行序算了一次轮转**，见下方实现注意事项——「注释说 X 才生效」和「实现按 Y 算」同时存在，是审计查出来的。） #164 修完之后，本文这一节和脚本注释都写着"mirror rotation 通常会自愈"，实际上重试从来没有换过镜像：runner 的 `sources.list` 指向 `mirror+file:/etc/apt/apt-mirrors.txt`，而 `apt-transport-mirror(1)` 明确两条——只有**取用失败**才会 failover 到下一个镜像，且镜像按 `priority:` 升序尝试。#164 的失败形式是镜像**答应了然后极慢**，这不算失败，failover 从不触发，三次尝试全都回到同一个 `priority:1` 主机。per-attempt timeout 把"一个慢镜像"变成了"三个慢镜像"。修复（2026-08-08）是在尝试之间真的轮转 `priority:` 顺序。教训与 #193「无人可见的属性必然腐烂」同族但不同：这里的属性有人写下来了，只是写的是**期望**而非**实测**——「重试后会落到另一个镜像」当时既没有实测也没有任何通道能看见它没发生。凡是声称「某个机制会自愈」的注释，都要能说出它靠哪一条具体行为自愈。

`ci-apt-install.sh` 的结构：update / install 各自最多 3 次尝试（`ATTEMPTS`，默认 3）、每次独立 per-attempt timeout（`ATTEMPT_TIMEOUT`，默认 300s，均可通过环境变量覆盖）、尝试间 backoff（`attempt * 10`s）、kill 后 `dpkg --configure -a` 修复半配置状态、尝试间轮转 mirrorlist 的 `priority:` 顺序（`APT_MIRRORLIST` 可覆盖路径；非 runner 环境读不到该文件时退化为原行为，不报错）、`Acquire::Retries=3`（覆盖单次尝试内的连接中断）+ `DPkg::Lock::Timeout=60`（等待 unattended-upgrades 类锁持有者）。

轮转实现上有三处不显然、改动时不要退回去：**重写 `priority:` 数值而不是只调整行顺序**（文件里的行序不是 apt 遵循的东西，无显式 priority 的镜像排最后）、**轮转量必须按 priority 序而非文件行序计算**（第一版就错在这里：注释正确地写着「`priority:` 才是生效的东西」，紧接着却按行序左移；当行序与 priority 序不一致时，一次轮转可能把首选镜像留在原地，恰好是要消除的失效模式。验证用例全部取自 runner 真实 mirrorlist，而那份文件行序**恰好**等于 priority 序，于是两种实现不可区分、用例全绿——与 #183「fixture 声明顺序恰好已是字典序」同型。判据：**写下「X 才生效、不是 Y」时立刻检查实现按哪个算，并专门造一个 X 与 Y 不一致的用例**，这种用例不会自然出现在现实样本里），以及**按 tab 字段解析**（格式是 URI + TAB + metadata，把整行当字符串重写会用空格连接 URI 与 metadata，apt 会把空格算进 URI；`arch:`/`codename:`/`component:` 必须原样带过，丢掉它们会扩大一个部分镜像被要求提供的文件范围）。调用方若给出更紧的时间预算，应整体包一层 `timeout` 限制 apt 总时长，而不是只压小 `ATTEMPT_TIMEOUT`——`.github/agentic/setup.sh` 的 pine-cpp 阶段就是这么做的（3 次 × 90s 两轮的最坏情况会吃掉整个阶段预算，cmake 根本轮不到）。

包清单纪律：只安装 runner image 真正缺失的包，不重复安装已预装工具（GitHub runner image 预装 cmake / g++ / build-essential，apt 装的同名包版本更旧且 PATH 排序在后，纯粹是死重，只会放大慢镜像暴露面）；install step 之后应对预装工具做版本断言（如 `cmake --version`、`g++ --version`），使 image 变更导致的依赖缺失在 install 阶段就明确报错，而不是在后续编译步骤里表现为莫名错误。新增 workflow 或 job 时禁止绕过 `ci-apt-install.sh` 直接内联 apt 命令。

## 统一任务入口（Makefile）

仓库提供顶层 `Makefile` 与 `pine-go/Makefile` 作为本地与 CI 共用的统一任务入口，封装跨四语言的格式化 / lint / test / bench / codegen / 版本管理等命令。目标列表以 `make help`（或两个 Makefile 自身）为准，禁止在本指南中硬编码完整清单。常用入口示例：

- `make all` — 本地提交前全检（`fmt-check lint test codegen-check`，不含 cross-validate / differential-fuzz / fuzz 等慢 job）
- `make fmt` / `make fmt-check` — 各语言格式化写回 / dry-run（CI 用，任何 diff 即 fail）
- `make lint` / `make test` / `make bench` / `make codegen` — 全语言 lint / 测试 / benchmark / codegen
- `make bench-lua-backends` — wangshu vs gopher-lua 后端对比（见 `llmdoc/reference/lua-backend.md`）
- `make hooks` — 安装 git hooks（等价 `git config core.hooksPath .githooks`）
- `make bump VERSION=X.Y.Z` / `make tag-release` — 跨 5 处同步版本号 + 全验 / 创建并推送双 tag

CI 的多个 job 直接调用这些 make target（如 `make go-cover` / `make cpp-test` / `make cross-validate` / `make codegen-check` / `make bench`），使本地与流水线执行同一份命令序列、避免 CI 内联命令与本地操作漂移。`make bench` 默认走 `pine-go/benchmarks/` 独立子 module 并带 `-tags=pine_bench`，`TAGS` 追加在其上（如 `make bench TAGS=lua_gopher` 切换对照后端）。复杂跨语言序列（如 pine-cpp 的 cmake/ctest）抽取到 `scripts/` 下脚本（如 `scripts/cpp-test.sh`），由 make target 调用。

## Go lint

工具：`golangci-lint`，配置位于 `.golangci.yml`。

提交前必须对以下高风险区域的改动在本地运行 `golangci-lint run ./相关包`，重点检查 errcheck，不能依赖远端 CI 帮你捕获遗漏的 error return value：

- HTTP handler（`pine-go/pkg/server/`）
- Benchmark（`benchmarks/`）
- Integration test helper（`pine-go/integration/`）
- Test helper 函数

测试代码与生产代码遵循同一套 linter 规则，没有“测试代码可以不检查 error”的例外。

## Python lint

工具：`ruff`，配置位于 `pyproject.toml` 的 `[tool.ruff]`。

`apple_generated/` 已通过 `extend-exclude` 排除。生成产物的 lint 问题应通过修复 codegen 源或其输入解决，不应手工修改产物。
## Java lint

工具：`checkstyle`，配置位于 `pine-java/checkstyle.xml`。

- 4-space indent 规则
- `failOnViolation=true`：checkstyle 违规直接导致构建失败
- `OneStatementPerLine` 规则：强制每行最多一条语句，拒绝 `if (...) return;` 等单行压缩写法

## C++ lint

CI `cpp-lint` job（`.github/workflows/ci.yml`）实际只做三件事，**不跑 clang-format**：

- `-Werror` 严格构建（`cmake -DPINE_CPP_WERROR=ON` + 全量 build）
- 卫生检查：对 `pine-cpp` 下所有非 build 目录的 `*.cpp` / `*.hpp` 检查 trailing whitespace、tab 字符、缺失结尾换行
- 相邻字符串字面量拼接排查（typo guard）：捕获 `"" + var + ""` 形状——作者本意是把变量嵌进引号，编译器却静默拼接相邻字面量把引号丢掉（P0-1，五轮 code review 才抓到 `config.cpp` 一处）

**clang-format 是本地/人工约定，没有 CI 覆盖**：配置位于 `pine-cpp/.clang-format`（基于 Google style），约定应用于所有 `pine-cpp/` 源文件（`include/`、`src/`、`cmd/`、`operators/`、`tests/`），本地通过 `clang-format -i`、编辑器集成或 `pre-commit` hook（staged 文件、可绕过）执行。`grep -rn "clang-format\|fmt-check" .github/workflows/` **零命中**——`make fmt-check` 里的 clang-format 检查在 CI 里没有任何对应 job。副作用：本机若未安装 clang-format，`make all` 会在 `fmt-check` 处以 Error 127 中止，这不是代码问题。是否给 CI 补 fmt-check job 记在 `llmdoc/memory/doc-gaps.md`。

## 本地 git hooks

仓库提供 `.githooks/` 作为统一的本地质量入口，开发者通过 `git config core.hooksPath .githooks` 启用。CI 环境（检测 `CI` / `GITHUB_ACTIONS`）短路所有 hook，不与流水线重复。

- **`pre-commit`** — 仅对**本次 commit 已 staged** 的源文件运行 file-level 格式检查（`*.cpp/*.hpp/*.cc/*.h` 走 `clang-format --dry-run --Werror`、`*.go` 走 `gofmt -l`、`*.py` 走 `ruff check`），违规直接中止 commit 并提示精确修复命令。范围限定 staged 文件，避免历史污染拖慢单点提交。
- **`pre-push`** — 两段式：先运行各子项目的工程级 linter（`golangci-lint` / `checkstyle` / `ruff` / `clang-format`），失败则中止 push；通过后自包装执行真实 push，并阻塞等待远端 PR 的 CI 结果，最终打印 ✓/✗ 报告。**外层 `git push` 的退出码因自包装语义不可信**，应以 hook 自身报告与 `scripts/check-pr-ci.sh` 输出为准。由于 git 不会把外层命令行参数透传给 hook，自包装的 inner push 无从得知用户是否输入 `-u`；hook 会在 refspec 扫描中检测「当前 HEAD 所在分支正被推送且尚无 upstream」，并向 inner push 注入 `--set-upstream`，使新分支首推即自动建立追踪（已追踪分支 / detached HEAD / 仅推 tag / 不含当前分支的推送均不受影响）。详细行为与环境变量配置见 `.githooks/README.md`。

这两层 hook 与 CI 形成"commit 阶段拦格式 / push 阶段拦工程级 lint / CI 兜底"的纵深结构，避免 clang-format 等纯格式问题只能在 push 后被 cpp-lint 反弹。

## 覆盖率

- Go：`go test -coverprofile=coverage.out -covermode=atomic`，产物上传为 artifact（保留 30 天）
- Python DSL：`pytest-cov` 输出 XML 报告，产物上传为 artifact（保留 30 天）

当前无硬性覆盖率阈值。覆盖率报告用于趋势观察，不作为门禁。

补覆盖时优先覆盖“可稳定断言的行为边界”，例如：

- HTTP handler：使用 `httptest.NewRequest` + `httptest.NewRecorder` 直接调用 handler，验证状态码、响应体与参数分支，而不是优先启动真实 server。
- 算子单测：优先覆盖 `Init`/`Execute` 主路径、默认值、降级路径和错误路径，而不是只补 happy path。
- 带外部依赖的算子：优先使用内存替身以保留真实客户端交互但避免环境依赖；例如 Redis 测试使用 `github.com/alicebob/miniredis/v2`，不要求本地或 CI 提供外部 Redis。

以下路径通常不适合作为常规单测的优先目标，应在 coverage 评估时单独判断：

- 含 `log.Fatal`、`os.Exit` 等进程级退出逻辑的入口
- 含无限循环、文件监听、长期后台 goroutine 的 watcher/daemon 路径

这类逻辑若必须验证，优先拆出可独立断言的纯逻辑部分，再由少量集成测试覆盖整体接线。

## Fuzz

入口选择原则：优先覆盖高扇出输入边界。

### Go native fuzz

当前覆盖的入口：

- `pine-go/internal/config/` — `FuzzLoad`：JSON 配置解析、reserved key 过滤、展开序列引用完整性
- `pine-go/internal/dag/` — `FuzzBuild`：DAG 图构建、pred/succ 对称性、拓扑序合法性
- `pine-go/internal/dataframe/` — `FuzzApplyOutputStorageEquivalence`：RowFrame 与 ColumnFrame 的 ApplyOutput/ToResult 语义一致性
- `pine-go/internal/runtime/` — `FuzzDataParallelEquivalence`：data_parallel 多 shard 与单 shard transform 语义一致性

CI 中 fuzz 运行时间为 30s/入口，并使用 `-run=^$ -parallel=4` 固定为短时 smoke，用于回归防护而非深度探索。

### 差异模糊测试（Differential Fuzz）

`scripts/differential-fuzz.py` 生成随机管道配置，**Go/Java/C++ 三引擎**执行并比对输出：

- CI 模式：100 轮，发现分歧则 job 失败
- **Nightly 模式**（`.github/workflows/nightly-diff-fuzz.yml`）：固定 **10000 轮**，3 引擎 3 pairs 比对，发现分歧时自动创建 GitHub issue 附带复现 fixture（手动触发可通过 `inputs.rounds` 覆盖；周末自动升级模式已移除，统一由 `inputs.rounds` 显式控制）
- 分歧产物保存为 CI artifact，可直接下载复现
- Stability runs：每个配置执行 3 次以排除非确定性差异
- **15 个算子类型**（R3-X5 从 10 扩展）：filter_truncate, filter_condition, filter_paginate, recall_static, recall_resource, reorder_sort, reorder_shuffle_by_salt, merge_dedup, transform_by_lua, transform_copy（4 方向）, transform_dispatch, transform_size, transform_normalize, transform_resource_lookup, observe_log
- **Lua table-aware 用例**：`LUA_ITEM_FUNCTIONS` 覆盖 array input（`#item_tags`）、array 累加（`for i=1,#item_vals`）、array return（`return {a, b}`）三种 host ↔ Lua 复合类型路径，由 `random_items` 生成 `item_tags`/`item_vals` 数据
- **随机化维度**（完整清单以 `scripts/differential-fuzz.py` docstring 为准）：pipeline 拓扑 / 算子参数 / 数据形状 / 边界值 / data_parallel / storage_mode（50/50 row/column）/ SubFlow / skip / sources（显式 DAG 边）/ common_defaults+item_defaults / **defaults+nil 共现**（对声明了 item_defaults 的字段向源 items 打显式 nil 孔，定向触发 Defaulted 替换路径——含批量列访问 `ItemColumn` 的 defaults-copy 分支；summary 的 `defaults_nil` 计数该共现真实发生的轮数）/ debug+_return_trace / 请求直接提供 items / 稀疏 items（部分行缺字段）/ 嵌套 dict/array 值
- **跨存储模式比较**：每个 fixture 自动在 row 和 column 两种 storage_mode 下执行，输出在同一引擎内进行 row-vs-column 等价比较
- **Stabilize sort 条件收紧**：仅在 operator 有非 skip 的 `common_input` 时才附加 skip 字段，避免不必要的 skip 导致非确定性
- **Stratified 报告**：summary 输出 `row=A/B column=C/D` pass/fail 分布 + per-dimension 覆盖计数
- **`--time-budget-seconds`（wall-clock 优雅停止开关）**：默认 0（关闭），CI 模式与 Nightly 模式行为不受影响。非 0 时一旦耗尽即停止发起新轮，仍正常输出 `Results:` 汇总（标注为 `N/M rounds (time budget)` 而非 `M rounds`），使慢 runner 降级为"轮数变少但信号完整"而非丢失整个 pass。当前唯一消费者是 `daily-sanitized-fuzz.yml`（见下）。**改动 `Results:` 输出格式前必须先 grep 所有消费者**（当前含 `nightly-diff-fuzz.yml` 与 `daily-sanitized-fuzz.yml` 的 evaluate step），确认改动只在 `^Results:` 前缀之后扩展，不能变动前缀本身。

### 新增 fuzz 维度必须验证信号到达比对面

差分测试的探测能力上限由**比对面**决定，不由生成维度决定。历史教训（issue #175）：生成器在整个历史上从不发 flow_contract，三引擎把 common/items 全投影成 `{}`——差分比对看得见退出码、错误文案、item 数量与顺序，唯独从未看见任何计算出来的字段值，整类值破坏 bug 从构造上就不可见，30k+ 轮绿灯给了虚假的覆盖信心。现状机制：~40% 轮次发出 flow_contract 投影全部累计输出；item-mode Lua 轮次在上游存在 name/tag 字段时 25% 概率强制 identity 直通（`LUA_IDENTITY_ITEM_FUNCTION`）。

新增 fuzz 维度的规则：

- 度量**有效可见率**（危险值出现在被投影、被差分比对实际读取的输出里），不是**形状出现率**（危险值仅在某处被生成）——两个指标会因投影类盲区完全脱节
- 端到端验证探测能力：red-before（pre-fix 二进制 + 新生成器在真实生成轮上复现分歧）→ green-after（fixed 二进制通过同一轮）→ N 轮新鲜 fuzz 零假阳性

### 只生成「已经是期望形状」输入的生成器，检不出关于形状的 bug

新加检查之后必须**对着 mutant 验证它真的会红**，不能只看它在正确代码上是绿的。绿了先怀疑生成器，而不是被测代码。

issue #183 的实际经过：`key_order_signature` 加完之后，对着**故意改坏的** Java 序列化器仍然 1000/1000 全绿。根因不在检查，在生成器——`scripts/differential-fuzz.py` 生成 flow_contract 时写的是 `sorted(common_outputs)` / `sorted(item_outputs)`，声明顺序恒等于排序后顺序，于是「按插入顺序输出」的错误实现恰好与 Go 的排序输出一致，检查永远不触发。生成器改成 shuffle 之后，同一个 mutant 60 轮红 6 轮。

这与 issue #180 的「benchmark 取样恰好避开会反驳注释的区间」、以及 `04_number_precision.json`「名字像覆盖精度、实则只覆盖已对区间」是同一个失败模式：**样本回避了反例**。检查的探测能力上限由输入分布决定；输入分布恰好落在期望形状里时，检查等于没加。

### 新增的 gate 条件必须量化它为 true 的占比

给一个检查加条件（只在某种轮次里启用）时，先测这个条件在真实轮次里有多少比例为 true。判据是「关掉这个条件后检查还会红吗 / 这个条件为 true 的轮次占多少」，不是「条件读起来是否合理」。

issue #183 踩到的：`key_order_signature` 最初被 gate 在 `strict_order` 上，理由是「key 顺序只在 item 顺序确定时才有意义」。而 `strict_order` 只在管道以唯一键排序结尾时为 true，所以大部分轮次检查是关着的。理由本身也是错的——key 顺序与 item 顺序是**互相独立**的确定性维度。改成不 gate：item 顺序不确定时把各 item 的 key 序列当 multiset 比，每个 item 自己的 key 顺序仍然精确比。

同类前例：issue #180 的「perf 快路径三次都漏掉一整类输入」。共同点是**新加的守卫条件本身从未被验证过覆盖面**。

### 先确定属性有没有外部可观察信号，再决定门放哪一层

新增契约时先问一句：**进程外面能不能看出这个属性的差别？** 答案是「不能」时，不要试图在跨运行时通道上钉它——那种门恒绿，只会给出虚假的覆盖信心。有些属性的可观察面为空，不是通道做得不好，而是别的设计契约把差别吸收掉了。

issue #179（`storage_mode` 非法值选中不同物理存储）就是这类。两条实测依据：行列存输出对等本身是设计契约（cross-validate section 4 就在断言这个），`/stats` 与 `/dag` 都不含 storage 字段。实测的那组 `storage_mode` 变体（合法值 + 大小写不同 + 拼错 + 空值 + 未知值）在三个运行时的输出逐字节相同。于是分派分歧只改变内存与性能形式，不改变任何一个响应字节，回归门只能落在各运行时自己的 factory 单测上。

与上一条的关系：上一条讲输入分布回避了反例，这一条讲**比对面本身看不见这个维度**。前者可以靠改生成器修好，后者不能。

**这一条只回答「门不能放哪」，不回答「要不要门」**——后者见下一条，两条必须成对读。

### 无人可见的属性必然腐烂（上一条的另一半）

上一条的结论是「属性不可观察 → 别在跨运行时通道上钉它」。它容易被误读成「不可观察 → 不设门」，
而那是错的。补上的另一半：**不可观察不等于不需要门，门可以下沉到更便宜的层。**

判据：**一条跨运行时声明如果没有任何通道能看见它，要么补一个代价与可观察性相称的检查，要么
不要声称它一致。** 第三种做法（既没有门、又在文档里声称一致）会让分歧无声累积，而且文档反过来
让下一个人相信有保障。

issue #193 的实测依据：各运行时 metric 的 `# HELP` 文案由三方源码**各自独立声明**，而
`# HELP` 从不出现在任何输出（仓库不产出 Prometheus 文本格式），出厂 provider 也都不读 `Help`
字段（实测消费点计数为 0）。于是三方文案各自漂移，`pine_*` 指标里累积出的分歧数量远超 issue
点名的那几条（当前数量与清单以 `scripts/check-metrics-help-parity.py` 的输出为准，不在文档里
写死）。同期还有一处文档断言这些属性「由 cross-validate metrics-parity section 保证一致」，
而那个 section 读的是另一套机制——见 `guides/investigation-to-fix-testing.md` 的
「『由 X 保证』这类断言必须能追到具体脚本行」。

补的门与可观察性相称：`scripts/check-metrics-help-parity.py` 同时比对 Help 文案与 histogram 桶数组，是纯文本源码扫描，不需构建、
不需起服务，pine-go 是 source of truth，接入 `scripts/lint.sh`（即 `make lint`）。已按
「Mutation 验证的两步判据」验过：注入一处分歧变红、恢复变绿、`make lint` 端到端同样。
脚本有三道门，**审计发现最初只有一道且方向搞反了**：最初只在 pine-go 侧扫不到任何 `pine_*` 时才失败，
而 Java / C++ 侧正则失配会静默退化成恒绿；补上「缺失即报错」之后又发现真正危险的是 **Go 侧**——
Go 的 metric 集合是这个检查的基准，基准少一条时对称性检查看不见它，于是一条真实分歧连同那条 metric
一起消失（实测：Go 侧 `Name:` 与 `Help:` 之间插一行普通注释 + Java 侧注入真分歧 → 打印
「23 metrics, 0 mismatches」、exit 0）。现在三道门是：Go 侧 metric 条数下限断言（不只是非零）、
三方名字集合对称性（任一侧缺失即报错，Help 与桶各一份）、逐条值比对。**第四道门:扫描完整性自检**。前三轮各查出一种让 pine-go 侧扫描「看得比文件里少」的手法
（插注释、注释吸走绑定、折行拼接截断值），每次修完下一轮又换一种——**这不是四个独立 bug，而是正则扫描
的固有失效方式：它只报告匹配到了什么，从不报告本该匹配什么**。所以脚本现在用一个刻意更笨的方法
（直接数 `"pine_..."` 字符串出现）与解析结果对账，不一致即失败。它定位不了问题、且会过计数，
唯一职责是发现扫描变盲——而这件事任何正则打磨都做不到。**作用面要说准**（终审实测纠正过一次）：
它只覆盖 Help 扫描、且只覆盖「声明写法不再被匹配」这一类；值被截断时名字仍然命中所以它看不见，
桶扫描也没有对应自检。那两类分别由逐值比对与桶对称性接住——够用，但不是「泛化了前三轮全部手法」。

**判据**：一个「只比较子集」的
检查比没有检查更糟，因为它读起来像通过——所以让基准缩小与让比较失败一样响。

两条并排的结论：可观察性决定门放在**哪一层**（跨运行时通道 / 单测 / 源码文本扫描），不决定
**有没有门**。

### 新增校验段必须自陈它抓不到什么

一个校验段实际能钉住的属性往往窄于它读起来的样子。**新增校验段时在脚本注释里写明：它能钉住什么、抓不到什么、为什么抓不到、真正的门在哪几个文件。**

cross-validate section 21 是正面样本，而且它的断言**已经反转过一次**，这让自陈边界的价值更明显：#179 时它断言「非法值被静默接受 + 三方输出字节相同」，抓不到 dispatch 分歧本身（实测把 pine-cpp 的 fallback 改回去仍然全绿，mutation 验证过）；#187 让三方一律拒绝非法值，整段重写为断言拒绝对等。两个版本共有的那半边界不变：**合法值走哪个物理存储在这里永远不可见**，那半由各运行时的 factory 单测钉住。

脚本注释因此保留了旧断言作为历史（读到 #187 之前提交的人需要这个上下文），并分别写明拒绝对等那半的门在哪三个 validation 单测、分派那半的门在哪三个 factory 单测。判据：**契约反转时重写断言、保留旧表述并标注为历史**，不要直接删掉。

与 issue #183 那条被删掉的 gate 对照（既抓不到缺陷、又会对正确输出误报）：**承认边界并把门放到能放的地方，好过留一个看起来覆盖了、其实恒绿的检查。**

### Mutation 验证的两步判据

用 mutation（故意改坏被测代码）验证一个测试"有牙"时，必须分两步，不能合并：

1. **先证明 mutation 真的改变了被测语义**。mutation testing 的隐含前提从不被自动检查——一段看起来更激烈的改写可能与原代码语义完全等价。
2. **再判断测试有没有牙**。只有第 1 步成立时，"测试没红"才等于"测试没牙"。

因此「测试没红」的第一解释应当是**mutation 无效**，而不是「测试没牙」。顺序颠倒会把一个本来正确的断言改坏。

具体反例（issue #122 实际踩到）：为验证 `OperatorOutput::reset()`（`pine-cpp/include/pine/pine.hpp`）的容量保持断言有牙，把 `item_writes_.clear()` 改成 `item_writes_ = {}`，测试仍全绿。原因是 `= {}` 绑定 `operator=(std::initializer_list)` 并转发到 `assign()`，而标准库的 `assign` **从不缩减 capacity**——它与 `clear()` 在容量语义上完全等价，语义 delta 为零。改成真正的移动赋值 `item_writes_ = std::vector<ItemWrite>{}` 后立刻变红（`0 == 256`）。

推论：容量类断言的有效 mutation 必须是真移动赋值或 `shrink_to_fit`，不是 `= {}`。

### Artifact triage playbook（分歧定位顺序）

Nightly diff-fuzz artifact 分歧定位顺序：(a) 下载 artifact，解压 `divergence_NNNNNN/`，读 `info.txt` 确定 `divergent_pair` + 各方 rc；(b) 本地跑各引擎复现，对齐输出；(c) **末端错误文案往往误导**——报错的 op 常是"下游第一个观察到分歧的"，不是"真正产生分歧的"。若 rc/文案不一致但输出结构类似（如 items 顺序不同却都为空投影），从 pipeline 末端**逐算子向前截断**（保留 `flow_contract.item_output` 揭示中间字段），找到第一个 frame 内容开始分歧的 op。issue #174 就是 op_3 的 `transform_resource_lookup` 报错分散了排查方向，实际根因是 op_1 `reorder_shuffle_by_salt` 的 shuffle 顺序不同。

判据：本地复现走弯路超过 30 分钟时，条件反射式切"从末端逐算子截断"策略，不要继续深挖末端错误路径。

### fuzz 失败必须自带复现命令

**随机化测试报告失败时，报告本身要包含把这次失败重新跑出来的完整命令**，不能只在开头 header 里印 seed。

理由是两条实测约束叠加：seed 每次随机，长跑 nightly 的轮数很大，光有 seed 还得猜是哪一轮；而保存的 divergence 目录落在 `/tmp`，会被系统回收。issue #190 能被诊断纯粹靠一份恰好残留的副本。

现状：`scripts/differential-fuzz.py` 在有 fail 或 unstable 轮次时打印两行 `REPRODUCE:`——第一行是失败/unstable 的 round 号列表，第二行是可直接粘贴的完整命令（带 `--rounds` / `--seed` / `--engines` / `--save-dir`）。新增随机化测试脚本或改动其汇总输出时保持这条：**失败摘要与复现命令同处一屏**。注意 `Results:` 前缀有下游消费者（见上文 `--time-budget-seconds` 条），复现行加在其后、不要动前缀本身。

### 长跑 differential-fuzz 期间不得触碰构建产物

**长跑差分 fuzz 与重编译不能并行。** fuzz 每轮都在调各运行时的二进制，构建过程中被读到的半写状态产物会产出与代码无关的分歧。

实测症状（issue #189/#190 期间自找的）：在 fuzz 跑的同时重编译 Java，收到 8 个假分歧，形式统一为 `java_rc=1` + 空输出，全部源于半写状态的 `target/classes`；一度被误判为自己的修改引入了新缺陷，逐个复跑才排除。

要求：需要改代码就先停 fuzz，或让 fuzz 指向独立的构建目录 / 二进制快照。凡是 fuzz 报出的分歧带「某一方 rc 非零且输出为空」这种形状，先问在那段时间里有没有构建在跑，再去读分歧内容。与 `guides/benchmark-hygiene.md` 的 bench 噪声纪律（zombie 进程、并行负载互相污染）同族：**共享机器上的长跑测量，先排除环境竞争再解释数据。**

### 校验通道能钉住的属性（归一化 vs 字节级）

**通道各自看得见响应的哪一部分，与它归一化什么同样重要。** issue #183 的 key 顺序修复
第一版漏掉了 `/execute` 的 trace 快照与 `/stats`，而当时刚被加强的两条通道
（09-raw-byte、differential-fuzz）**都走 CLI**，CLI 输出只有 common/items——既没有
trace 也没有 `/stats`。唯一能看到那两处的 06-server-http 当时把 `trace[0].keys()`
排序后再比，正好把待测维度排掉了。三条通道同时看不见同一处，不是巧合而是因为没人问过
「哪条通道能看到这个字段」。判据：**新增契约时，先确认哪条通道能看到承载它的那个响应
字段**，再看该通道对这个维度是否归一化。

差分 fuzz 与 cross-validate 大部分通道在比对前做**归一化**，因此有整类属性对它们结构上不可见。新增契约时必须先问「哪条通道会红」，而不是「测试是否全绿」。issue #180（JSON 数字格式跨运行时分歧）暴露的通道能力如下，issue #183 之后 key 顺序一栏已经补上。

**differential-fuzz 的 `normalize_json` 抹掉 key 顺序与绝大多数数字字面量差异，key 顺序另有专门比对面。** `scripts/differential-fuzz.py` 的 `normalize_json` 做 `json.loads` → `_normalize_value` → `json.dumps(sort_keys=True)`，`sort_keys=True` 使 key 顺序在这条比对面上不可见；`_normalize_value` 只对 `float` 分支做 `round(v, 10)` 与小量级归零，`int` 分支原样穿过。issue #183 因此长期没被这条比对面抓到。现状：另有 `key_order_signature()` 用 `object_pairs_hook` 从原文读出 key 顺序**单独比对**，与值比对并行，数值容差与 item 顺序归一化都保持不变；item 顺序不确定时把各 item 的 key 序列当 multiset 比，单个 item 内部的 key 顺序仍然精确比。数字字面量的可见性边界没有变化（见下表）。

**#180 能被 fuzz 报出来靠的是 Python 的 int/float 类型分裂，不是设计出来的检出能力**：Go 输出 `100000000000000000000` 被 `json.loads` 解析成 `int`（原样穿过），Java 输出 `1.0E20` 解析成 `float` 再 re-dump 成 `1e+20`，两串才不相等。推论：**只有至少一侧输出整数形状字面量（无小数点无指数）时，数字格式分歧才可见**。实测的可见性分档：

| 分歧 | 归一化后可见 |
|------|------|
| Go `100000000000000000000` vs Java `1.0E20` | 可见 |
| Go `100000000000000020000` vs C++ `100000000000000016384` | 可见 |
| Go `9007199254740992` vs Java `9.007199254740992E15` | 可见 |
| Go `0.0000001` vs Java `1.0E-7` | 不可见 |
| Go `1e+21` vs Java `1.0E21` | 不可见 |
| C++ `1e-07` vs Go `1e-7` | 不可见 |
| 第 11 位起的精度差（`1.2345678901234567` vs `...68`） | 不可见（`round(v,10)` 抹掉） |

#180 实际有 15 个分歧，fuzz 结构上只能看见其中一部分。

**`scripts/cross-validate/09-raw-byte.sh` 现在是真字节通道。** 它曾经与标题 "no normalization" 不符：字节比较失败后会用 `normalize_json` 再比一次，相等就打 `[W]` 警告并**计为 pass**。那个回落**就是为容忍 issue #183 的 key 顺序分歧而存在的**，于是这条通道恰好检不出它唯一在容忍的那类字节差异。issue #183 已把回落删掉，字节不同即硬失败（91/91 两对全绿；把 Java 序列化器改回去立刻红）。剩下的唯一例外是 `strict_order: false` 的 fixture——item 顺序按设计不确定，那些 case 走 `normalize_json_set`，失败文案里显式写「values differ, not just key ordering」。

**`scripts/cross-validate/14-byte-exact-execute.sh` 也是真字节通道**（09 号修复前它是唯一一条）：curl 响应体直接 `==`，无任何回落。#180 给它补了 `fixtures/server_byte_exact/06_number_format_regimes.json`，覆盖 Go 各个格式化区间（输入 doubled 后分别落在 1e20 / 1e21 / 1.5e21 / 1e-7 / 1e-6 / 最短往返差异 / 1e16 / -1e20）。双向 mutation 验证过有牙：Java 序列化器改回 `writeNumber` → Go-vs-Java 变红；C++ 改回 `chars_format::fixed` → Go-vs-C++ 变红。

**原有的 `04_number_precision.json` 名字看起来正好覆盖数字精度，实际不可能抓到 #180**：输入 `100000 / 1000001 / 0.5`，×2 后全部落在 ±2^53 内的整数值区间——恰好是 Java 旧代码唯一处理对的区间。一个名叫 `number_precision` 却漏掉所有真正分歧量级的 gate，比没有 gate 更糟：它读起来像已覆盖。

**纪律：声称「字节级对等」的属性，必须有一条不做任何归一化的通道覆盖。** 归一化通道只能证明「语义等价」，不能证明字节等价。这与 `guides/cross-layer-validation.md` 的「fixture 比对器语义决定该层能钉住的属性」是同一条原则。

### fixture 要按响应形状枚举，不要等出事才补

**按事故增长的 fixture 集合，覆盖面永远滞后于声明。** section 14 的 fixture 在 issue #188 之前**增量部分全部**是事故驱动的（#180 补数字格式、#183 补三个 key 转义）；其余四个来自这条通道自己的创建提交 `b3be2503`，只覆盖最基本的成功／空／部分错误形状。也就是说这条通道建立时的初始覆盖面就很窄，此后每一次扩充都是被缺陷推动的，而「字节级对等」是全局声明，两者的差距就是从没出过事、但可能一直错着的形状空间。

#188 按响应形状事先枚举补了一批（校验错误 envelope、深层嵌套、null 出现在各个位置、过滤后多 item 投影），**第一个（校验错误 envelope）就查出一处既存分歧**而不是确认对等：pine-cpp 在校验错误时输出 `{"common":{},"items":[]}`，pine-go / pine-java 输出 `null`。这就是事故驱动覆盖的代价的直接证据。

判据：**新增字节通道 fixture 时枚举响应形状，而不是枚举已经出过的事。** 枚举维度是 envelope 种类（成功 / 校验错误 / 部分执行错误）、容器边界（空 common、空 items、多 item）、嵌套深度、null 位置、key 字符集、数字量级。

同时**这条通道结构性不可能覆盖什么**（是响应本身的性质，不是待填的缺口）：

- 含 `trace` 的响应——`trace[].duration_ms` 是真实测量值，body 永远不字节稳定。issue #183 试过并放弃。
- 来自 `/stats` 的响应——计数器与耗时，同一个问题。

这两类改由 `06-server-http` 的结构比较（`object_pairs_hook` 比 key **序列**、忽略值）与格式化器单测钉住。**在这里加一个 trace fixture 会得到一个对着正确代码失败的检查**，比没有检查更糟。section 14 的头部注释记录了这条通道覆盖什么、结构性不能覆盖什么，新增 fixture 前先读它。

### 穷举矩阵的测试会查出读代码查不出的缺口

**当分歧的形式是「某一侧缺一整块」时，跨运行时对读代码不可靠，穷举维度的矩阵可靠。** 缺失在阅读中是负空间（表现为「没有那一行」，比「有一行写错了」难看见得多），在矩阵里是一个空格子。

issue #187 的实例：写「根级字符串字段 × JSON 类型」矩阵时发现 pine-java 那一格无论填什么都不红，才暴露出 `_PINEAPPLE_CREATE_TIME` 在 pine-java 里**根本不存在**。此前读过三方 config 解析代码，没看出来。

判据：跨运行时对齐一类字段或一类路径时，写「维度 × 取值」的穷举测试，而不是对读实现。规则本身见 `reference/root-config-string-fields.md`。

### Daily sanitized-fuzz（ASan/TSan 深度诊断）

`.github/workflows/daily-sanitized-fuzz.yml` 每日 schedule 运行 pine-cpp 的 ASan+UBSan 与 TSan 两个 sanitizer-instrumented differential-fuzz pass，复用同一份 `scripts/differential-fuzz.py`：

- **与 Nightly differential-fuzz 的分工**：Nightly 用 Release 二进制追求原始吞吐覆盖（10k 轮/不同 seed），本 workflow 用 sanitizer 加持换取"内存越界/UAF/竞态类 bug 在首次触发时即可拿到完整栈"的深度诊断能力，二者互补而非替代关系。
- **两层 timeout 设计**：内层 `--time-budget-seconds` 是 pacing 机制，预算耗尽即停止发起新轮并正常输出 `Results:` 汇总；外层 CI `timeout` 降级为纯 hang 保护，只在进程真正卡死或脚本崩溃时才触发。
- **incomplete 信号语义单一化**：evaluate step 判定某个 pass "incomplete"（无 `Results:` 汇总行）现在只意味着真实 wedge 或脚本 crash——"慢但健康"的 runner 已经被内层 budget 兜住，不再落入 incomplete 分支。evaluate step 会从 `Results:` 行 parse 实际轮数（`N/M` 形式）写入 summary 表，使部分覆盖在报告中可见。
- 具体的 schedule cron、ASan/TSan 轮数、in-script budget / 外层 timeout / step timeout / job 超时的分钟数以 `.github/workflows/daily-sanitized-fuzz.yml` 文件本身（含其头部注释的标定依据）为准，禁止在本指南中硬编码——这些数值已因 runner 吞吐方差多次重新校准，注释里记录了数据来源的观测窗口。

### DAG 差异模糊测试（DAG Differential Fuzz）

`scripts/dag-differential-fuzz.py` 在 DAG 构建层面进行多引擎差异比对，与上述执行级差异测试互补：

- 生成随机管道配置，在 Go/Java 中构建 DAG
- 比对边集（依赖关系）和拓扑排序，而非执行输出
- 检测 DAG 构建逻辑的跨引擎不一致，即使执行结果恰好相同的情况也能发现

fuzz 通用策略分两步推进：

1. 先保证"不 panic"——fuzz 目标在任意输入下不应 panic
2. 逐步增加语义断言——对解析结果校验不变量（例如各引擎的 DAG fuzz 均断言"每个拥有 item 字段的算子在构建后的图中必须有 `_row_set_` 依赖边"这一行集安全不变量）

单个 fuzz target 应设置输入规模预算，避免随机大输入把 CI 变成解析器压力测试。

## Cross-validate 架构

`scripts/cross-validate.sh` 运行多 section 跨运行时校验。具体 section 列表以 `scripts/cross-validate/` 目录为准，禁止在本指南中硬编码层数。sections 默认并行执行（`scripts/cross-validate/_parallel.sh` 调度），`--serial` 可回退串行；单 section 内各运行时也并行运行。

CI 中 `cross-validate.sh` 的输出会被捕获到 `cross-validate-output.txt`，随后由独立的 `Fail on any divergence` step 在出现以 `FAIL:` 开头的行时显式 `exit 1`，使 CI job 失败。新增 section 输出格式时若使用其他失败标记，需确认能被该 grep 捕获，避免分歧被静默吞掉。

C++ 端是否参与某次比对取决于该 section 中对 `CPP_RUN` / `CPP_DAG` / `CPP_SERVER` / `CPP_CODEGEN` 的引用，以及 `scripts/cross-validate/_prebuild.sh` 是否成功构建 pine-cpp 二进制（输出到 `$WORK_DIR/pineapple-*-cpp`）。

`01-codegen-schema.sh` 现包含四个子段：

| 子段 | 校验内容 | 触发条件 |
|------|----------|----------|
| 1   | Go vs Java schema JSON 结构对比（operator 名、参数类型/默认值/必需性） | 总是运行 |
| 1b  | Go vs C++ schema JSON 结构对比 | `CPP_CODEGEN` 已设置 |
| 1c  | Go vs Java `apple_generated/` Python 产物字节级 `diff -r`（覆盖 `operators.py` / `__init__.py` / `markers.py` / `resources.py` / `resources_init.py`） | 总是运行 |
| 1d  | Go vs C++ `apple_generated/` 产物字节级 `diff -r` | `CPP_CODEGEN` 已设置 |

1b / 1d 显式区分 "二进制缺失（informational skip）" 与 "存在但产物分歧（fail）"，避免回归 byte parity 时被静默跳过。

### 端口隔离

各 section 使用独立的千位端口段避免并行执行时端口冲突（如 Section 4 用 4xxx，Section 6 用 6xxx）。每个 section 脚本内部通过 `BASE_PORT` 变量分配端口，确保各运行时 server 实例不会竞争同一端口。

### `set -e` 下断言失败的校验段，每个捕获都要 `|| rc=$?`

`scripts/cross-validate/_env.sh` 设了 `set -e`，这与「非零退出是预期结果」结构性冲突。**一个断言「必须失败」的校验段，它的每一次命令替换都要写成 `rc=0; out=$(...) || rc=$?`。**

冲突的表现极具误导性：issue #187 重写 section 21 后未加保护，脚本在第一个「预期失败」的用例上就直接中止、**连 rc 都没来得及读**，看起来像是循环没进去或者变量拼错，而不是像「有个命令失败了」——表现是静默跑完、一条 pass/fail 都不打。加上 `|| rc=$?` 后正常。

判据：写含负面用例的 section 时，先确认每个 `$(...)` 都带了捕获，再看断言逻辑。

### Section 4: Column-Store Row-vs-Column 比较

Section 4 (`04-column-store.sh`) 验证 `storage_mode: row` 和 `storage_mode: column` 在各引擎内产生相同输出：

- 同一 fixture 分别以 row 和 column 模式执行，比较输出一致性
- 覆盖 Go 引擎内部的 RowFrame vs ColumnFrame 等价性
- 使用与 Section 3 相同的比较策略（支持 `strict_order` flag）

### Section 3 执行 parity 的比较策略

Section 3 (`03-execution-parity.sh`) 比较三引擎 `/execute` 输出：

- **默认 list comparison**：`normalize_json` 规范化（递归 key 排序 + int→float 统一）后字符串精确比较。items 数组**顺序敏感**。
- **Set comparison**（fixture 声明 `"strict_order": false` 时）：`normalize_json_set` 额外对 items 数组按 JSON 序列化排序后比较。**顺序无关**，仅验证 item 集合一致。
- **适用场景**：fixture 有并行 DAG 节点（如多个 recall_static 无 trailing sort）时，item 插入顺序不确定，必须用 set comparison 避免假阳性。

### Differential fuzz 的 stabilizing sort 机制

`scripts/differential-fuzz.py` 在检测到 ≥2 recall-type 算子 + 下游 `filter_paginate` 时，自动在 paginate 前插入 `_stabilize_sort`（按 `_fuzz_distinctive_score` 排序），确保 paginate 输入确定性。这解决了"并行 recall → 非确定性位置 → paginate 切到不同 item 子集"的假阳性问题。

### Metrics Parity section

`scripts/cross-validate/13-metrics-parity.sh` 验证各运行时 pre-init 行为和 `/stats` 数值一致性，包含：

- zero-traffic pre-init：引擎启动后、无请求时 `/stats` 已暴露全部算子
- operator names match：各运行时的算子名集合一致
- exec_count / skip_count / error_count match：算子执行/跳过/错误计数一致
- scheduler.run_count match：调度器运行计数一致
- http.requests_total `POST /execute 2xx` 三方计数一致
- http.request_duration_seconds `POST /execute` count 三方一致
- `/stats.http` schema shape 三方一致（`requests_total` + `request_duration_seconds` 两子树存在 + duration bucket 含 `count`/`sum_ns` 字段）

**边界**：本 section 读的是 `/stats`，数据由 `runtime.Stats` 供给，与 `metrics.Provider` 是分离的
两套机制。它抓不到 Provider 侧的 metric `Help` 文案与 histogram 桶边界——那两项由
`scripts/check-metrics-help-parity.py`（Help 文案与 histogram 桶数组，接 `make lint`）覆盖（issue #193）。
名字里的 "metrics-parity" 曾被一份文档误读成覆盖了 Provider 侧属性。

### Section 15: Error Cause Chain Parity

`scripts/cross-validate/15-error-cause-chain.sh` 用 **probe binary 矩阵**验证三运行时的 ExecutionError cause chain 输出一致 -- 这是 cross-validate 框架的第二种验证模式（第一种是 fixture-driven HTTP 字节对比）。

每方 probe binary:
- pine-go: `cmd/pine-cause-chain-probe/main.go`
- pine-java: `page.liam.pine.CauseChainProbe`
- pine-cpp: `cmd/pineapple-cause-chain-probe/main.cpp`

probe 流程：构造 `FakeRedisError("user:42")` -> 包装为 ExecutionError -> catch 外层 -> 用语言原生 idiom 取出 inner -> stdout 输出 `PASS:key=user:42 not found`。Section 15 收集三方 stdout 做字节级 diff。

适用场景：当 parity 维度在 HTTP 接口不可见时（语言层 API 形态、原生能力可用性），用 probe binary 把维度具象化为可比对的 stdout 字符串。

## 跨引擎 Benchmark 基础设施

`scripts/` 提供跨引擎性能对比工具链：

- `scripts/bench-generate-fixtures.py`：生成 small/medium/large 三档 fixture，用于标准化性能基准
- `scripts/cross-engine-bench.py`：HTTP server 模式跨引擎 benchmark，测量 per-request latency（median/p95/p99）+ 并发 RPS
- `scripts/cross-engine-bench-cli.sh`：CLI 模式快速端到端延迟对比

## 并发压力测试

默认测试可包含轻量 HTTP 并发覆盖。服务器级重压测试必须用环境变量显式开启，例如 `PINEAPPLE_STRESS=1 GOMAXPROCS=$(nproc) go test -race -run TestServerHighConcurrencyStress -count=1 -timeout=10m ./pine-go/pkg/server/`。

重压测试默认不进 CI 门禁，适合在多核服务器、本地 release 前或排查竞态时运行。

HTTP 吞吐 benchmark 使用 `BenchmarkHTTPServerComplexDAGThroughput`。通过 `-args` 控制复杂 DAG：`-pineapple.bench.depth`、`-pineapple.bench.width`、`-pineapple.bench.fanin`、`-pineapple.bench.work`、`-pineapple.bench.items`、`-pineapple.bench.workers`、`-pineapple.bench.reload`。

## Codegen 目录边界

以下目录为生成产物，由 `pine-go/cmd/pineapple-codegen` 生成：

- `apple_generated/`
- `doc/operators/`

规则：

- lint 工具应排除这些目录
- 产物中的问题通过修改 codegen 源（`pine-go/pkg/codegen/`）或算子 Schema 解决
- CI 的 `codegen-check` job 通过 `git diff --exit-code` 强制产物与当前 Schema 一致

## Release gate

Release workflow（`.github/workflows/release.yml`）不包含质量检查 job。

触发机制：

- `on: workflow_run` 监听 CI workflow 完成
- `github.event.workflow_run.conclusion == 'success'` 确保 CI 通过
- `startsWith(github.event.workflow_run.head_branch, 'v')` 区分 tag push 和普通 push

此设计确保 release 仅在 CI 全部通过后才触发，同时避免重复执行质量检查。

Pine-Java 通过 Sonatype Central Portal 发布到 Maven Central（release profile 包含 source/javadoc/GPG 签名）。

`scripts/tag-release.sh` 是创建双 tag（`vX.Y.Z` + `pine-go/vX.Y.Z`）的标准路径，自动校验五处版本源一致后创建 tag 并推送。

## 检索指针

- CI 配置：`.github/workflows/ci.yml`
- CI apt 安装 wrapper：`scripts/ci-apt-install.sh`
- Agentic workflow 环境准备脚本：`.github/agentic/setup.sh`（由 `agentic-pr-review.yml` / `agentic-llmdoc-updater.yml` 的 `setup_script` 输入引用）
- Nightly differential-fuzz：`.github/workflows/nightly-diff-fuzz.yml`
- Daily sanitized-fuzz：`.github/workflows/daily-sanitized-fuzz.yml`
- Nightly cross-runtime benchmark：`.github/workflows/nightly-benchmark.yml`
- Release 配置：`.github/workflows/release.yml`
- Go lint 配置：`.golangci.yml`
- Python lint 配置：`pyproject.toml` `[tool.ruff]`
- C++ lint 配置：`pine-cpp/.clang-format`
- Go fuzz 入口：`pine-go/internal/config/load_test.go`、`pine-go/internal/dag/dag_test.go`、`pine-go/internal/dataframe/dataframe_test.go`、`pine-go/internal/runtime/parallel_test.go`
- Differential-fuzz 脚本：`scripts/differential-fuzz.py`、`scripts/differential-fuzz.sh`
- DAG differential-fuzz 脚本：`scripts/dag-differential-fuzz.py`
- Cross-validate section 列表：`scripts/cross-validate/`
- Cross-validate raw-byte（真字节，仅 `strict_order: false` fixture 走 set 归一化）：`scripts/cross-validate/09-raw-byte.sh`
- Cross-validate 真字节通道：`scripts/cross-validate/14-byte-exact-execute.sh`、`fixtures/server_byte_exact/`
- JSON key 顺序对等规则：`llmdoc/reference/json-key-order-parity.md`
- Cross-validate metrics-parity section：`scripts/cross-validate/13-metrics-parity.sh`
- Cross-validate pine-cpp 预构建：`scripts/cross-validate/_prebuild.sh`
- 跨引擎 benchmark：`scripts/cross-engine-bench.py`、`scripts/cross-engine-bench-cli.sh`、`scripts/bench-generate-fixtures.py`
- 跨运行时 benchmark（nightly）：`scripts/bench-cross-runtime.sh`、`scripts/bench-compare.py`、`scripts/bench-analyze.py`、`scripts/bench-dag-scheduler.sh`、`scripts/bench-profile.sh`（perf/gprof profiling）
- Benchmark fixtures：`fixtures/benchmarks/realistic_for_you.json`、`fixtures/benchmarks/realistic_for_you_calibrated.json`（iteration-based 校准）、`fixtures/benchmarks/transform_heavy_1000_config.json`（合成列存 guardrail，见 `guides/benchmark-hygiene.md`）
- Tag release：`scripts/tag-release.sh`
- Server stress 入口：`pine-go/pkg/server/server_test.go`
