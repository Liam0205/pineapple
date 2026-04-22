# Transform Remote Pineapple Operator Reflection

## Task
- 为 Pineapple 新增 Transform 类型算子 `transform_by_remote_pineapple`，通过下游 Pineapple 服务的 `/execute` 端点执行远程子流程。
- 支持本地 frame 字段按位置映射到下游请求/响应字段，并提供 `timeout` 与 `fail_on_error` 参数控制超时与降级行为。
- 同步补充实现测试、`design_doc/05_operator_types.md` 与 `README.md`。

## Expected vs Actual
- Expected outcome.
  - 算子实现应复用已有同包 helper，避免重复解析逻辑；测试应一次性对齐真实 API；交付时应按标准工作流同步 design_doc、README 与 llmdoc 更新。
- Actual outcome.
  - 最终功能与测试覆盖达成目标，字段映射 fallback、fatal/warning、HTTP 500、timeout 等场景都已覆盖。
  - 但实现过程中先后出现 3 个可避免的问题：同包 helper 重名导致编译失败、第一版计划漏掉文档收尾步骤、测试中误用了不存在的 `out.CommonWrites()` API。

## What Went Wrong
- 在 `operators/transform/remote_pineapple.go` 初始版本中自行定义了 `toStringSlice`，没有先检查 `operators/transform` 包内是否已有同名 helper，结果与 `redis_set.go` 中已有实现冲突，直接造成编译失败。
- 第一版 plan 只覆盖了代码与测试，没有把 `design_doc`、`README`、`llmdoc:update` 这些标准收尾动作纳入计划，直到用户指出后才补齐。
- 写测试时凭印象调用 `OperatorOutput` 的方法名，使用了不存在的 `out.CommonWrites()`，而正确方法是 `out.GetCommonWrites()`；说明测试前没有先核对目标类型的真实接口。

## Root Cause
- 对“同包复用优先”的检查不够前置，动手前没有先 grep 同目录已有 helper 与常用参数解析函数。
- 对项目标准工作流的记忆依赖经验，没有在 plan 阶段显式对照 `llmdoc/guides/standard-workflow.md` 清单，导致文档步骤遗漏。
- 对测试接口采用记忆式编写，而不是先查签名再落笔，本质上是“先写后验”的习惯问题。

## Missing Docs or Signals
- 缺少一个更显性的提示，提醒“新增同包文件前先搜索是否已有可复用 helper，尤其是 `toStringSlice`、`toInt64Param` 这类参数解析函数”。这一点更像开发时的局部经验，先记在 memory 即可。
- 标准工作流文档已经说明 design_doc、README、llmdoc 的同步步骤；问题不在文档缺失，而在 plan 阶段没有强制对照清单。更适合在 memory 中记录为执行提醒，而不是重写稳定文档。
- `OperatorOutput` 访问器命名需要通过 grep/Read 核对这一点，也更像通用开发习惯提醒；除非后续多次重复发生，否则暂不需要升级为稳定文档。
- 另外，`llmdoc/reference/operator-contract.md` 的算子清单可以后续补充 `transform_by_remote_pineapple`，方便新增算子时检索现有先例；`llmdoc/architecture/dag-engine.md` 与 `llmdoc/must/conventions.md` 本次无须调整。

## Promotion Candidates
- 暂留 memory：
  - 新增同包算子实现前，先搜索包内已有 helper，优先复用，避免同名冲突与参数解析分叉。
  - 写测试前先核对目标类型的方法签名，不凭记忆猜 API 名称。
- 可考虑后续提升到 stable docs：
  - 在 `llmdoc/reference/operator-contract.md` 增补 `transform_by_remote_pineapple` 作为 Transform 类型的远程调用示例，说明字段映射 fallback、超时与 `fail_on_error` 的行为边界。
  - 若后续再出现 workflow 漏项，可在 `llmdoc/guides/standard-workflow.md` 增加一个更显眼的“计划模板/收尾检查清单”，把 design_doc、README、llmdoc:update 明确列成提交前 checklist。

## Follow-up
- 将本次经验保留在 reflection 中；后续若继续演进该算子或再新增同类远程 Transform 算子，优先把 `transform_by_remote_pineapple` 补入 `llmdoc/reference/operator-contract.md` 的示例或算子清单，降低后续重复踩坑概率。
