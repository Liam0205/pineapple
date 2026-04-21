# [修复 Flow 输出投影语义反思]

## Task
- 修复 Flow 声明中 `common_output` 与 `item_output` 的语义错误：未声明输出时，结果不应默认返回该维度的全部字段。
- 同步更新运行时测试、集成测试、设计文档与 README 中的 pre-1.0 兼容性说明。

## Expected vs Actual
- Expected outcome.
  - Flow output contract 应被严格执行：`common_output`/`item_output` 为空时返回空投影，只有显式声明的字段才出现在最终结果中。
  - Python DSL、JSON 契约、Go 引擎和文档应保持同一语义。
- Actual outcome.
  - 实际行为是未传 `common_output` 时仍返回了所有 common 字段，`item_output` 也沿用了同样的“空即全量”语义。
  - 问题不仅存在于实现，也被 design doc 明确写成了“向后兼容”行为，导致测试、文档和运行时长期一致地偏离了预期契约。
  - 最终修复只需删除 `internal/dataframe/dataframe.go` 中 `projectMap` 的空列表回退分支，并补齐依赖旧语义的测试与文档。

## What Went Wrong
- 一开始容易把问题归因到 Python 编译器把 `None` 编码成 `[]`，但这次实际证明该编码对新语义是正确的；真正错误的是运行时把空列表解释成“返回全部字段”。
- 语义缺陷跨越三层：Python 编译输出、Go DataFrame 投影、设计文档说明，若只修实现而不审文档和测试，会留下新的不一致。
- 多个 engine/integration 测试里的 flow contract 没有显式 output 列表，默认依赖旧行为；这些测试在语义收紧后必须逐个改成显式声明，否则会把历史偶然行为继续固化。
- 这次暴露出一个边界问题：`apple/validator.py` 内部其实保留了 `None` 与 `[]` 的语义差异，但 JSON 边界把“未声明 contract”与“空输出 contract”压成了同一个编码，最终只能依赖运行时约定解释。

## Root Cause
- 项目早期为了所谓“backward compatible”选择了空输出列表 = 返回全量字段的语义，并把这个选择同时写进了 Go 实现和设计文档。
- Python 侧虽然仍有更细的内部语义，但跨语言契约最终只传递 JSON；当 `None` 被编码成 `[]` 后，运行时解释规则就决定了最终行为。
- 因为 Pineapple 尚处于 1.0 前阶段，历史上不必要地保留兼容性，反而让 output contract 的声明式语义被削弱。

## Missing Docs or Signals
- 已有且有帮助的信息：
  - `llmdoc/architecture/apple-compiler.md` 已强调 JSON 是 Python 与 Go 的持久边界，这帮助判断问题需要沿着跨语言契约链路排查，而不是只盯某一侧实现。
  - `llmdoc/architecture/dag-engine.md` 已指出 `ToResult`/结果投影是 DataFrame 负责的运行时行为，因此很快能把落点收敛到 `internal/dataframe/`。
- 缺失或需要更新的信息：
  - `llmdoc/architecture/apple-compiler.md` 目前未写明步骤 7 构建 `flow_contract` 时，`None` 会编码为 `[]`，以及这要求运行时把空列表解释为“空投影”而不是“全量投影”。
  - `llmdoc/architecture/dag-engine.md` 当前把 `ToResult` 的空输出列表语义写成“返回全部内容”，已与修复后的行为相反，需要更新为“空列表返回空结果”。
  - `llmdoc/must/conventions.md` 尚未明确 pre-1.0 阶段可以为纠正语义错误而放弃向后兼容；这类决策标准应更显式，避免未来再被“兼容性”惯性绑住。

## Promotion Candidates
- 适合提升为稳定文档：
  - 在 `llmdoc/architecture/apple-compiler.md` 的 flow contract 章节补充 `None -> []` 的 JSON 编码说明，并明确其语义依赖运行时按“空即空投影”解释。
  - 在 `llmdoc/architecture/dag-engine.md` 更新 `projectMap` / `ToResult` 语义，明确空输出列表不会回退到全量字段。
  - 在 `llmdoc/must/conventions.md` 增补 pre-1.0 兼容性约定：语义错误修复优先于维持历史行为，除非任务明确要求保兼容。
- 更适合先保留在 memory：
  - 这次多个测试 fixture 依赖旧默认行为，说明“未显式声明 output 的测试很脆弱”；这是后续改测试时的检查提醒，可先作为 memory 保留，待类似问题再次出现再考虑上升为测试指南。

## Follow-up
- 保留本次 reflection，并在后续 llmdoc 更新时优先同步三处稳定文档：`apple-compiler` 的 flow_contract 编码语义、`dag-engine` 的结果投影语义，以及 `conventions` 中的 pre-1.0 兼容性原则。
