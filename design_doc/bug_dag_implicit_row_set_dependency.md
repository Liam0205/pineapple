# Bug: DAG 构建器缺少 item 字段算子对 _row_set_ 的隐式依赖

## 现象

当 MutatesRowSet 算子（filter/reorder/merge）后面跟着一个有 item 字段但未声明 ConsumesRowSet 的 Transform 时，DAG 构建器不会在二者之间生成依赖边。调度器可能让二者并行执行，导致 Transform 访问已失效的行索引，触发 `SetItem index out of range`。

## 复现条件

任何 pipeline 中，MutatesRowSet 算子之后存在一个有 `item_input` 或 `item_output` 但未实现 `ConsumesRowSet` 接口的自定义算子。当调度器并发度足够时，该算子可能在行集变更前（或变更中）执行。

所有 18 个内置算子不受影响——凡是有 item 字段的内置 Transform 都已手动嵌入 `ConsumesRowSetMarker`。但下游自定义算子若遗漏此标记即会触发。

## 根因分析

### 背景：从 barrier 到 _row_set_ 的演进

v0.7（commit `328109e`）将旧的全屏障模型（`addBarrierEdges`、`IsBarrier()`）替换为基于 `_row_set_` 哨兵字段的精细依赖追踪。三个 marker interface 决定算子如何参与 `_row_set_` 追踪：

| marker | _row_set_ 操作 |
|--------|---------------|
| `AdditiveWritesRowSet` | additive write（Recall 追加行） |
| `ConsumesRowSet` | read（等待行集稳定） |
| `MutatesRowSet` | mutating write（变更行集，重置 tracker） |

### 缺陷

`addEdges()` 的 item pass 中，只有显式声明了上述三个 marker 之一的算子才参与 `_row_set_` 追踪。**有 item 字段但无任何 marker 的算子不参与**，在 `_row_set_` tracker 上不产生任何边。

旧 barrier 模型中这不是问题——Filter/Reorder/Merge 作为全屏障会序列化所有前后驱算子。新模型删除了 barrier 的"保守安全网"，但未为 item 字段算子补上等价的行集稳定性保证。

### 本质

任何按索引访问 item 数据的算子（通过 `SetItem`/`GetItem`）本质上依赖行集的稳定性——哪些行存在、行的索引映射不变。这是 `_row_set_` 语义的隐含消费者。**"有 item 字段"指 `$metadata.item_input` 或 `$metadata.item_output` 任一非空——读（`GetItem`）和写（`SetItem`）都按索引访问，都需要行集稳定。** `AdditiveWritesRowSet` 例外，因为它只追加新行，不访问已有行的索引。

## 修复方案

在 `addEdges()` 的 item pass 中，对有 item 字段但未声明 `AdditiveWritesRowSet` 且未声明 `ConsumesRowSet` 的算子，自动注入 `_row_set_` 作为 read 依赖。

```go
// After existing ConsumesRowSet/AdditiveWritesRowSet injection:
if !isCommon && !opCfg.ConsumesRowSet && !isAdditiveWrite {
    if len(readFields) > 0 || len(writeFields) > 0 {
        readFields = append(readFields[:len(readFields):len(readFields)], rowSetSentinel)
    }
}
```

### 效果

通过标准 hazard 追踪自然产生正确的边：

- **RAW ← Recall**：item 字段算子等待所有先序 Recall 追加完行
- **RAW ← MutatesRowSet**：item 字段算子等待行集变更完成
- **WAR → MutatesRowSet**：后续行集变更等前面的 item 字段算子读完

### 与旧 barrier 的关系

| 场景 | 旧 barrier | 新 auto-inject |
|------|-----------|----------------|
| common-only 算子 | 被 barrier 序列化 | 不受影响（无 item 字段） |
| 同一 MutatesRowSet 后多个 item 算子 | 全部序列化 | 可并行（都是 _row_set_ reader，reader 间无冲突） |
| 依赖范围 | ALL 前驱 | 仅最近的 _row_set_ mutWriter + 所有 additiveWriters |

新方案是旧 barrier 约束的严格子集——保留了正确性，去掉了不必要的序列化。

## 附带清理

`internal/types/operator.go` 的 `IsBarrier()` 方法在 v0.7 删除 `addBarrierEdges()` 后已无调用方，是死代码，一并移除。

## 影响范围

- 三引擎（Go/Java/Python）的 DAG 构建逻辑均需同步修复
- 现有 34 个 fixture 和 18 个内置算子不受影响（均已有正确 marker）
- 需新增 fixture 覆盖此场景
- 需更新 DAG 单元测试中断言旧（错误）语义的用例
