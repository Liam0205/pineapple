# Apple DSL data_parallel 编译期校验复盘

## Task
- 将 Go 引擎侧 `pine.go:validateDataParallel` 的两条 `data_parallel` 约束前移到 Apple/Python DSL 编译阶段：
  1. `data_parallel > 1` 仅允许用于 Transform 算子。
  2. `data_parallel > 1` 时要求 `common_output` 为空。
- 让错误在 `compile_flow()` 时暴露，而不是等到 Go 端加载配置时才失败。

## Expected vs Actual
- Expected outcome.
  - 以 Apple 现有 `validate_*` 模式直接补齐与 Go 端一致的编译期校验。
  - `data_parallel` 作为引擎保留字段被正确提取、校验并写入 JSON。
  - 测试与文档同步更新，确保 Python/Go 双端约束一致。
- Actual outcome.
  - 实现过程很直接：`OpCall` 增加 `data_parallel` 字段，`_add_op` 将其视为引擎保留键，`validator` 新增 `validate_data_parallel()`，`compiler` 在校验阶段调用并把字段写入 JSON。
  - 新增 5 个 Python 测试覆盖两条约束与合法路径；全部 65 个 Python 测试和全部 Go 测试通过。
  - README 与 design_doc 已同步说明双层校验，但 llmdoc 稳定文档此前未补齐，这次明确识别出需要更新 `llmdoc/architecture/apple-compiler.md`。

## What Went Wrong
- `testdata/e2e_apple_dsl.json` 出现了非预期变更。根因不是功能逻辑错误，而是 `OpCall` dataclass 新增字段后改变了 `repr()`，进而改变自动命名使用的 MD5 输入，导致基于哈希的默认算子名更新。
- `data_parallel` 的 llmdoc 稳定文档补齐滞后。此前 `data-parallel-framework` 反思已经指出 `llmdoc/architecture/apple-compiler.md` 需要补充 DSL 侧信息，但当时没有及时完成，导致这次还需要重新补文档缺口。

## Root Cause
- Apple 自动命名依赖 `OpCall` 的 `repr()`，因此 dataclass 字段集的任何变化都会影响哈希结果；这是当前命名机制的脆弱点，而不是这次实现特有的问题。
- 前一轮关于 `data_parallel` 的工作重点在运行时框架和 Go 侧约束，DSL 编译器文档更新项虽然已被识别，但没有在同一任务内完成闭环，说明“运行时能力新增后同步检查声明侧文档”还不是一个被强约束的收尾步骤。

## Missing Docs or Signals
- `llmdoc/architecture/apple-compiler.md` 需要明确补充三点：
  - `OpCall` 包含 `data_parallel` 字段。
  - 编译期校验规则新增 `validate_data_parallel()`，并说明两条限制。
  - 编译输出的 operator JSON 会携带 `data_parallel` 键。
- 这次哈希名变化说明 Apple 编译器文档还可以补一个信号：自动命名依赖 `OpCall` 表示形式，因此 IR 字段变化可能引发 golden/testdata 名称漂移。这更偏 memory 经验，但若未来频繁踩坑，可考虑升格为稳定说明。

## Promotion Candidates
- 适合提升到 `llmdoc/architecture/apple-compiler.md`：`data_parallel` 是 Apple 编译器识别的引擎保留字段，会从 kwargs 中抽离，不进入业务 `params`，并在 JSON 中透传给运行时。
- 适合提升到 `llmdoc/architecture/apple-compiler.md` 或 `llmdoc/reference/operator-contract.md`：`data_parallel > 1` 仅适用于 Transform，且要求空 `common_output`；DSL 编译期与 Go 加载期都应保持相同约束。
- 仅保留在 memory：`OpCall` 字段变更会通过 `repr()` 影响哈希命名，导致与语义无关的 testdata/golden 更新。这是当前实现细节带来的脆弱性提示，还不一定值得写入稳定架构文档。

## Follow-up
- 更新 `llmdoc/architecture/apple-compiler.md`，补充 `data_parallel` 的 IR 字段、保留键处理、校验规则与 JSON 输出位置。
- 后续若再给 `OpCall` 增减字段，提前检查自动命名是否会引发 snapshot/testdata 漂移，并在提交说明中明确区分“语义变更”与“哈希名漂移”。
