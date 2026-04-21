# [隔离 benchmark + 资源消费算子复盘]

## Task
- Item 16：设计纯算子级隔离 benchmark，去除引擎框架开销，量化 Lua vs Go 的真实计算差距。
- Item 15：新增 `recall_resource` 和 `transform_resource_lookup` 两个内置资源消费算子，补全资源系统的端到端生态。
- Item 17：确认 golangci-lint v2 配置升级已完成，无需操作。

## Expected vs Actual
- Expected：隔离后 Lua/Go 差距应显著大于端到端（端到端 1.2-2.1x）。资源消费算子应能直接复用现有 `resource.FromContext` 模式。
- Actual：隔离后差距为 1.9-4.1x，证实引擎框架开销稀释了 50-70% 的实际算子差距。资源算子顺利实现，`resource_name` 命名约定与 `ValidateResourceDeps` 自然衔接。Item 17 经验证已是 v2 格式，无需操作。

## What Went Wrong
- 先前 inventory 中列出了大量已完成的"待办"（12/14 个文档类条目和 golangci-lint v2），根因是从 reflection 文件提取 promotion candidates 时没有交叉验证稳定文档的当前状态。这是 reflection 作为时间快照的固有问题——后续 llmdoc:update 已将这些建议落地，但 reflection 中的文字不会自动标记为"已完成"。
- 隔离 benchmark 需要从 registry 获取 Lua 算子实例，但 `BuildOperator` 在 `internal/` 下未暴露。通过新增 `pine.BuildOperator` 公共包装器解决——这个 API 缺口应在 operator-contract 中记录。

## Root Cause
- inventory 幻觉：reflection 的 "Promotion Candidates" 是写入时的建议，不是实时状态。缺少"已落地"标记机制。
- BuildOperator 未暴露：项目初期只有引擎内部需要构建算子，外部消费者（benchmark、测试工具）的需求未被考虑。

## Missing Docs or Signals
- `llmdoc/reference/operator-contract.md` 应补充 `pine.BuildOperator` 的公共 API 说明，明确外部消费者可通过类型名构建注册算子。
- 资源消费算子的使用模式（`resource_name` 约定、资源值类型约束）应在 operator-contract 或相关文档中说明。

## Promotion Candidates
- 在 `llmdoc/reference/operator-contract.md` 的注册契约章节补充 `pine.BuildOperator` API。
- 在 `llmdoc/reference/operator-contract.md` 或独立文档中补充资源消费算子的实现模式（`resource_name` 参数约定、`resource.FromContext` 拉取方式、资源值类型约束）。

## Follow-up
- 检查 operator-contract.md 是否需要补充 BuildOperator 和资源消费模式说明。
