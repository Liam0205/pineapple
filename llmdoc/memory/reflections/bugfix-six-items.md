# [六项缺陷修复与测试补齐反思]

## Task
- 基于 llmdoc 调查结果，完成 Pineapple 的 6 项缺陷修复与测试补齐，并复盘 llmdoc 中哪些信息帮助了定位、哪些缺失增加了试错，以及哪些发现值得沉淀为稳定文档。

## Expected vs Actual
- Expected outcome.
  - llmdoc 应足够支持快速定位编译器、codegen、E2E 与资源集成相关问题，减少 API 误判与测试设计试错。
- Actual outcome.
  - 现有 llmdoc 对 Apple 编译流水线、控制流降级、算子契约、codegen 生成链路提供了有效导航，帮助较快锁定 6 个问题的落点。
  - 但在 SubFlow 具体 API、资源系统“谁实际消费 resource”、以及 Schema type 字符串与 codegen 映射边界上缺少明确说明，导致实现前仍发生一次 API 误用与若干额外核对。

## What Went Wrong
- 对 SubFlow 使用方式做了错误假设，先按常见 builder 习惯寻找 `add_sub_flow()`，实际项目是 `Flow(sub_flows=[sf])` 构造传入。
- 资源系统文档说明了声明与注入，但没有明确提示“当前无内置算子通过 `resource.FromContext` 消费资源”，导致资源 E2E 设计初期默认以为可直接复用现有算子。
- `operator-contract` 说明了 Schema 是 codegen 权威来源，但没有列出 codegen 当前实际支持的参数类型集合，`"int"` 与 `"int64"` 的分歧只能靠读实现发现。
- llmdoc 现有文档偏架构与契约，缺少“如何为调查出的缺口补负面测试/E2E 测试”的工作流指引；这次虽顺利完成，但方法仍主要依赖临场探索。

## Root Cause
- 稳定文档覆盖了高层架构和核心不变量，但对“易误判的具体 API 入口”和“实现与文档之间的灰区”记录不足。
- 文档更多描述系统应然结构，较少显式标记当前实现限制、未覆盖区和测试策略，因此调查报告能指出问题方向，但实施阶段仍需回到代码确认细节。

## Missing Docs or Signals
- 已有且有帮助的信息：
  - `llmdoc/architecture/apple-compiler.md` 已准确说明编译步骤、控制流降级、自动命名冲突加 `_N`、资源声明校验时机，对修复未关闭控制块、补 hash 碰撞测试、理解资源声明边界都有直接帮助。
  - `llmdoc/reference/operator-contract.md` 已说明 Schema -> codegen -> `apple_generated/`/`doc/operators/` 的生成链路，帮助快速判断 `"int"` 映射问题位于 codegen 而非编译器。
- 缺失且影响定位/实施的信息：
  - `apple-compiler` 未把 SubFlow 的实际接入方式写得足够显眼，尤其没有明确说明“组合入口是 `Flow(sub_flows=[...])`，不是后置追加 API”。这直接导致一次无效测试写法。
  - 资源相关文档缺少“当前状态”信号：资源声明会进入 context，但仓内暂无内置算子消费该资源；若要做端到端验证，需要测试专用算子或新增真实消费者。
  - `operator-contract` 缺少 codegen 支持的 schema type 对照表或至少一条约束，未提示 `Type` 虽为字符串但生成侧并非对任意字符串都等价支持。
  - 缺少一份调查转实施的 guide，说明何时补 validator 单测、何时补 compiler 单测、何时补 integration E2E、何时需要生成产物与 CI 校验联动。

## Promotion Candidates
- 适合提升为稳定文档：
  - 在 `llmdoc/architecture/apple-compiler.md` 增补 SubFlow 使用说明与常见误区，明确 `Flow(sub_flows=[sf])` 是组合方式，并提示 `__getattr__` 动态分发可能掩盖 API 误用。
  - 在资源相关稳定文档中补一条当前实现说明：资源系统已有声明/注入链路，但暂无内置资源消费者；资源集成验证通常依赖测试专用算子，除非后续引入正式消费者。
  - 在 `llmdoc/reference/operator-contract.md` 增补“Schema type 与 codegen 支持矩阵”或最小约束列表，明确哪些 type 字符串会映射到 Python helper/default，避免 `"int"`/`"int64"` 这类漂移。
  - 新增 `llmdoc/guides/` 文档，沉淀“从调查报告到修复落地”的测试策略：编译器校验类问题优先 validator/compiler 单测；运行时错误映射补负面 E2E；资源/上下文链路补专门集成测试；涉及 Schema 变更时同步 regenerate 并让 CI generated-diff/benchmark 一起覆盖。
- 更适合先保留在 memory：
  - 这次资源 E2E 只能通过测试算子验证，属于当前实现缺口；若后续引入正式资源消费者，再决定是否弱化该说明。
  - 本轮 6 项改动一次通过全量测试，说明前置调查充分；这是流程经验，可作为 memory 保留，不必写入架构文档。

## Follow-up
- 写入本次 reflection，并建议后续优先补两类稳定文档：一是 `apple-compiler` 的 SubFlow/API 误区说明，二是面向调查结果落地的 `guides/` 测试补齐指南；待资源消费者与 schema type 支持边界进一步稳定后，再补充对应 reference 文档。
