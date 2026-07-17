# 标准工作流程

本指南描述非平凡任务的完整执行流程。

## 适用范围

当任务涉及以下情况时使用本流程：

- 预期变更超过 2-3 个文件
- 需要跨 Python/Go 边界
- 涉及算子 Schema、codegen、DAG 语义等核心概念
- 需要新增或修改测试

单行修复、纯文档编辑、简单问答不需要完整流程。

## 流程步骤

### 1. 加载 llmdoc

非平凡任务开始前加载 `llmdoc` skill，按以下顺序读取：

- `llmdoc/index.md`
- `llmdoc/startup.md` 及其列出的文档
- 与任务相关的 `guides/`、`memory/reflections/`

目的：获取项目约定、架构背景和历史经验，避免重复踩坑。

### 2. Plan mode 对齐

进入 plan mode，在动手写代码之前：

- 用 Explore agent 并行调研相关代码，定位问题和影响范围
- 撰写计划文件，列出变更点、涉及文件、执行顺序、验证方式
- 退出 plan mode，等待用户审批

不要跳过对齐直接实施。计划阶段的投入能大幅减少返工。

### 3. 任务跟踪与实施

用 task list 将计划拆分为可追踪的离散任务，按以下子步骤推进：

#### 3a. 更新 design_doc

若任务涉及新功能或架构变更，先更新 `design_doc/` 中对应的设计文档。设计文档是功能设计的权威记录，代码实现应与其保持一致。

#### 3b. 编写代码

逐项实施代码变更：

- 每个任务标记 `in_progress` → `completed`
- 设置任务间的依赖关系（如先修 bug 再重生成 codegen）

#### 3c. 逐步验证

每完成一项变更立即验证：

- 运行相关测试（`pytest`、`go test`）
- 提交前必须运行对应语言的 lint，并确认 0 issues：Go 项目运行 `golangci-lint run ./...`，Python 项目运行 `ruff check`
- 确认无回归后再进入下一项
- 如果涉及 codegen，修复后立即重生成并检查产物

#### 3d. 更新 README

若变更影响用户可见的功能、API、使用方式或项目结构，同步更新 `README.md`。

#### 3e. 提交代码

所有任务完成、全量测试通过后，提交代码变更。

### 4. 更新 llmdoc

运行 `/llmdoc:update`：

- reflector 写反思记录
- recorder 更新受影响的稳定文档
- 同步 `llmdoc/index.md`

### 5. 提交 llmdoc 变更

将 llmdoc 更新作为单独提交（或与代码变更合并提交，视情况而定）。

### 6. Bump version

若本次变更构成一个版本发布：

- 运行 `bash scripts/bump-version.sh <new-version>`
- 该脚本自动同步 `version.go`、`apple/_version.py`、JSON fixture 中的 `_PINEAPPLE_VERSION`，并重新生成 codegen 产物、运行全量测试
- 脚本不会自动提交，需手动 review diff 后提交并打 tag

## 何时简化流程

- **单文件小修**：跳过 plan mode 和 task list，直接修改并验证
- **纯调研/问答**：只需加载 llmdoc 读取背景
- **用户已给出精确指令**：跳过调研，直接执行

## 关键原则

- **先读后写**：先理解现有代码和文档，再动手改
- **先对齐后实施**：计划经用户确认后再写代码
- **逐步验证**：每步改完跑测试，不要攒到最后一起验证
- **文档跟随代码**：design_doc、README、llmdoc 都要同步更新

## Review-driven scope expansion 接受

Review feedback 不是简单"修缺陷"——它常带新的 scope 增长信号，把 PR 推向更高价值终点。被 review 反馈推动 scope 增长是正反馈，而非"偏离原计划"。原则：

- 不要默认走折中方案（"先做一半，剩下做 follow-up"）。"打折"应是最后选项，不是第一选项
- review 反馈点的工作量评估应严格——不要因为"看起来工作量大"就推荐折中跳过；估错工作量推动错误方向
- 反向验证：当用户矫正"不要后退、不要打折扣"时，先 honor，复盘错估的根因（是真的工作量大、还是方向反了）

历史教训：0.10.10 redis cascade-safety 任务中，per-command metrics 是 review 第一轮后追加的（非原计划），最终拉高 PR 价值；同期 pine-cpp markdown emit 第一轮我推荐"折中跳过 metadata 节作为 follow-up" 被用户否决，最终全做完只比折中多约 1.5 小时。详见 `memory/reflections/redis-cascade-safety-and-observability.md`。

## 并行 worker 改动收集

多 worker 并行实现（各自 worktree 隔离）时的改动收集与审计约定：

- **worker 不 commit，主会话统一收集**：worker 在隔离 worktree 完成后不提交；主会话用 `git -C <worktree> diff > patch && git apply patch` 收 tracked 改动、`cp` 收 untracked 新文件，统一验证、统一提交后删除 worktree。注意 worktree 是独立副本，改动必须显式搬运；`cp` 报 "same file" 表示源和目标解析为同一文件身份（同一路径或同 inode——GNU cp 不比较内容），遇到时用规范化路径或 `stat -c %i` 核对 inode 定位原因；要判断内容是否已搬运，应显式 `cmp`/`diff` 比较，不要靠解读 cp 的错误文案。
- **给旧组件加新公开入口或修状态机 bug 时，重扫整个状态机**：审计动作分两层，历史案例均来自 issue #169 / PR #170 的 Java `PineServer`（详见 `memory/reflections/upstream-serverplus-custom-routes.md`）：
  - **可达性重扫**：之前"不可达"的状态会因新入口变成可达。`stop()` 只 release 快照不清引用，stop 后的退休快照在旧入口集合下无人访问；新增嵌入 `execute()` 后该状态变为可达，暴露 `acquireSnapshot()` 无限自旋的既有 bug（修复为 `getAndSet(null)`，对齐 Go `Close()` 的 `Swap(nil)`）。
  - **修一个状态机洞之后，重扫同一状态机的其他窗口**：首轮只修了 stop 后自旋（可达性洞），漏掉 stop 与在途 reload 的并发窗口——`shutdownNow` 只中断不等待、`loadConfig` 不检查中断标志，落后的 reload 可在快照拆除后重新发布快照（服务器"复活"+ watcher 泄漏）；第二轮才补上 `awaitTermination` join + `stopLock` 下 `closed` 标志防落后发布。同一状态机的修复要一次扫全：可达性、并发交错、发布顺序。
