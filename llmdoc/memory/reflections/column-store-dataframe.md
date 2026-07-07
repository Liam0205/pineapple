# 列存 DataFrame 实现复盘

## 任务

为 Pineapple 引擎新增列存 DataFrame 实现，与现有行存并存，通过 JSON 配置 `storage_mode` 选择。

## 做得好的

- **接口抽象干净**：`Frame` 接口 7 个方法，`RowFrame` 和 `ColumnFrame` 各自实现，调用方（scheduler、pine.go）完全不感知存储格式。
- **包级转发函数保持兼容**：`BuildInput`、`ApplyOutput`、`ToResult` 和 `New` 作为转发函数保留，外部测试和调用方无需大规模改动。
- **Parity 测试全面**：所有 19 个功能测试均用 `t.Run("row", ...)` / `t.Run("column", ...)` 双模式运行，确保行为完全一致。
- **Benchmark 覆盖 7 个关键操作**：揭示了列存的优势场景（构造、字段写入）和劣势场景（removals、reorder），为用户选择提供了数据依据。

## 教训

- **`[]any` 而非 typed columns 是正确的初始选择**：系统全程使用 `any`，typed columns 在无类型声明机制的前提下复杂度过高。但这意味着列存的 GC 优势主要来自减少 map 对象数量，而非类型化数组。
- **列存在结构变更操作上天然劣势**：Removals 和 Reorder 需要遍历所有列，性能是行存的 1/10。适合"大量 item、少结构变更"的场景（如大规模召回后的 transform 阶段）。
- **ColumnFrame.ApplyOutput 中 additions 的字段补齐逻辑**：新增 item 可能引入新字段，需要对现有列补 nil；现有列中不在新 item 里的字段也要补 nil。这个双向补齐是列存特有的复杂度。

## 文档更新

- `design_doc/03_data_abstraction.md` — 标记列存已实现，记录 `[]any` 设计选择
- `README.md` — 新增行存/列存可切换特性描述
- `llmdoc/architecture/dag-engine.md` — DataFrame 不变量小节更新为接口抽象 + 双实现

## Benchmark 数据摘要（Apple M5, 1000 items × 10 fields）

| 操作 | Row | Column | 列存优势 |
|------|-----|--------|---------|
| New | 323μs / 4004 allocs | 190μs / 18 allocs | 1.7x 快, 222x 少分配 |
| BuildInput | 130μs | 130μs | 持平 |
| ItemWrites | 91μs | 78μs | 1.2x 快 |
| Removals | 5μs | 49μs | 行存 10x 快 |
| Reorder | 1.5μs | 12.5μs | 行存 8.6x 快 |
| Additions | 425μs / 5011 allocs | 568μs / 1125 allocs | 分配少但时间慢 |
| ToResult | 143μs | 140μs | 持平 |

> **后续深化（2026-07-07）**：上表的"持平/微弱优势"结论已被后续调查解释——逐元素 `Item()` 接口税（27x）是列存优势不可见的第一性原因，另有三处行主序热路径已做列主序化原型验证。见 `column-vs-row-parity-investigation.md`。
