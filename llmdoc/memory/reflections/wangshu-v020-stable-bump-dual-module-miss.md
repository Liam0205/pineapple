---
name: wangshu-v020-stable-bump-dual-module-miss
description: wangshu rc5→v0.2.0 stable 版本 bump(PR #166,3 commits)复盘,记录手工 go mod tidy 漏掉 pine-go/benchmarks 子 module(应走 make tidy 覆盖双 module)、CI 失败后未第一时间读 PR review bot 评论就去挖日志(bot 诊断早于且优于 CI 日志)两条教训
type: reflection
---

## Task

将 pine-go 依赖的 wangshu 从 v0.2.0-rc5 升到 v0.2.0 正式版(PR #166,3 commits)。

升级前审计:上游 diff 只有 1 个 commit(`a8c2b461`),只碰 `.githooks/pre-push`,非库代码;go.sum 里 `/go.mod` 哈希与 rc5 一致,证明模块定义等价;API 面与 rc4 相同(pine-go 自 #115 起追踪的版本下限)。

三个 commit:
- `90f5969c` 主 module bump(`pine-go/go.mod` + `go.sum`),双 tag(默认 wangshu / `-tags=lua_gopher`)测试绿。
- `4332564b` 文档版本下限同步(`reference/lua-backend.md` rc4→v0.2.0 谱系、`overview/project-overview.md` 里过时的 `v0.1.4+` 一并修正、`index.md` 同步)。
- `371b135d` benchmarks 子 module 补救 commit。

## Expected vs Actual

- Expected:主 module bump + 双 tag 验证 + 文档同步三步走完,CI 全绿合并。
- Actual:CI 的 benchmark job 失败——`pine-go/benchmarks/` 是独立子 module(自己的 `go.mod`/`go.sum`,indirect pin wangshu),第一个 commit 只 tidy 了主 module,子 module 的 wangshu pin 仍停在 rc5。Go 模块图取 max(rc5, v0.2.0)=v0.2.0,但 `benchmarks/go.sum` 缺 v0.2.0 条目,`-mod=readonly` 下直接拒绝构建("updates to go.mod needed")。需要第 3 个 commit `371b135d` 补救。此外 PR 的首轮 review 是 bot 给出的 REQUEST_CHANGES,精确诊断了这个子 module 问题(含 MVS 分析 + `make -C pine-go tidy` 修复建议),但当时没有第一时间读这条评论,而是从 CI 日志独立定位问题,直到用户提醒"你大概会漏看 PR comments"才回头看。

## What Went Wrong

1. **手工 `go get` + `go mod tidy` 只覆盖了当前所在的 module,没有触达 `pine-go/benchmarks/`。** 事后发现 `pine-go/Makefile` 里 `tidy` target 本就明确覆盖两个 module:
   ```
   tidy: ## go mod tidy(主 module + benchmarks 子 module) + git diff 守门
       go mod tidy
       cd benchmarks && go mod tidy
   ```
   工具链早就把"这个仓库有几个 module 需要同步"这件事编码进了 `make tidy`,但升级时手工跑了 `go mod tidy`(只在主 module 目录下执行),等于绕开了这层保护而不自知。
2. **CI 失败后没有优先读 PR review comment。** bot 首轮 review 在 CI 完全跑完之前就已经发出,且诊断比日志更完整(直接给出 MVS 冲突分析和 `make -C pine-go tidy` 的修复命令)。但当时的动作顺序是"看 benchmark job 失败日志 → 独立推理出子 module 未同步 → 才想起去看 review comment",绕了一圈才到达 bot 已经给出的结论。

## Root Cause

- **多 module 仓库的"哪些 module 要同步"信息只存在于 Makefile/脚本入口里,不存在于直觉里。** 手工执行底层命令(`go mod tidy`)天然只作用于当前工作目录对应的 module,而"这个仓库有几个 module"这件事本身不是 `go` 工具链能替你想起来的,只有仓库自定义的封装入口(`make tidy`)才把这层拓扑关系显式固化下来。绕过封装入口直接调用底层命令,等于放弃了这层保护。
- **判断"先看什么"的优先级时,把"最近生成的信号"当成了"最先应该看的信号"。** CI 日志是在任务执行过程中被动等到的,而 PR review comment(尤其是 agentic review bot)往往先于 CI 完成就已可用,且诊断质量不低于甚至高于原始日志。没有建立"CI 一失败,先扫一遍 review comments 再深挖日志"这个默认动作,导致重复劳动。

## Missing Docs or Signals

- 没有一条通用提醒:"依赖版本 bump 类任务,必须使用仓库自带的 tidy/build 封装入口(`make tidy` 等),不要绕过封装直接调用底层命令(`go mod tidy`/`go get`)"。这条目前只存在于 Makefile 注释里,靠踩坑发现。
- 没有一条通用提醒:"PR CI 失败时,先读 review comments(尤其 agentic bot)再挖日志"——这条在本仓已经不是第一次被提及(见 `feedback_no_manual_check_pr_ci.md` 讨论过 CI watch 相关注意事项),但"失败后看什么顺序"这个具体动作还没有被显式沉淀。

## Promotion Candidates

- **依赖 bump 必须走仓库自定义 tidy 入口,不要手工调用底层命令**——候选归入 `guides/` 下与 Go 依赖管理相关的条目(如有)或 `must/` 里的通用工程纪律。核心动作:bump 后除了跑封装入口,还可以反向核查 `grep -rn "旧版本号" --include="go.mod" --include="go.sum" .` 全仓扫一遍确认没有遗漏的 module。
- **CI 失败优先级:先扫 review comments 再挖日志**——候选归入 `must/` 或既有的 CI 相关反馈条目旁,与"不要主动跑 check-pr-ci.sh"那条(`feedback_no_manual_check_pr_ci.md`)属同一主题域,可以合并考虑是否值得沉淀成一条"PR 出问题时的排查顺序"通用指引。
- rc→stable 升级审计三件套(逐 commit 审上游 diff、go.mod 哈希对比证模块定义等价、双 tag 测试纪律)这次做对了,且"无关提交"也亲自核实过是真无关(`.githooks` 非库代码)——这部分不算问题,不需要 promotion,仅作为本次任务里正面对照记录。

## Follow-up

- 代码层面已随 `371b135d` 修复,无需额外动作;PR #166 待用户确认后续走向(merge/push 均需等用户指示)。
- 建议下一次 llmdoc 更新时评估:是否要在依赖管理相关指南或 `must/` 里新增"多 module 仓库 bump 依赖必须用封装入口"和"CI 失败先看 review comments 再看日志"这两条通用提醒;若已有相近条目可以合并扩展而非新增重复条目。
