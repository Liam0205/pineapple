# DAG 可视化功能复盘

## 任务

为 Pineapple 新增 DAG 可视化能力，支持 DOT（Graphviz）和 Mermaid 两种输出格式，同时提供编程 API 和 HTTP 端点。

## 做得好的

- **模块放置准确**：`internal/dag/visualize.go` 直接访问 `Graph` / `Node` 结构体，无需向外暴露 internal 类型。`Engine.RenderDAG` 作为公共 API 桥接。
- **颜色映射从算子类型推导**：复用 `config.OperatorConfig.OperatorType` 字段（在 `pine.go` 编译阶段填充），没有引入新的类型查找路径。
- **Mermaid ID 清洗**：考虑到算子名含 hash 后缀，做了 `sanitizeMermaidID` 防御性处理。
- **测试覆盖**：用最小 DAG 验证节点、边、类型标签在两种格式中都存在。

## 教训

- **编译阶段填充 `OperatorType`**：DAG 可视化依赖 `Node.Config.OperatorType`，该字段在 `pine.go` 的编译循环中由 registry schema 填充，不是从 JSON 读取的。如果有新的可视化路径绕过编译流水线，该字段会为空。
- **design_doc 与 README 应在功能提交中一并更新**：这次在同一个 commit 中完成了，避免了中间状态不一致。

## 文档更新

- `llmdoc/architecture/dag-engine.md` 新增 "DAG 可视化" 小节和检索指针。
- `design_doc/08_observability.md` 将 DAG 可视化从"后续再做"移至"已实现"。
- `README.md` 新增 `GET /dag` API 参考。

## 可提升为稳定文档的候选

无。可视化模块足够独立，检索指针和架构文档中的描述已覆盖需求。
