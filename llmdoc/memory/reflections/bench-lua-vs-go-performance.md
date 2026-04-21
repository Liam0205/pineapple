# [Lua vs Go 原生算子 benchmark 复盘]

## Task
- 设计并实现 5 档复杂度递增的 Lua vs Go 原生算子性能对比 benchmark，量化 gopher-lua VM 开销。

## Expected vs Actual
- Expected: Lua 比 Go 慢 10-50x（基于通用 Lua VM 性能经验）。
- Actual: 端到端引擎测试下 Lua 仅慢 1.2-2.1x。引擎框架开销（DataFrame 构建、DAG 调度、recall 写入）占总耗时大头，稀释了 Lua VM 的纯计算差距。

## What Went Wrong
- 初始预估偏差大：凭经验预估 10-50x 差距，但忽略了端到端测试中引擎框架开销的稀释效应。如果做纯算子 Execute 级别的隔离 benchmark，差距会更大。
- 算子注册的 Description 和 ParamSpec.Description 是必填字段，首次注册时遗漏，导致 panic。这在 `llmdoc/reference/operator-contract.md` 中应该有记录但未检查。

## Root Cause
- 端到端 benchmark 包含 recall_static + DataFrame 构建的固定开销，使得算子计算部分的占比被压缩。这不是错误——反映了真实场景下的比例。
- 对 OperatorSchema 注册契约不够熟悉，没有在编码前查阅 `reference/operator-contract.md`。

## Missing Docs or Signals
- `llmdoc/reference/operator-contract.md` 应明确列出 OperatorSchema 和 ParamSpec 的必填字段清单（包括 Description），避免注册时遗漏。
- 缺少 Lua 算子的性能特征描述。现已通过 `design_doc/13_lua_vs_go_benchmark.md` 和 `operators/lua/lua.go` 顶部注释补充。

## Promotion Candidates
- `reference/operator-contract.md` 可以补充 Schema 必填字段 checklist。
- 未来如果需要纯算子级别的隔离 benchmark（去除引擎框架开销），可以设计直接调用 Execute 的测试方式，作为 benchmark 指南的一部分。

## Follow-up
- 检查 `reference/operator-contract.md` 是否已列出 Description 为必填。
