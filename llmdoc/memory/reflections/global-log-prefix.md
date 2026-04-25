# 全局日志前缀功能复盘

## Task
- 为 Pineapple 增加全局日志前缀能力：Apple DSL 的 `Flow` 声明支持 `log_prefix`，编译为 JSON 根级字段，由 Go 引擎在 `NewEngine` 中通过 `log.SetPrefix()` 与 `log.SetFlags(log.Ldate|log.Ltime|log.Lshortfile)` 应用；同时提供 `WithLogPrefix` Option，且优先级高于 JSON 配置。
- 补齐 Python 编译器测试与 Go 引擎测试，覆盖 root-level 配置透传、Option 覆盖 JSON、以及日志 flags/prefix 的生效行为。

## Expected vs Actual
- Expected outcome.
  - 在方案设计阶段一次性覆盖“全局日志格式”的完整需求维度：前缀、时间戳、源码位置，以及 JSON 配置与 Go Option 的优先级关系。
  - 沿用既有 root-level 配置扩展路径，完成 Python → JSON → Go 的一致透传，并在测试中正确处理全局日志状态的 cleanup。
- Actual outcome.
  - 首版实现只调用了 `log.SetPrefix()`，遗漏了 `log.SetFlags()`，导致日志没有 `file:line` 信息；这是用户指出后才追加修复的。
  - root-level `log_prefix` 的跨层透传本身实现顺利，说明 `storage_mode` 之后这类扩展路径已经比较成熟。
  - Go 测试后续采用了保存并 `defer` 恢复 `log.Prefix()` / `log.Flags()` 的模式，避免了全局状态污染其他测试。

## What Went Wrong
- 计划阶段把需求过度收缩为“给日志加 prefix”，只关注了一个控制维度，没有同时检查 Pineapple 期望的标准日志格式是否还包含时间戳和源码位置。
- 对标准库 logger 的全局状态影响考虑不够靠前。`log.SetPrefix()` / `log.SetFlags()` 不是 Engine 实例内局部属性，而是进程级设置；这一点虽然在实现时是有意识选择，但应在设计说明和测试策略里更早显式化。
- 测试策略初始关注点偏向“是否设置了 prefix”，而不是“最终日志格式是否完整正确”。如果一开始就把 flags 视为验收面的一部分，遗漏更容易在首轮实现中暴露。

## Root Cause
- 根因不是不会调用 `log.SetFlags()`，而是在 plan 阶段没有先枚举“日志标准格式”的完整需求维度，只按表面功能点拆解任务，导致实现和验证都围绕单一字段展开。
- 团队已经形成了 root-level 配置扩展的实现路径，但还没有把“功能字段透传成功”与“运行时语义完整落地”区分成两个检查层次。前者做到了，后者首版不完整。
- 对进程级全局状态的设计约束虽有认知，但没有在一开始就转化为明确的测试 checklist：覆盖优先级、覆盖 flags、覆盖 cleanup、记录多 Engine 场景下后写覆盖前写。

## Missing Docs or Signals
- 缺少一个更明确的 planning signal：凡是涉及日志、观测、trace 这类横切能力，方案阶段应先列完整的行为面，而不是只盯住新增字段名本身。
- 稳定文档中虽然已经能描述 root-level 配置扩展路径，但还可以更明确强调：这类功能落地时需要同时检查 Python DSL 参数、JSON 根级 schema、Go 加载逻辑、运行时副作用、测试 cleanup 五个面。
- 对 `log_prefix` 而言，还应记录一个明确信号：`WithLogPrefix` 与 JSON `log_prefix` 最终作用于标准库全局 logger，因此多 Engine 实例场景下后初始化者会覆盖前者。这是设计约束，不是 bug。

## Promotion Candidates
- 适合提升到 `llmdoc/guides/` 或相关 stable docs：涉及日志/观测格式的功能时，plan 阶段应显式枚举完整需求维度，例如 prefix、timestamp、source location、优先级与作用域，而不是只围绕单一新增参数设计。
- 适合提升到 `llmdoc/architecture/apple-compiler.md` 与 `llmdoc/architecture/dag-engine.md`：`log_prefix` 继续验证了 root-level 配置扩展的成熟模式，即 `Flow` 顶层参数 → `apple/compiler.py` 步骤 9 根级 JSON → `internal/config.RootConfig` → `pine.NewEngine()` 运行时应用。
- 适合提升到 `llmdoc/architecture/dag-engine.md`：`log.SetPrefix()` / `log.SetFlags()` 操作的是进程级全局 logger，不是 Engine 私有状态；多 Engine 并存时最后一次配置生效，这一约束需要被明确记录。
- 适合提升到测试指南或 reference：凡测试进程级全局状态（如标准库 `log`）时，标准模式是保存旧值并 `defer` 恢复，避免测试间相互污染。
- 仅保留在 memory：这次首版遗漏 `Lshortfile` 的直接教训，核心是“plan 阶段先列完整需求维度”，属于流程提醒，后续若反复出现再考虑强化为 must 级规则。

## Follow-up
- 后续凡新增 root-level 配置字段，先按固定 checklist 过一遍：DSL 参数、编译输出、Go 根配置、运行时语义、优先级/覆盖规则、全局状态约束、测试 cleanup。
- 若未来继续扩展日志能力（如自定义 flags、logger 注入、结构化日志），应先明确其是否仍基于标准库全局 logger；若是，就必须在设计文档中提前说明多实例覆盖语义与适用边界。
