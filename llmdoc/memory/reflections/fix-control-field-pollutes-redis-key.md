# [修复 if_ 控制字段污染 Redis key 反思]

## Task
- 修复 `if_` 分支内算子在构建 Redis key 时混入编译器注入控制字段的问题。
- 采用引擎侧双层过滤：在 `SetMetadata` 前和 `BuildInput` 前剔除 `skip` 字段，并同步补齐 design_doc 与回归测试。

## Expected vs Actual
- Expected outcome.
  - `if_` 仅通过控制字段参与 DAG 依赖推导，算子元数据与运行时输入都不应看到 `_if_*` / `skip` 这类编译器注入字段。
  - `transform_redis_get`、`transform_redis_set` 以及其他依赖 `common_input` 的算子，在分支内执行时应保持“算子透明”，业务逻辑只看到声明的业务字段。
- Actual outcome.
  - 编译器把控制字段注入 `common_input` 以建立 DAG 依赖后，引擎又把这份完整列表原样传给 `SetMetadata` 和 `BuildInput`。
  - 结果是算子内部无论读取 `o.CommonInput`，还是通过 `in.CommonKeys()` 枚举输入字段，都能看到控制字段；Redis 算子在拼 key 时把对应布尔值一并拼入，导致 key 错误。
  - 最终修复证明需要同时过滤 metadata 路径和 runtime input 路径，单点修复不够。

## What Went Wrong
- 之前默认把“用于 DAG 推导的 metadata”与“暴露给算子的 metadata/input”视为同一份数据，忽略了控制字段只服务于编译/调度，不属于算子业务可见输入。
- bug 文档方向 A 依赖了一个未被实现的前提：设计文档曾写“算子可通过 `MetadataHolder` 访问自身的 `Skip` 配置”，但当时实际上并没有对应的 `SkipAware` 接口或稳定注入机制。
- 如果只在 `SetMetadata` 处过滤，只能修复直接读取 `o.CommonInput` 的算子；如果只在 `BuildInput` 处过滤，则仍会留下 metadata 污染。两层入口都需要分别验证。
- 一开始容易把问题局限在 Redis 算子本身，但实际影响面更广：任何在 `if_` 分支中把 `common_input` 当业务字段集合使用的算子都可能被污染。

## Root Cause
- 根因是控制流编译为建立 DAG 依赖，把控制字段放进了 `common_input`，但运行时没有区分“内部依赖字段”和“算子可见字段”。
- 更深层原因是编译器、引擎和设计文档对 `skip` 的职责边界没有被严格验证：设计层假设存在供算子读取的 skip 元信息，实际实现却没有对应契约，导致错误修复方向一度看起来合理。
- 由于控制字段被复用到多个消费点，最终形成了 metadata 污染和输入污染两个独立暴露面，因此需要引擎侧一次性收口。

## Missing Docs or Signals
- 已有且有帮助的信息：
  - `llmdoc/reference/operator-contract.md` 已明确 `skip` 是保留配置键，不应作为业务参数暴露给算子，这有助于判断引擎当前行为偏离了契约。
  - 任务自带 root cause 已经指出污染同时发生在 `SetMetadata` 和 `BuildInput` 两条路径，减少了只修一处的试错。
- 缺失或需要更新的信息：
  - 稳定文档尚应更明确写出：控制流编译产生的控制字段可参与 DAG 推导，但对算子应保持透明，不能出现在 metadata/common input 中。
  - design_doc 中关于“算子可读取 Skip 配置”的表述在修复前没有实现支撑，说明 design_doc 中的接口/能力声明需要在动手前回到代码里核实。
  - 当前文档还缺少一条更直接的排查信号：当 bug 涉及编译器注入字段污染业务逻辑时，应优先考虑在引擎边界统一过滤，而不是逐个算子兜底。

## Promotion Candidates
- 适合提升为稳定文档：
  - 在 `llmdoc/architecture/apple-compiler.md` 或相关控制流文档中补充：`if_` 注入的控制字段仅用于依赖推导，对算子应保持透明。
  - 在 `llmdoc/architecture/dag-engine.md` 或 `reference/operator-contract.md` 中明确：`SetMetadata` 与 `BuildInput` 暴露给算子的字段集合应过滤内部控制字段，不等同于完整 DAG metadata。
  - 在流程/设计文档约定中补一条：修 bug 前需要验证 design_doc 里提到的接口或能力是否真的存在于实现中。
- 更适合先保留在 memory：
  - “两层过滤缺一不可”这一点是本次具体实现经验，后续若再出现类似双入口污染问题，再考虑抽象成更通用的检查清单。
  - 本地 `golangci-lint` v2 与项目配置不兼容仍是独立事项，应单独治理，不应混入当前 bugfix 的稳定文档。

## Follow-up
- 保留本次 reflection，并在后续 `/llmdoc:update` 时优先把“控制字段对算子透明”和“修复前先验证 design_doc 假设”提升到稳定文档。
