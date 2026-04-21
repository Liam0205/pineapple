# [CI 质量基线补齐反思]

## Task
- 为 Pineapple CI 补齐工程质量基线，包括引入 Go/Python lint、补充测试覆盖率产物上传、加入关键路径 fuzz 测试，并修复由新规则暴露出的现有质量问题。

## Expected vs Actual
- Expected outcome.
  - CI 应具备最基础且可持续执行的质量门禁：Go 与 Python 都有统一 lint，测试任务产出覆盖率报告，关键输入边界有短时 fuzz 防回归。
  - 新增规则应尽量贴合现有工程状态，避免把 codegen 产物或非关键噪音一并纳入，导致门禁失真。
- Actual outcome.
  - 已完成 Go 侧 `golangci-lint` 和 Python 侧 `ruff` 接入，CI 新增 `go-lint`、`python-lint`、`fuzz` job，并为 Go/Python 测试增加覆盖率输出与 artifact 上传。
  - Go 侧修复了 `server`、`benchmark`、`integration` 中多处未检查 error return value，以及 `scheduler_test.go` 中的未使用变量；Python 侧批量修复了 import 排序、unused imports 和超长行问题。
  - `apple_generated/` 已从 Python lint 中排除，避免对 codegen 产物做无效修复；fuzz 选择了 `config.Load` 与 `dag.Build` 两个关键输入边界，本地短时运行未发现 panic。

## What Went Wrong
- 本轮没有明显返工，但新增 lint 后暴露出一个事实：Go 侧长期未启用 `errcheck`，HTTP handler、benchmark、integration 这类“非核心业务逻辑”区域积累了较多未检查 error，属于典型质量盲区。
- Python 侧 `ruff` 一次性自动修复了大量 import 排序与未使用导入，说明此前仓库缺少最基础的格式化/静态检查约束，代码风格主要靠人工维持，导致小问题长期滞留。
- 如果未显式排除 `apple_generated/`，lint 会把 codegen 产物当作手写代码对待，修复结果也会在下次生成时被覆盖；这类目录若未在接入阶段先识别，容易引入无意义噪音。
- fuzz 测试虽然已覆盖两个高价值入口，但当前仍以“不 panic”为最低保障，更多语义级不变量还没有形成统一策略。

## Root Cause
- 项目之前已有测试与功能验证，但缺少“持续执行的工程质量基线”这一层，因此一些低级但真实存在的问题没有被自动化工具稳定拦截。
- Go 代码中最容易漏掉 `errcheck` 的区域，恰好是 handler、benchmark、integration/test helper 这类经常被开发者视为“外围代码”的位置；没有专门规则时，很容易默认忽略返回值。
- Python 侧此前没有统一 lint/format 工具，导致 import 排序和 unused import 只能依赖人工习惯，难以长期一致。
- 对 codegen 目录与手写目录的边界虽然在架构上明确，但在质量工具接入时若没有显式规则，工具会默认全量扫描，进而把“可生成覆盖”的文件也纳入人工维护范围。

## Missing Docs or Signals
- 已有且有帮助的信息：
  - `llmdoc/must/conventions.md` 已强调 codegen 新鲜度与生成产物边界，这有助于判断 `apple_generated/` 不应作为手写 lint 修复目标。
  - 标准工作流文档强调非平凡任务需要验证与文档同步，因此本轮把 README 测试命令一并补上是自然延伸。
- 仍缺失或值得强化的信息：
  - 当前稳定文档没有一份“工程质量基线”说明，未明确 Go/Python 默认应跑哪些 lint、哪些目录应排除、覆盖率产物放在哪里、fuzz 应优先覆盖哪些入口。
  - 缺少针对 Go 常见质量盲区的提示，例如 HTTP handler、benchmark、integration/test helper 中也必须检查 error return value，不能因为是测试或示例代码就放宽。
  - 缺少针对 Python codegen 目录的工具接入约定，未明确 `apple_generated/` 属于生成产物，lint/format 通常应排除，除非修改的是生成源或 codegen 本身。
  - fuzz 入口选择原则目前只体现在本次实现中，尚未在文档中明确“优先覆盖解析入口和图构建入口这类高扇出边界”。

## Promotion Candidates
- 适合提升为稳定文档：
  - 在 `llmdoc/guides/` 增加工程质量基线指南，列出 Go/Python 默认 lint 规则、覆盖率命令、artifact 约定、fuzz job 最小配置，以及接入新仓库检查项。
  - 在 `llmdoc/must/conventions.md` 或相关 reference 中补一条质量工具边界说明：生成目录如 `apple_generated/` 默认不做手工 lint 修复，需通过生成源和 codegen 流程解决问题。
  - 在 Go 相关贡献约定中补充 `errcheck` 提示，点名 handler、benchmark、integration/test helper 是高频漏检区域。
  - 在测试/CI 指南中补充 fuzz 入口选择原则：优先覆盖 JSON/配置解析、DAG/编译构建等高扇出输入边界，先保证“不 panic”，再逐步增加语义断言。
- 更适合先保留在 memory：
  - Python 侧本次大量 import 排序与 unused imports 被一次性清理，说明历史上完全缺乏格式化门禁；这是一次性历史背景，可作为记忆保留，不必单独写成架构文档。
  - 当前覆盖率水平（Go 66.3%、Python 97%）可作为阶段性快照，但不适合作为稳定文档中的长期阈值，除非后续正式设门槛。

## Follow-up
- 保留本次 reflection，并建议后续把“CI 质量基线”整理为稳定 guide：明确 lint/coverage/fuzz 的默认组合、codegen 目录排除规则，以及 Go `errcheck` 高频盲区检查清单。
