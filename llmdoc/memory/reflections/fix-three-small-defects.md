# [修复三项小缺陷反思]

## Task
- 修复 3 项小缺陷：更正 `demo.py` 中错误的算子示例调用；在 `apple/flow.py::end_if_()` 为空控制分支增加编译期校验；改写 `apple/compiler.py::_resolve_source` 的误导性 docstring，并同步补充测试与相关文档说明。

## Expected vs Actual
- Expected outcome.
  - 示例代码应使用真实存在的 DSL 算子名，控制流声明应在编译期拦截空分支，编译器注释应准确反映 source refs 的实际语义。
- Actual outcome.
  - 三项修复都顺利完成：`demo.py` 改为 `flow.transform_by_lua(...)`；`end_if_()` 现在会检查每个 branch 的 `ctrl_field` 是否被至少一个业务算子的 `skip` 引用，空 `if`/`else` 分支会抛出 `ValueError`；`_resolve_source` 的 docstring 改为说明编译器对 source refs 直接透传。
  - 同时新增了 `apple/tests/test_validator.py` 中的空分支用例，并把新增编译期校验更新到 `design_doc/06_json_config.md` 与 `README.md`。

## What Went Wrong
- 严格说本轮没有明显返工或实现偏航，执行较顺。
- 但 `demo.py` 的错误再次证明：`_FlowBase.__getattr__` 的动态分发会把拼错或不存在的 API 名当作合法算子记录下来，DSL 层静默通过，只会在 Go 运行时以 `RegistryError` 暴露问题。
- `_resolve_source` 的 docstring 长期保留了“若歧义则报错”的表述，但实现其实只是 pass-through，说明文档语义与当前实现之间存在轻微漂移。

## Root Cause
- 动态分发是 Apple DSL 的有意设计，带来灵活性的同时，也天然削弱了“错误 API 名称”的早期反馈能力；示例代码一旦写错，靠 DSL 本身不容易及时发现。
- source/merge 语义在稳定文档中的说明还不够具体，导致注释容易沿用过时心智模型，没有及时和实际实现对齐。
- 控制流原先只校验“块是否闭合”，没有进一步校验“分支里是否真的挂了业务算子”，于是空分支被当成语法上合法但语义上无效的声明静默接受。

## Missing Docs or Signals
- 已有且有帮助的信息：
  - 上一轮反思 `llmdoc/memory/reflections/bugfix-six-items.md` 已明确指出 `_FlowBase.__getattr__` 会掩盖 API 误用，这次 `demo.py` 问题正是该信号的直接体现。
  - `llmdoc/architecture/apple-compiler.md` 已较清楚地描述控制流降级和 `skip` 字段语义，因此本次为空分支补校验时，设计方向是清晰的。
- 仍缺失或值得强化的信息：
  - 稳定文档对“动态分发会吞掉错误算子名，示例和文档需要特别谨慎”虽已有提及，但还可以在更贴近示例编写的位置再强调一次，帮助减少 demo/API 文档误写。
  - `llmdoc/architecture/apple-compiler.md` 对 source refs / merge 语义还不够展开，未明确说明 DSL 层的 source 引用在进入编译器前就应是最终算子名，编译器这里不负责消歧或重写。
  - 空分支校验是新增编译器行为，虽然本次已补到 `design_doc` 和 `README`，但稳定架构文档后续也应同步体现，避免规则只存在于实现和外围说明里。

## Promotion Candidates
- 适合提升为稳定文档：
  - 在 `llmdoc/architecture/apple-compiler.md` 中补一条 source refs / merge 语义说明，明确 `_resolve_source` 目前是透传逻辑，source refs 在 DSL 层已是最终算子名，而不是等待编译期再做消歧。
  - 在 `llmdoc/architecture/apple-compiler.md` 的控制流校验部分补充“空分支检测”规则，说明 `if_/elseif_/else_` 的每个 branch 必须至少挂接一个实际受对应 `ctrl_field` 控制的业务算子。
  - 在与 DSL 使用方式相关的稳定文档中继续强化 `_FlowBase.__getattr__` 的风险提示，明确“未知方法名会被当作算子名记录”，因此示例、README 和教程中的 API 名必须以真实算子/helper 为准。
- 更适合先保留在 memory：
  - 这次改动体量小、验证顺利，属于一次平稳的小缺陷修复，不需要把流程层面的经验额外沉淀为新 guide。

## Follow-up
- 保留本次 reflection，并在后续合适时机把两项内容补入稳定文档：一是 `apple-compiler` 中的 source refs / merge 语义说明，二是控制流章节中的空分支编译期校验规则；动态分发掩盖 API 误用则继续作为示例审查时的重点检查项。
