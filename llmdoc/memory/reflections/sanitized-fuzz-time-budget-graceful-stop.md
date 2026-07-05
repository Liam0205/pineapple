---
name: sanitized-fuzz-time-budget-graceful-stop
description: daily-sanitized-fuzz.yml ASan pass 连续 8/10 天被内层 timeout 杀死复盘，记录预算标定必须用 worst 实测而非快日均值、长时任务 timeout 应分内层 pacing / 外层 hang-protection 两层、观察窗口决策树无 owner 即失效三条教训
type: reflection
---

## Task

修复 `.github/workflows/daily-sanitized-fuzz.yml` 的 ASan pass 反复被内层 `timeout` 杀死的问题：2026-06-24 至 07-04 的 10 次 run 中 8 次 exit=124，每次都只差 5-11%（2658-2859/3000 轮，~0.5 rnd/s，实测 worst throughput ~2.03 s/round）。被杀时脚本无输出机会，`Results:` 汇总行缺失，evaluate step 判定 incomplete，机器人连开 9 个同模板 issue（#142/#144/#146-152）。TSan 是下一个要炸的（07-03 跑了 63m52s，离 65m 上限不足 1 分钟）。

修复方案两层：

1. `scripts/differential-fuzz.py` 新增 `--time-budget-seconds`（默认 0=关闭），预算耗尽时循环停止发起新轮，仍输出正常 `Results:` 汇总（标注 `N/M rounds (time budget)`），保持 `^Results:` 前缀不变以兼容下游 grep 消费者。
2. `daily-sanitized-fuzz.yml` 按本次 worst 实测（2.03 s/round ASan / 1.92 s/round TSan）重新标定预算：ASan budget 105m / outer timeout 90m→110m / step 95m→115m；TSan budget 75m / outer timeout 65m→80m / step 70m→85m；job 180m→215m。语义随之改变：in-script budget 是 pacing 机制，外层 timeout 降级为纯 hang 保护，incomplete 从此只剩一种含义。

对应 commit：`b5ef7a93`。

## Expected vs Actual

| 维度 | 预期 | 实际 |
| --- | --- | --- |
| 修复范围 | 只需调大 timeout 数值即可解决 | 数值调整只是表面；真正问题是 all-or-nothing 结构本身——不管差多少轮，超时即全部作废，任何静态数值标定都会被 runner 噪声方差击穿 |
| 预算来源 | workflow 注释里已有 #132/#133 的 ~1.5/1.6 s/round 数据可直接复用 | 该数据是"快日"数字，06-24..07-04 的 worst case 实测是 2.03/1.92 s/round，快慢日方差高达 55%（6/27 快日 1.31 s/round vs 均值），复用旧标定必然复发 |
| 根因归属 | 期望能定位到某个单一"罪魁"提交拖慢了性能 | 排查后没有单一罪魁——6/23 的 #137 Redis cascade-safety 让 schema/fixture 面变宽一点，叠加 GitHub runner 噪声漂移，是多因素叠加而非可回滚的单点退化 |
| 观察窗口决策树 | #142 定义的触发条件（"若 06-25/06-26 再现 → 走 B 方案 bump timeout"）应该已经被执行 | 06-25、06-26 都确实再现了，但之后 8 天无人跟进，issue 越积越多到 9 个，决策树形同虚设 |

## What Went Wrong

### 1. 差点又走"调大超时数字"这条老路

最直接的反应是把 90m 改成 105m 或 110m 就完事——workflow 注释里 #132/#133 就是这么做的（从最初 smoke-run 投影的 ~1.4 s/round 调整到"实测" ~1.5/1.6 s/round）。但这条路径本身就是本次问题的历史成因：任何基于某个观测窗口"实测均值"的静态数值标定，只要 runner 噪声或输入面变宽，就会在下一个观测窗口被重新击穿。本次没有再简单复刻这条路径，而是先问"为什么这套结构会反复触发同一类故障"，才发现 all-or-nothing 是结构性缺陷。

### 2. 差分 fuzz 里的 divergence 排查踩了 rc 陷阱

本地用 go+java 验证功能正常，但用 go+cpp 验证时发现的"divergence"其实全部是 cpp 二进制 rc=127（本机缺 `libluajit-5.1.so.2` 共享库，属环境问题，不是真实行为分歧）。差分 fuzz 报告分歧前必须先看各引擎子进程的 rc，rc≠0 时该轮不能被当作真实语义分歧对待，否则会浪费时间去调查一个根本不存在的 bug。

### 3. 观察窗口决策树本身没有 owner 机制

#142 里 2026-06-24 的评论明确写了触发条件（"若 06-25/06-26 再现 → routine signal，走 B 方案 bump timeout"），06-25/06-26 确实再现，但接下来 8 天没有人执行这个已经写好的决策。机器人按日开 issue 只是把信号摆在那里，不代表信号会被消费。

## Root Cause

### CI 长时任务预算标定的默认输入偏差

写预算注释时天然倾向引用"手头有的实测数据"，而这些数据往往来自当时最近一次校准窗口（#132/#133 的 06-20/06-21 两天），样本量小、容易采到偏快的日子。真正需要的是显式的"worst case across a longer window"标定，并且要在注释里写清数据来源日期范围与快慢日方差幅度（本例 55%），否则下一个读这段注释的人会误以为这是稳定值可以直接复用。

### 长时任务 timeout 的单层设计天然脆弱

`timeout 90m python3 script.py` 这种单层结构下，timeout 触发的原因和"脚本本身健康度"完全绑死——不管是脚本卡死（真正需要报警的情况）还是脚本健康但恰好慢一点（应该优雅降级的情况），观测到的现象都是同一个 exit=124，外部无法区分。这种设计下任何静态数值都注定要在正常方差范围内被击穿。

