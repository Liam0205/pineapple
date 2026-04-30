# [BuildInput sparse missing vs explicit-nil fix]

## Task
- 复盘 `RowFrame.BuildInput` 与 `ColumnFrame.BuildInput` 的稀疏字段语义修复，确保构造 `OperatorInput` 时能区分“字段缺失”和“字段显式存在但值为 nil”。
- 本次修复覆盖 common 与 item 两条路径，并保持 row/column 两种 Frame 实现语义一致。

## Expected vs Actual
- Expected outcome.
  - `BuildInput` 应与 `ToResult` 保持一致：缺失字段且无默认值时不写入 key；缺失字段且有默认值时写入默认值；显式 nil 且有默认值时写入默认值；显式 nil 且无默认值时保留 key 且值为 nil。
  - 既有 row/column parity 测试应继续通过，并新增覆盖稀疏输入场景的测试。
- Actual outcome.
  - `ColumnFrame.BuildInput` 之前虽然已经有 `present` bitmap，却没有在 `BuildInput` 中读取，导致声明字段总是被无条件写入 `OperatorInput`。
  - `RowFrame.BuildInput` 之前通过 `v := item[field]` 读取 map，丢失了 key presence 信息；同样把字段缺失与显式 nil 混为一谈。
  - 修复后，row/column 两种实现以及 common/item 两条路径都按同一规则处理 presence + default 组合；既有测试无需修改，新补的三组测试覆盖了此前未测到的 sparse case。

## What Went Wrong
- 先前针对列存稀疏语义的修补只完成了一半：`present` bitmap 接到了 `ToResult`，却没有接到 `BuildInput`，形成“结果投影正确、算子输入错误”的半完成状态。
- 深度风险审计把问题定位为 `ColumnFrame.BuildInput` 未读取 `present` bitmap`，这个方向是对的，但实际影响范围比最初判断更大：`RowFrame` 也有同类问题，而且 common/item 两条路径都需要一起修。
- `RowFrame` 的 bug 之所以容易漏掉，是因为直接写 `v := item[field]` 在 Go 里看起来很常见，但这里真正需要的是 `v, ok := item[field]` 才能保留语义边界。
- 设计文档 `design_doc/03_data_abstraction.md` 中仍写着“字段存在但值为空 与 字段不存在 均表现为 nil”，这个表述已经和 `present` bitmap 设计相矛盾，成为主动误导调查与修复判断的旧文档信号。

## Root Cause
- 根因不是单点实现疏漏，而是“稀疏字段语义”没有被当成需要端到端保持的一条明确不变量。于是一次修复只修到了结果投影路径，没有系统性检查输入构造路径是否同样需要 presence 信息。
- 对 row store 的实现存在默认信任：因为 map lookup 语法很常见，容易把“零值读取”误当成“语义正确读取”，没有主动检查是否保留了 key 是否存在这一层信息。
- 文档层面，稳定/设计文档没有持续跟上实现演进，导致“缺失 vs 显式 nil”这一语义边界同时缺少架构文档强调和设计文档纠偏，弱化了审查时的提醒作用。

## Missing Docs or Signals
- `llmdoc/architecture/dag-engine.md` 已说明 `ColumnFrame` 用 presence bitmap 保证 `ToResult` 的稀疏输出语义，但还应明确该 bitmap 也必须参与 `BuildInput`，否则算子看见的输入语义会与最终结果语义分叉。
- `llmdoc/memory/reflections/column-store-dataframe.md` 目前记录了列存实现、parity 测试与 benchmark，但没有补充“稀疏输入修复已完成、BuildInput 现在也遵守 presence 语义”的后续结果。
- `design_doc/03_data_abstraction.md` 的旧表述属于需要尽快纠正的误导性信号；这是稳定设计叙述落后于实现的案例，不应继续沿用。
- 更适合留在 memory 的流程信号：凡是引入 presence bitmap、validity bitmap 或其它“存在性载体”的实现，后续检查必须同时覆盖读入、写回、默认值应用、结果投影四条路径，避免再次出现“只修一半”的情况。

## Promotion Candidates
- 适合后续提升到稳定文档：
  - 在 `llmdoc/architecture/dag-engine.md` 的 DataFrame 输入投影部分补充不变量：`BuildInput` 与 `ToResult` 都必须区分 missing 和 explicit nil，`present` bitmap 不只是结果投影辅助结构，也是输入构造语义的一部分。
  - 在相关 design doc 中修正文案，不再宣称“字段缺失”和“字段存在但为 nil”表现一致，而是明确默认值应用规则与 presence 语义。
- 更适合暂留 memory：
  - 审计中发现 “问题看似只在 ColumnFrame，实际 RowFrame 也可能以不同形式丢语义”的经验，可作为以后检查 row/column parity 和 common/item parity 的调查提醒。
  - “Go map 读取默认值语法容易掩盖 presence bug” 属于实现层审查经验，先作为 memory 保留更合适。

## Follow-up
- 更新 `llmdoc/architecture/dag-engine.md`，把 `BuildInput` 对 presence bitmap 的依赖写成明确不变量。
- 更新 `llmdoc/memory/reflections/column-store-dataframe.md` 或后续稳定文档，注明稀疏输入语义修复已补齐到 `BuildInput`。
- 修正 `design_doc/03_data_abstraction.md` 中关于“字段缺失 vs nil”等价的过期表述，避免继续误导后续实现与审查。
