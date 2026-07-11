---
name: ci-apt-resilience-and-dead-weight-packages
description: 修复 apt 慢镜像二次击穿 CI（#125→#164）复盘，记录单发安装+整段超时结构性缺陷需重试层而非更大超时数值、死重包（cmake/ninja-build/build-essential）放大慢镜像暴露面 10 倍、依赖断言比隐式依赖预装更健壮、go fuzz "context deadline exceeded" 无 corpus 文件即为 coordinator flake 四条教训
type: reflection
---

## Task

修复 `.github/workflows/*.yml` 中 apt 安装 C++ 构建依赖反复被慢镜像拖垮的问题（commit `c04d73e3`，PR #165，closes #164）。

2026-07-10 nightly diff-fuzz run 在 "Install C++ build deps" step 死掉：Azure archive mirror 以 ~26 KB/s 供给 cmake 包（11.2 MB，433 秒才下完这一个包），`timeout 600` 在下载中途 trip（exit=124），机器人开 issue #164。这是第二次同类故障：#125（2026-06-18）当时的"修复"是把 timeout 从 300s 提到 600s——静态加大超时只是把悬崖挪远。

修复两部分：

1. 新增 `scripts/ci-apt-install.sh`：apt-get update + install 各自最多 3 次尝试、每次独立 per-attempt timeout（默认 300s）、尝试间 backoff（`attempt * 10`s）、kill 后 `sudo dpkg --configure -a` 修复半配置状态、`Acquire::Retries=3`（覆盖单次尝试内的连接中断）+ `DPkg::Lock::Timeout=60`（等竞争锁持有者，如 unattended-upgrades）。
2. 全部 12 个 apt 站点迁移（`ci.yml` 7 个 + `nightly-diff-fuzz.yml` + `daily-sanitized-fuzz.yml` + `nightly-benchmark.yml` + `nightly-sanitizer.yml` 2 个），包清单瘦身到 image 真缺的：`libluajit-5.1-dev`、`libcurl4-openssl-dev`（+ sanitizer job 的 `util-linux`、cross-validate 的 `redis-server=5:7.*`）。install step 新增 `cmake --version` / `g++ --version` 断言。

## Expected vs Actual

| 维度 | 预期 | 实际 |
| --- | --- | --- |
| 修复方式 | 沿用 #125 的模式，继续调大 timeout 数值即可收敛 | 同一模式已经用过一次仍然复发，说明问题根本不在数值大小；真正缺陷是"单发安装 + 整段超时"结构本身没有第二次机会——即使 mirror rotation 重试后大概率落到健康端点，旧结构也无法利用这个事实 |
| 包清单假设 | 包清单是"构建真实需要的依赖"，问题只在超时机制 | 审计发现包清单本身充满死重：cmake 是 runner image 早已预装的（3.31+，比 apt 的 3.28 新且 PATH 在前，apt 副本从未被真正使用过），而且 #164 致命下载正是这个从未被用到的包；ninja-build 全仓无 `-G Ninja` 引用（是 pre-Makefile 时代遗留）；build-essential/g++-13 在 noble image 上预装；libhiredis-dev 对应的 pine-cpp redis client 是 raw-socket 实现，全树无 hiredis 引用 |
| 暴露面量化 | 只有主观感觉"包好像有点多" | 有具体数字支撑决策：#164 清单归档 15.7 MB vs 实际需要 ~1.5 MB，慢镜像暴露面差 10 倍——这个数字直接来自"如果 #164 那次只需要下载真正缺失的包，会不会还撞上这次故障"的反事实推演 |
| CI 验证过程 | 迁移后应该一次性全绿 | PR 首跑 fuzz job（Go native fuzz，`FuzzBuild`）失败，报 "context deadline exceeded" @30.08s——但排查后与本 PR 无关（详见下方"插曲"），rerun 后 pass；迁移后的 ci.yml 7 个 apt 站点本身全部一次跑绿 |

## What Went Wrong

### 1. 没有立刻复用"调大超时数值"这条已经用过一次的老路

`timeout 300 -> 600` 已经是 #125 用过的修复模式，直觉上最快的动作是继续把 600 提到 900 或更高。但这条路径本身就是本次故障的历史成因：任何静态数值标定都只是把"击穿点"往后挪，只要 mirror 偶发比这次更慢（本例单包就花了 433s），下一次还会撞上同一堵墙。本次没有再简单复刻这条路径，而是先问"这个失败模式的本质是什么"，才发现真正缺陷是结构（单发无重试）而非参数（超时秒数）。

### 2. 包清单的死重不是显而易见的，需要主动审计而非默认信任

如果只盯着"怎么让 apt install 更抗打"，很容易忽略"这些包本来就该不该装"这个正交问题。cmake 死重尤其隐蔽——`apt-get install cmake` 表面上"成功"（不会报错，因为 apt 确实把它装到了系统里），但装出来的版本比 image 预装的旧且 PATH 排序在后，实际构建从未使用过它，纯粹是浪费下载带宽和暴露面。这类"看起来在工作、实际是死重"的依赖只能靠主动去核对"runner image 出厂预装了什么"（`cmake --version`、`g++ --version`、`dpkg -l` 之类）来发现，不会自己暴露出来。

## Root Cause

### 单发安装 + 整段超时结构天然没有利用"重试大概率恢复"这个事实

