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