### 决策树没有绑定 follow-up 责任人

写"若 X 再现则走 Y"这种条件语句本身不构成一个可执行的流程，除非有人被指派在条件满足时去执行 Y。自动化系统能生产信号（issue），但消费信号（读决策树、执行方案）仍然是人工环节，这个环节缺失时，再精确的决策树设计也不会自动生效。

## Missing Docs or Signals

1. **`ci-quality-baseline.md` 里的 fuzz 章节尚未收录 `daily-sanitized-fuzz.yml`**：该指南的"Fuzz"小节目前只覆盖 Go native fuzz、CI 模式差分 fuzz、Nightly 差分 fuzz、DAG 差分 fuzz 四类，没有提到 daily sanitized（ASan/TSan）fuzz 这条独立 workflow，也没有提到 `--time-budget-seconds` 这个新增开关。下次有人排查 daily-sanitized-fuzz 相关问题时，指南检索不到入口。
2. **没有"长时 CI 任务 timeout 分层设计"的通用规范**：目前只在这次 workflow 注释里体现了"in-script pacing budget + outer hang-protection timeout"两层模式，但这个模式本可以推广到其他潜在的长跑 CI 任务（如 nightly benchmark），当前没有落成一条可复用的设计准则。
3. **没有"CI 预算标定必须注明数据来源窗口 + 方差"的明文约定**：`ci-quality-baseline.md` 目前对各 job 的超时数值没有统一规范要求写明标定依据，未来再有人调整超时阈值时容易重复"拍脑袋改数字"的旧模式。

## Promotion Candidates

### 应补到 `guides/ci-quality-baseline.md` 的 Fuzz 小节

- 补充一段描述 `daily-sanitized-fuzz.yml`（ASan/TSan 深度诊断路径，独立于每次 push 的 fast 路径），说明其与 nightly-diff-fuzz.yml 的分工（后者是 Release 二进制 10k 轮吞吐覆盖，前者是 sanitizer 加持的 race/memory bug 深度诊断）。
- 补充 `--time-budget-seconds` 开关语义：预算耗尽后循环停止发起新轮但仍输出正常 `Results:` 汇总（标注 `N/M rounds (time budget)`），默认 0 关闭，ci.yml/nightly-diff-fuzz.yml 行为不受影响。
- 补充一条通用提示：改 `differential-fuzz.py` 输出格式前，先 grep 所有消费者（当前两处：`nightly-diff-fuzz.yml` 与 `daily-sanitized-fuzz.yml` 的 evaluate step），确认都 key 在 `^Results:` 前缀上，改动只能在前缀之后扩展，不能变动前缀本身。

### 可考虑新增到 `guides/` 的通用长时任务 timeout 设计模式

- **两层 timeout 分离**：内层脚本自带 pacing budget（优雅降级、保留部分信号），外层 CI timeout 纯粹作 hang 保护（含义单一，只在真正卡死或脚本崩溃时触发）。"被外层杀死"应与"脚本主动提前收束"在日志/汇总行上可区分（本例用 `Results:` 行是否存在 + `N/M` 标注区分）。
- 该模式当前只体现在 daily-sanitized-fuzz.yml 一处，暂不确定是否值得单独提炼成稳定文档条目；建议先观察是否有第二个类似场景（如 nightly-benchmark 类长跑任务）需要同款设计，再决定要不要促成通用准则。

### CI 预算标定规范（候选，可能属于 `must/conventions.md` 或 `ci-quality-baseline.md`）

- 任何 workflow 里写入的超时/预算数值，注释必须注明：(a) 数据来源的具体日期范围 (b) 该窗口内快慢日的方差幅度。本例的教训是：仅用两天数据（#132/#133 的 06-20/06-21）标定的"实测值"在扩大到 10 天观测窗口后被证明只是快日均值，实际 worst case 高出 35%+。

### 仅保留在 memory

- 具体的 s/round 数字（2.03 / 1.92 / 1.31 等）与具体 budget 分钟数（105m/75m 等）：这些数值会随 runner 硬件、sanitizer 版本、fixture 复杂度漂移，不属于跨时间稳定的契约，且已完整记录在 workflow 文件自身的注释里，不需要在 llmdoc 中重复维护第二份可能漂移的副本。
- 本次涉及的具体 issue 号（#142/#144/#146-152）：一次性事件的追踪编号，无复用价值。

## Follow-up

1. **代码层面已随本次任务完成**：commit `b5ef7a93`，两层修复 + workflow 注释重写 + README 一行同步，无需额外动作。
2. **建议下一次 llmdoc 更新时执行**：给 `ci-quality-baseline.md` 的 Fuzz 小节补充 daily-sanitized-fuzz.yml 条目（含 `--time-budget-seconds` 语义与两层 timeout 分工说明）。
3. **观察性 follow-up**：daily-sanitized-fuzz.yml 后续几次 run 若仍然频繁触及 105m/75m budget 上限（而不是像本次一样只在偶发慢日触发），说明 worst-case 标定本身又该重新提高，且这次应显式建一个"预算标定过期"的复查节奏（例如每季度或每次触发 N 次budget-stopped 后复查），避免重蹈"决策树写了没人执行"的覆辙。
4. **方法论沉淀**：以后任何"观察窗口决策树"类型的 issue 评论（"若 X 再现 → 走 Y 方案"），触发条件满足后应该有明确的后续动作追踪，而不是让机器人继续按日开新 issue 掩盖决策树已经失效这个事实本身。