`timeout 600 sudo apt-get install ...` 这种结构下，一旦某个 mirror rotation 落到慢端点，整段预算就在这一次尝试里烧光，没有第二次机会——尽管 Azure archive mirror 的负载均衡通常会在下一次连接时把请求路由到健康端点。区分"结构性缺陷"和"参数标定不够"的关键问题应该是：这个失败模式在重试后是否大概率能自愈？如果答案是"是"（如本例的 mirror rotation），修复就该加重试层而不是加超时数值；如果答案是"否"（如真正的死锁/hang），加超时数值才是对症的。

### CI 包清单会随构建系统演进腐化成死重，且死重直接放大暴露面

ninja-build 是仓库从多生成器切换到统一 Makefile 构建后遗留的死代码式依赖——没有任何 commit 显式移除它，是"没人主动清理旧参照"的自然腐化结果。这类腐化不会自己报错（apt install 一直"成功"），只会持续放大故障暴露面：包清单越臃肿，撞上慢镜像端点的下载总量越大，被单个慢包拖垮的概率就越高。#164 的 15.7 MB vs 1.5 MB 是这个腐化过程的具体量化后果。

## Missing Docs or Signals

1. `ci-quality-baseline.md` 目前只描述了 fuzz/sanitized-fuzz/benchmark 的两层 timeout 设计模式（内层 pacing + 外层 hang-protection，来自 `sanitized-fuzz-time-budget-graceful-stop.md` 那次修复），没有覆盖"依赖安装类"步骤的重试层设计。两者虽然都是"结构性抗故障"而非"调参数"，但适用场景不同（fuzz 是长跑进程内部的优雅降级，apt 是短命令的多次独立尝试+backoff），指南目前没有把这个模式族收敛成一条通用条目。
2. 没有"CI 依赖包清单需定期对照 runner image 预装内容审计"的明文约定——cmake/ninja-build/build-essential 这类死重能存活到第二次故障（#125 到 #164 跨了近一个月）才被发现，说明这类审计目前完全依赖故障触发的被动排查，没有主动巡检节奏。

## Promotion Candidates

- **单发+整段超时 vs 分层重试+per-attempt超时 的选择准则**：候选归入 `ci-quality-baseline.md` 或新的通用 CI 韧性小节。核心判据：先问"这个失败模式重试后是否大概率自愈"，是则加重试层（每次独立 per-attempt timeout + backoff + 失败态修复动作，如本例 `dpkg --configure -a`），否则才考虑调大超时数值。这与 `sanitized-fuzz-time-budget-graceful-stop.md` 里"内层 pacing / 外层 hang-protection 两层 timeout"是同一方法论家族的另一种应用方式（一个是长跑进程内部优雅降级，一个是短命令外部多次独立尝试），若未来再出现第三个场景，值得把两者一起提炼成 `guides/` 下的通用"CI 长任务/易失败步骤韧性设计模式"条目。
- **依赖断言优于隐式依赖预装**：`cmake --version` / `g++ --version` 这类断言把"image 变更导致的依赖缺失"从"莫名编译错误"前移到"install step 明确报错"，属于可复用的通用工程实践，值得在 `ci-quality-baseline.md` 的相关 job 描述里补一句"预装工具应显式断言版本，不应隐式假设存在"。
- 具体的死重包名单（cmake/ninja-build/build-essential/libhiredis-dev）与具体的超时数值（300s per-attempt / 3 attempts / 15.5m worst-case）：这些属于会随 runner image 更新、构建系统演进漂移的实现细节，已完整记录在 `scripts/ci-apt-install.sh` 自身的注释与各 workflow 的 inline 注释里，不需要在 llmdoc 里维护第二份可能漂移的副本，仅保留在本篇 memory 中作为案例参考。

## Follow-up

1. **代码层面已随本次任务完成**：commit `c04d73e3`，无需额外动作。
2. **建议下一次 llmdoc 更新时执行**：给 `ci-quality-baseline.md` 补充一小节描述 `scripts/ci-apt-install.sh` 的重试机制与检索指针（当前指南对 apt 安装环节完全没有提及），并考虑是否要和已有的"两层 timeout"表述并列成通用韧性设计模式的两个案例。
3. **观察性 follow-up**：若未来再出现第三次同类 mirror 慢速故障（#125 -> #164 -> 下一次），应重新检视 `ci-apt-install.sh` 的 3 attempts / 300s per-attempt 标定是否仍然够用，而不是默认继续在这个脚本框架内简单调大数值——这正是本次要避免重蹈的覆辙。
4. **旁支教训（非本次核心但值得沉淀）**：PR CI 首跑遇到 Go native fuzz job（`FuzzBuild`）报 "context deadline exceeded" @30.08s 但无 "Failing input written" 输出（无 crash corpus 文件生成），排查后判定是 go fuzz coordinator 在 `-fuzztime=30s` 边界的收尾竞态 flake，与本 PR 改动无关（该 fuzz job 本身不含 apt step）。rerun 后 pass。区分依据：真正的 crash 会在 testdata/fuzz corpus 写入文件并打印 "Failing input written to"，仅有 "context deadline exceeded" 而无该文件产出时，应判定为 coordinator flake 走 rerun，而非误当作真实 fuzz 发现去排查 bug。
