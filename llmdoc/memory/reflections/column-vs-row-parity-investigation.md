# 列存 vs 行存性能持平调查复盘

## 任务

回答一个长期困惑：为什么 pineapple 上列存模式（ColumnFrame）的性能没有显著优于行存模式（RowFrame）？这不符合列存的一般认知。以 pine-go 为例深入调查，找到根因并给出解法。

**状态更新（2026-07-07）：本篇记录的解法已在三引擎正式实现并提交——pine-go `fbf2ef7` / pine-java `bf7ce0b` / pine-cpp `95a3000`（分支 feat/column-store-batch-access），详见文末"实现记录"。原文中"未提交原型"的措辞保留为调查时点的记录。**

## 发现：三层根因

### 根因 1（第一性）：逐元素 `Item()` 接口税抹平列存优势

所有算子都通过 `OperatorInput.Item(i, field)`（`pine-go/internal/types/operator_io.go`）**逐元素**访问数据，每次调用 = `Frame.Item()` 的 RLock + columns map lookup + 边界检查。scan 分解实测（1000 items × 10 fields，Apple M5 Pro）：

| 访问路径 | 耗时 | 说明 |
|---|---|---|
| 理想列扫描（列 hoist 出循环，单锁单查） | 262 ns | 列存应有的形态 |
| typed `[]float64` 扫描 | 257 ns | typed columns 的上限 |
| `ColumnFrame.Item()` 逐元素 | 7,210 ns | **理想形态的 27 倍** |
| `RowFrame.Item()` 逐元素 | 9,347 ns | — |
| ColumnFrame 逐元素去锁版 | 4,674 ns | 锁占 ~35%，map 查找是大头 |

列存的连续内存优势在真实算子访问路径上只剩 ~23%（7.2 vs 9.3μs）——**每元素一次锁 + 一次 map 查列的接口税，把两种存储压到了同一量级**。列的"壳"对了，但访问模式还是行主序逐元素；这比 `[]any` interface 装箱更根本——装箱之前，接口形状就已经把优势吃掉了。

### 根因 2（实现级）：ColumnFrame 三处热路径是"行主序代码操作列存储"

- `BuildInput` 的 strict/nullable item 校验按 item-major 循环，每 (item × field) 一次 map lookup
- `ApplyOutput` ItemWrites 每次写 2 次 map lookup，而算子实际是 field-major 连续写同一字段
- `ApplyOutput` Additions 每个新增 item 遍历一次全列 map，O(added × cols) map 迭代

### 根因 3（负载形状）：典型管道由列存天然劣势操作构成

- recall additions：行存 zero-copy 直接接管 added map 引用；列存必须把每个 map 拆散进各列
- removals：列存 ~10x 劣势（全列重建）；reorder：列存 ~8x 劣势
- 生产 calibrated fixture N≈10、38-op DAG，调度与 Lua 边界主导成本，列存可赢的 transform 扫描占比极小
- 列存明确赢面仅 `New` 构造（allocs 4004→33）与 `ToResult`

这解释了 calibrated fixture 配 `"storage_mode": "row"` 是正确选择。

## 原型修复与 A/B 数据

对根因 2 的三处热路径做列主序化原型（保持首错优先级字节对等——校验仍 item-major 迭代顺序，仅 hoist 列解析出循环；dataframe/operators/engine 测试全绿）：

| 指标 | 修复前 | 修复后 |
|---|---|---|
| BuildInput/column 微基准 | 53μs（比行存慢 48%） | **3.9μs（比行存快 ~7x）** |
| Additions/column 微基准 | 600μs | 340μs（仍慢于行存 123μs，zero-copy 是行存固有优势） |
| e2e TransformHeavy（8 链式 transform_normalize）5000 items | 与行存持平 | **快 ~30%**（2.7ms vs 3.9ms，allocs -20%，bytes -34%） |
| e2e Small/Medium/Large（recall+filter+sort 形状） | 行存胜 40-90% | 差距收窄到 10-20% |
| e2e N=10（生产 calibrated 形状） | — | 行列基本持平、行存微优 |

## 解法分层

1. **短期**（原型已验证）：ColumnFrame 三处列主序修复。
2. **中期**：`OperatorInput` 批量列访问 API（一次锁 + 一次 lookup 拿整列），消除逐元素接口税——这是列存优势兑现的**前提闸门**。注意属跨引擎 API 面变更（pine-java / pine-cpp 的 ColumnFrame 同构），需跨引擎评估与 cross-validate 覆盖。
3. **长期**：typed columns + arena（`llmdoc/memory/decisions/perf-evolution-roadmap.md` 第一步）。本次数据证明它**必须配合批量列访问 API 才能兑现**——typed `[]float64` 扫描 257ns 与 `[]any` 理想列扫描 262ns 几乎无差，单独做 typed columns 仍会被逐元素接口税吃掉。
4. **使用判据**：transform-heavy + 大 N + 少结构变更 → column；recall/filter/sort 主导或小 N（如生产 calibrated 形状）→ row。

