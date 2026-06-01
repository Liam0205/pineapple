# Redis→ResourceManager 迁移 + pine-python 下线 PR 收尾反思

## Task

把 Redis 连接从"算子持有"（`Init` 里建 `*redis.Client`，随引擎退休泄漏）迁移到
"ResourceManager 托管"的句柄型资源（按 `resource_name` 借用、`interval:-1` 永不刷新、
热重载原子替换）。解决 issue #64 的 teardown 覆盖缺口。同会话内顺带下线 pine-python
运行时引擎、收尾 llmdoc 同步、CI job 改名、bump v0.9.8，开 PR #65 并推进到 CI 全绿。

代码实现（Go/Java/C++ 三引擎迁移、C++ `ResourceValue` 句柄通道）在更早会话完成；
本会话聚焦设计决策落盘、commit 历史整理（reorder）、文档同步与发布流程。

## 决策与取舍

### "难而正确"压过"小而易"

issue #64 的 minimal proposal 是给 `transform_redis_get`/`set` 各加一个 `Close()`，
复用 v0.9.7 的算子 `Closer`。这能止血泄漏，但是蚁穴方案——连接池本质是可共享、
可声明、配置驱动的基础设施，绑死在算子实例上意味着每个算子各持一份连接、无法跨
pipeline 复用、热重载语义割裂。最终选择更彻底的结构：**连接池归 ResourceManager，
算子退化为纯计算按名借用**。

由此固化出一条可复用的归属判据（已写入约定/设计文档）：
**可共享 + 可声明 + 配置驱动 → ResourceManager；算子自身派生、独占 → Closer。**
据此 Lua state pool 仍留在算子 `Closer`（算子私有、随引擎死），只有 Redis 迁走。
`Closer` 接口本身不删——它对 Lua 仍是 load-bearing。

## What Went Wrong / 踩到的坑

1. **"Python"在本仓库是双义词，是 llmdoc 全程的核心正确性风险**：`pine-python/`
   是已删除的**运行时引擎**，`apple/` 是保留的 **Python DSL 声明层（编译器）**。
   批量清理 pine-python 引用时，必须区分每一处 "Python" 指哪一个——删错就把 DSL
   层文档也连带删了。最终在 project-overview.md 顶部加了一条显式消歧注释固化这个区分。

2. **llmdoc baseline 误判**：起初以为"上次 llmdoc 更新"是某个本地 commit，按它界定
   更新范围。用户纠正"先 rebase onto origin/master"，fetch 后发现 origin/master 已有
   一个 GitHub Actions 自动生成的 llmdoc commit（覆盖了 v0.9.6/v0.9.7），范围被大幅
   收窄。**教训：界定 llmdoc 更新范围前，必须先 fetch origin 并检查是否有自动生成的
   llmdoc commit，否则会重复劳动或错配 baseline。**

3. **clang-format 在 commit 阶段无 gate，潜伏到 pre-push 才暴露**：pine-cpp 的
   Redis 文件格式违规在 commit 时无人拦截，直到 `git push` 触发 `.githooks/pre-push`
   跑 `clang-format --dry-run --Werror` 才报错（4 个文件）。C++ 代码若非由 C++ 主力
   流程产出，格式问题会一路潜伏。**改动 pine-cpp 后应例行 `clang-format -i`，或在
   commit 前本地跑一次 pre-push 等价检查。** 修复落成独立的 `style(pine-cpp)` 单域
   commit（用户选"追加新 commit 而非 squash 回源 commit"，符合"新 commit 而非 amend"）。

4. **分支保护核查路径**：用户问"能不能用 gh 改 required checks"。核查发现 master
   **没有传统分支保护**（`branches/master/protection` 返回 404），唯一的 ruleset
   只有 `deletion`/`non_fast_forward`/`creation`/`pull_request`(review=0)/
   `required_linear_history`——**没有 `required_status_checks` 规则**。所以 CI job
   改名根本不卡合并，无需改任何设置，我据此撤回了"改名会卡 PR"的错误提醒。
   **教训：声称"改 job 名会卡 required checks"前，先用 `gh api .../rulesets/<id>`
   核实仓库到底登记了哪些规则，别凭直觉。**

## 操作约定的实践印证

- **单域 commit 隔离贯彻**：CI 改名（`.github` 域）与 llmdoc 表格同步（docs 域）即便
  逻辑紧密也拆成两个 commit；bump v0.9.8 的跨语言版本号同步算 version 域单一职责，
  合为一个 commit 合理。
- **reorder split-then-reorder 零冲突**：只有一个 commit 真正跨域（pine-go+design_doc），
  沿原始拓扑序拆分后再按契约优先金字塔 cherry-pick，两次 Zero-Diff 校验均空。
- **pre-push hook 自包装**：hook 内部用显式 refspec 做 inner push（成功），outer push
  必然报 `remote rejected: cannot lock ref ... reference already exists`——这是预期
  产物，真正的判据是 hook 打印的 `✓ all CI checks passed`。但副作用是 upstream
  tracking 没设上，后续裸 `git push` 会 no-upstream，需显式 `-u`。
- **bump-version.sh 是发布标准路径**：跨 apple/_version.py、pine-go/version.go、
  pom.xml、pine-cpp kVersion、各 fixtures/testdata `_PINEAPPLE_VERSION` 同步（已不含
  pine-python），并跑 codegen+三语言测试+cross-validate。其 cpp 构建已用 `-j2`（OOM 防护）。
  脚本不 commit/tag/push，需人工 review diff 后落 commit。

## Missing Docs or Signals

1. **commit 阶段缺 clang-format gate**：质量门只在 pre-push，commit 时无提示。可考虑
   pre-commit 加 clang-format 检查，或在 `ci-quality-baseline.md` 标注"pine-cpp 改动
   commit 前手动 `clang-format -i`"。
2. **CI job 命名与所测对象脱节的隐患**：`python-test`/`python-lint` 实际只测 `apple/`，
   pine-python 删除后名实更不符。本次已改名 `apple-test`/`apple-lint` 并同步 llmdoc
   表格——这类"job 名 = 隐式契约"的同步要求值得在文档里强调。

## Follow-up

1. PR #65 等 review/approve 后**用 rebase 合并**（ruleset 禁 merge commit + 要求线性
   历史；保留契约优先金字塔历史结构）。
2. 合并进 master 后用 `scripts/tag-release.sh` 打**双 tag**（`v0.9.8` + `pine-go/v0.9.8`），
   不在 feature 分支打。
3. 下游迁移指引（已写入 PR #65 描述）：搜 `redis_addr` → 改 `resource_name` +
   `flow.resource(...)` 声明；依赖 pine-python 执行的服务切到 Go/Java/C++；重跑 codegen
   生成 `RedisConnectionResource`。
