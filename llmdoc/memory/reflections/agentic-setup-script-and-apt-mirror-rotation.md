# 评审器环境准备脚本接入 + apt 镜像轮转修复复盘

日期：2026-08-08。分支 `feat/pr-review-setup-script`，接在 PR #194（issue #193）合入之后。

起因：PR #194 的评审 bot 在结论里如实写了它没能跑完的检查——runner 缺 `ruff` 与 `golangci-lint`、JDK 不支持 `pom.xml` 要求的 `release 25`、因此 `make lint` 与 Java 测试都没执行完。上游 `agentic-workflow-template` PR #30 刚好加了 `setup_script` 扩展点，于是本次把它接上。

## 一、最重要的一条：读脚本看不出来的缺陷，跑一次就暴露

本次写的 `.github/agentic/setup.sh` 有两个缺陷，**都不是靠审读发现的，是靠真跑一遍发现的**，而且两个的症状完全一样、都极具欺骗性——"报告所有能力都不可用，实际什么都没装"，日志读起来像是"环境确实不支持"，而不像"脚本自己坏了"。

1. **`timeout` 是外部程序，调不了 shell 函数。** `timeout 30s setup_java` 里 `setup_java` 是 shell 函数，`timeout` 作为独立二进制根本看不见它，每个阶段以 `rc=127` 结束。修法是 `export -f` 后在 `bash -c` 里跑。
2. **仓库根目录不能从 `BASH_SOURCE` 推。** 上游 hook 的调用形式是 `cd "$repo_dir" && bash "$hook"`，而 `$hook` 在 `.trusted-base`——一个只含这一个脚本的 sparse checkout。按脚本位置推导根目录，每个阶段都会找不到 `pine-go/go.mod`。

两条的共性：**失败模式伪装成了环境的正常降级**。因为脚本被刻意设计成"失败不致命、把缺失能力报告给 agent"，一个坏掉的脚本和一个诚实报告受限环境的脚本，输出长得一模一样。这类"降级路径掩盖自身缺陷"的结构，必须有一次真实执行来区分，不能靠读。现已加 cwd 的三 marker 断言，让第 2 类问题以一条明确错误立刻失败，而不是伪装成五个阶段各自失败。

配套做法值得保留：用上游**真实的** `run-setup-hook.sh`（`gh api` 拉下来）在本地完整模拟一遍，而不是模拟"我以为 hook 会怎么做"。工作树洁净断言也是这样验证的——写一个 `touch ./stray` 的变体脚本，确认 hook 真的以 exit 1 失败（红），再确认真脚本通过（绿）。这就是 `ci-quality-baseline.md` 里 mutation 验证那条纪律在 CI 配置上的应用。

## 二、写下来的"会自愈"如果没实测，就是一句许愿

`ci-apt-install.sh` 是 #125/#164 两次慢镜像事故的产物。它的注释和 `ci-quality-baseline.md` 都写着「mirror rotation 通常会自愈」，本次读 `apt-transport-mirror(1)` 才发现**重试从来没有换过镜像**：

- runner 的 `sources.list` 指向 `mirror+file:/etc/apt/apt-mirrors.txt`；
- 手册明确：只有取用**失败**才 failover 到下一个镜像，且镜像按 `priority:` 升序尝试；
- #164 的失败形式是「镜像答应了，然后以 26 KB/s 爬」——这不算失败，failover 从不触发；
- 于是三次尝试全部回到同一个 `priority:1` 主机。per-attempt timeout 把「一个慢镜像」变成了「三个慢镜像」。

与 #193「无人可见的属性必然腐烂」同族，但失效方式不同，不要合并：#193 是**没人写下来**这条属性、也没有通道能看见它；本次是**写下来了，写的却是期望而非实测**。「重试后会落到另一个镜像」当时既没实测，也没有任何通道能看见它没发生——一个慢镜像日过去，CI 失败了，看起来正好印证「就是镜像慢」，反而强化了错误认知。

沉淀成纪律（已写进 `ci-quality-baseline.md`）：**凡是声称「某个机制会自愈」的注释，都要能说出它靠哪一条具体行为自愈。** 说不出来的，要么去实测，要么别声称。

## 三、修的时候顺手踩到的两个细节，值得单独记