## 教训

- **存储格式优势必须配套访问 API 才能兑现**：列存换了物理布局但没换访问接口，逐元素 `Item()` 让缓存友好性完全不可见。评估任何布局优化前，先确认访问路径是否会兑现它。
- **微基准分解定位比端到端猜测高效**：把一次扫描拆成"理想列扫描 / typed / Item() / 去锁"四档，一轮 benchmark 就把接口税、锁税、map 税分离量化了。
- **"列存实现"里也可能藏着行主序代码**：ColumnFrame 三处热路径都是 per-item 的 map lookup 模式，与存储布局南辕北辙。实现新存储格式时，热路径循环的主序方向要与布局一致。
- **首错优先级是字节级对等契约**（呼应 `review-driven-build-input-error-ordering.md`）：BuildInput 校验优化保留 item-major 迭代顺序，只 hoist 列解析，避免翻转跨运行时首错优先级。

## 实现记录（2026-07-07，三引擎）

调查后同日在 `feat/column-store-batch-access` 分支正式实现并提交，三引擎用同一个模式：

| 引擎 | commit | 批量列访问 API 形式 |
|---|---|---|
| pine-go | `fbf2ef7` | `types.ColumnReader` 可选接口 + `OperatorInput.ItemColumn(field) []any`（类型断言分派，非 ColumnReader 实现走逐元素 gather 降级） |
| pine-java | `bf7ce0b` | `Frame.itemColumnView` default method（返回 null = 不支持，降级 gather）+ `OperatorInput.itemColumn` |
| pine-cpp | `95a3000` | `Frame::item_column` 纯虚方法（双实现必须提供；值拷贝 `vector<Variant>`，无零拷贝逃逸面）+ `OperatorInput::item_column` |

三引擎共同语义契约：元素 i 与逐元素 `item(i, field)` 完全一致（含 item_defaults 对 nil 槽位的替换）；返回值只读、仅当次 Execute 有效；ColumnFrame 无 defaults 时 Go/Java 返回零拷贝视图（C++ 因 Variant 值类型天然拷贝）。10 个内置算子热循环同步改写（normalize/sort/shuffle/dedup/condition/resource_lookup/copy/observe_log/remote_pineapple/lua）。C++ 侧 BuildInput 校验与 item-write 路径本来就是批量写法（`validate_strict_items` bitmap 扫描），只移植了读侧。

验证：cross-validate section 1/3/4/5/9/14 全绿（column-store 95/95）；三引擎 differential fuzz 120 rounds 零 divergence；fuzz 生成器补 defaults+nil 定向维度（`16440e8`）后 coverage 证实 `ItemColumn` defaults-copy 分支被真实覆盖（此前 0 命中——"fuzz 全绿"若无覆盖率背书可能是空转绿）。

### 实现阶段的额外教训

- **OperatorInput 持 spec 裸指针是隐性生命周期契约**：pine-cpp `test_remote_pineapple.cpp` 的 helper 把栈上 `InputFieldSpec` 传给 `build_operator_input` 后返回 OperatorInput，spec 悬垂——生产端 Engine 的 `input_specs_` map 拥有 spec 生命周期所以无恙，但测试侧无人守护。该潜伏 bug 被 `item_column` 新增的字段名比较触发为可复现 SIGSEGV，ASan 一次定位。教训：给持裸指针/引用的构造路径写测试 helper 时，必须复刻生产环境的所有权关系，而非最短代码。
- **验证覆盖要量化不要感觉**："fuzz 跑过列存代码了吗"这个问题的正确回答方式是 `go build -cover` + `GOCOVERDIR` 实测函数级覆盖，而不是从生成器代码推断。实测发现主路径覆盖良好（ItemColumnView 75-87%）但 defaults-copy 分支 0 命中，遂补定向维度。
- **flaky divergence 先排环境再怀疑代码**：机器 load 30+ 时 fuzz 出现 go-vs-java divergence（java rc=1），同 config 单独重跑 30 次全过、master 基线同样无法复现，判定为高负载下 JVM 子进程环境问题。与 benchmark-hygiene 的"跑前查 load"纪律同源——fuzz 也是负载敏感的。
