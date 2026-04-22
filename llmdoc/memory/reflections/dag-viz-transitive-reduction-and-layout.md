# DAG 可视化传递性归约与纵向布局复盘

## 任务

修复 DAG 可视化冗余边问题（间接依赖被画成直接依赖），并将布局方向从横向改为纵向。

## 做得好的

- **仅在渲染层裁剪，不修改内部图**：`reducedEdges` 和 `reachableWithout` 在 `visualize.go` 中实现，保持 `dag.go` 的完整边集不变。调度器仍依赖完整边集，可视化独立做传递性归约。
- **BFS 排除直接边的设计**：对每条边 u→v，从 u 出发做 BFS 但跳过 u→v 这条直接边，若仍能到达 v 则该边冗余。实现简洁正确。
- **测试构造了精确的验证场景**：`TestTransitiveReduction` 用 recall→barrier→transform 链验证完整图有冗余边、归约后只剩 2 条边、DOT 输出不含冗余边。

## 教训

- **llmdoc 中可视化描述陈旧**：`dag-engine.md` 写的"边来自 `Node.Succs`"在 transitive reduction 后已不准确，渲染层现在使用 `reducedEdges(g)`。这次更新需要同步。
- **DAG 内部确实有冗余边但这是设计选择**：barrier edges + hazard edges 各自独立添加，必然产生可达性冗余。用户问"DAG 构建是否也应去除冗余边"——结论是内部图保留完整边集以确保调度正确性，仅可视化做归约。

## 文档更新

- 需更新 `llmdoc/architecture/dag-engine.md` 可视化小节，反映传递性归约和纵向布局。