- **轮转必须重写 `priority:` 数值，不能只调行序。** 文件里的行序不是 apt 遵循的东西。无显式 priority 的镜像排最后——所以「去掉 priority 让它随机」是个错误的简化方向。
- **必须按 tab 字段解析。** 格式是 URI + TAB + metadata（metadata 内部可用 tab 或空格分隔）。第一版把整行当字符串做 `sub()`，结果 `arch:amd64` 这类 metadata 被空格连回 URI，apt 会把空格算进 URI。这一条是**变异测试查出来的**：runner 当前的 mirrorlist 只有 `priority:` 一种 metadata，所以真实环境不会触发，属于潜伏损坏。造了「带 arch:」「URL 路径里含字面量 `priority:9`」「单镜像」「纯注释」「不可读」五个用例才钉住。

## 四、预算算术要算，不要估

pine-cpp 阶段给 600s。apt 默认 3 次 × 300s，update 与 install 各一轮，最坏 ~1900s——**apt 一个人就能吃掉整个阶段预算，cmake 永远轮不到**。试着压小 `ATTEMPT_TIMEOUT` 解决不了：算下来 2×120 / 3×80 / 2×90 全都留不下构建所需的时间。正确修法是**整体包一层 `timeout` 限制 apt 总时长**（300s），而不是压小单次尝试。

构建所需时间是实测的，不是估的：本地 configure 9s（含 clone ~70 MB 的 rapidjson + doctest）、`-j4` 构建测试目标 35s。有了这个数才敢说 300s 留给构建是宽裕的。这与 `reflections/sanitized-fuzz-time-budget-graceful-stop.md` 那条"预算标定要用实测 worst 而非快日均值"同源。

`-j` 并发也按仓库既有纪律封了顶（12），因为这个脚本也能在本地跑，裸 `-j$(nproc)` 在大开发机上是禁止的。

## 五、一条反直觉的时序，别改回去

`pr-review` 从 **PR base commit** 读 setup 脚本。所以**引入它的那个 PR 自己享受不到**——脚本在 base（master）上还不存在，走的是"声明了但可信来源里没有"的分支，降级为一条 warning。已对着真 hook 验证过这条路径。收益从下一个 PR 开始。`update-llmdoc` 从事件固定的 checkout 读，没有这个滞后。

这件事本身没问题（上游是有意如此，为了让当前 PR 无法改写准备脚本），但如果不预先知道，很容易在本 PR 的评审结论里再看到一次"缺 ruff / golangci-lint"，然后误以为接线失败、去改本来正确的代码。

## 六、做对了、可作正面对照的地方

- 版本钉的是 **CI 实际解析出来的值**，不是配置文件的字面写法：`golangci-lint-action@v9` 实际装的是 v2.12.2、`setup-python` 的 `"3.13"` 实际解析成 3.13.14。都去 PR #194 那次运行的日志里核过。照抄配置写法会得到一个"看起来钉了版本、其实和 CI 不同"的脚本。
- 阶段按最便宜在前排序、各自限时，于是一个病态阶段只损失它自己那项能力。pine-cpp 排最后不是因为它不重要，而是因为它是唯一一个历史上真的因为基础设施（而非代码）弄挂过 CI 的步骤。
- 上游文档、上游脚本、runner image 的 manifest 都是直接 `gh api` 拉原文读的，包括 mirrorlist 是怎么配出来的（`configure-apt-sources.sh`）、镜像清单里有哪三个 host。三个 host 是否都提供完整 noble archive 也实测确认了（否则轮转可能落到一个部分镜像上）。

## 待办与已知边界

- setup 脚本不覆盖上游的 `validate_codex` / `validate_claude` job（上游有意为之，validator 要在未经审查的候选提交上把关）。
- 需要认证的私有包索引不支持（上游已记录，给准备脚本传凭据会破坏"agent 进程不接收 GitHub/PAT 凭据"这条不变量）。
- Codex 与 Claude 在各自 runner 上各准备一次，无缓存约定，所以准备耗时是双份。
- 本次没有给 `setup.sh` 加自动化测试。它是 CI 配置代码，仓库里没有对应的测试层；目前靠的是"用真 hook 跑一遍 + 变异验证洁净断言"这套手工配方，已写进 `ci-quality-baseline.md`。是否值得一个离线契约测试（上游自己有 `scripts/test-pr-review-contract.py`）留作 `doc-gaps.md` 条目。
