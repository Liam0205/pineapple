# [评审驱动的 resource_lookup 修复复盘]

## Task
- 根据外部评审意见，复盘 `transform_resource_lookup` 及其相关 codegen / Apple 校验链路中同时暴露出的三类问题。
- 将本次问题沉淀为一个聚焦跨层语义验证缺口的记忆条目，供后续类似 JSON 契约、codegen、编译校验任务复用。

## Expected vs Actual
- Expected：`transform_resource_lookup` 在 Python DSL、JSON 编译产物、Go Init、运行时执行之间应保持一致语义；非字符串 lookup key 应显式处理；未传 `default_value` 时缺失查找应保持 skip 语义；业务参数若隐含 metadata 契约，编译期应拒绝不一致配置。
- Actual：出现了三处跨层缺陷并在多轮内部开发中漏过。第一，Go 端 `key, _ := ... (string)` 对非字符串 key 静默失败，JSON 数字被反序列化为 `float64` 后总是 miss，并错误写入 `default_value`。第二，codegen 总是序列化 `default_value`，即便 Python 调用方未传该参数，`None -> null -> hasDefault=true` 导致缺失查找写出 `null` 而不是 skip。第三，Apple 侧缺少业务参数到 metadata 的一致性校验，`lookup_key` / `output_field` 这类参数隐含的 item_input/item_output 约束没有被编译器捕获。

## What Went Wrong
- 测试只覆盖了单层正确性，没有沿着 Python DSL -> JSON -> Go Init -> Execute 追踪同一个边界值，导致跨层语义漂移无人发现。
- 在 JSON 边界没有系统枚举类型空间，只按"正常字符串 key"思维实现与测试，忽略了 JSON number 在 Go 中落为 `float64` 的常见情况。
- codegen 的验证停留在语法/可导入层面，没有检查"未传可选参数"在生成 Python、JSON 序列化和 Go 反序列化后的真实语义。
- 校验系统只建模了 metadata 之间的显式关系，没有识别"业务参数的值本身是 metadata 字段名"这种隐含契约，因此 validator 设计上就缺了一类规则。

## Root Cause
- 根因不是粗心，而是方法论偏单层：开发和验证默认把 Python、JSON、Go、运行时视为独立环节，而不是一条需要端到端语义守恒的数据通路。
- 团队没有把 JSON 边界视为高风险类型折叠点，缺少固定的类型枚举检查习惯，导致 `str`/`None`/number 等值跨语言转换后的语义差异未被提前审视。
- 对 codegen 的信任模型过于乐观，认为生成代码"能跑"就足够，没有追问 optional param 在全链路上的存在性语义是否正确。
- 编译器校验框架的抽象层次不足，只覆盖显式 metadata 声明关系，未抽象出"business param implies metadata contract" 这一校验维度。

## Missing Docs or Signals
- 现有稳定文档缺少一份明确的跨层测试/验证指南，告诉开发者何时必须从 Python DSL 一直追到 Go Execute，尤其是涉及 JSON 契约、可选参数和 codegen 时。
- 缺少 JSON 边界类型枚举的提醒信号：当参数跨 Python -> JSON -> Go 传递时，应主动列出 string / number / bool / null / missing 等可能形态及其落地语义。
- 缺少关于"隐含 metadata 契约"的 validator 设计说明，导致新增业务参数时不容易想到要补 param-metadata consistency 校验。

## Promotion Candidates
- 应优先把"跨层数据流追踪测试"提升为稳定 guide，覆盖至少四个检查维度：
  - 跨层数据流 tracing：对关键参数从 Python DSL -> compile JSON -> Go Init -> Execute 做语义追踪，而不是只测单层。
  - JSON 边界类型枚举：系统检查 missing / null / string / number / bool / list / object 在跨语言后是否仍符合预期语义。
  - codegen 语义校验：不仅验证生成代码语法正确，还要验证 optional params、默认值和条件序列化在全链路上是否正确。
  - implied contract 检测：当业务参数值引用 metadata 字段名时，validator 必须建模并校验这种隐含契约。
- 可后续考虑在 `llmdoc/reference/` 或 `llmdoc/architecture/apple-compiler.md` 中补充"param-metadata consistency" 这类校验模式的设计说明，但本次更适合先沉淀成 guide。

## Follow-up
- 后续整理并升级一份稳定指南，主题为跨层测试与 JSON 边界语义验证，作为涉及 codegen、可选参数、编译器校验任务的默认检查清单。
