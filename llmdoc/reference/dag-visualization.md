# DAG 可视化参考

本文档总结 Pineapple 0.5.7 起稳定支持的 DAG 渲染入口、层级折叠规则和 HTTP 参数约定。

## 权威文件

- `pine-go/pine.go`
- `pine-go/internal/config/load.go`
- `pine-go/internal/dag/dag.go`
- `pine-go/internal/dag/visualize.go`
- `pine-go/pkg/server/server.go`

## 稳定入口

### 引擎 API

`Engine.RenderDAG(format string, opts ...RenderOption) (string, error)` 支持两类选择维度：

- 输出格式：`"dot"`、`"mermaid"`
- 渲染选项：当前稳定项为 `WithCollapse(level int)`

`WithCollapse(0)` 表示不折叠，返回完整算子级 DAG；`WithCollapse(1)` 表示按最外层 SubFlow 聚合；`WithCollapse(2)` 及更高值表示按路径前 N 段做层级折叠。

### HTTP API

`GET /dag` 暴露同一能力：

- `format=dot|mermaid` — 选择输出格式，默认 `dot`
- `collapse=N` — 启用层级折叠，其中 `N` 为非负整数

约定如下：

- `collapse=0` 或未提供 `collapse` 时返回完整图
- `collapse=1` 表示按顶层 SubFlow 折叠
- `collapse=2`、`3`... 表示按更深层路径前缀折叠
- 非整数或负数请求应返回 400

## SubFlow 元数据来源

SubFlow 折叠不是重新解析 DSL，而是沿用引擎编译期保留下来的分组元数据：

1. `pine-go/internal/config.ExpandOperatorSequenceWithSubFlows()` 递归展开 `pipeline_group` / `pipeline_map`
2. 同时生成 `opToSubFlow map[string]string`，值为算子直接父 SubFlow 的层级路径
3. `pine-go/internal/dag.Build(...)` 把该映射写入每个 `Node.SubFlow`
4. `pine-go/internal/dag/visualize.go` 在渲染时读取 `Node.SubFlow` 并按路径前缀分组

因此 SubFlow 只是图节点的附加标签，不参与 DAG 边推导。

## 渲染模式

### 完整算子级视图

- `RenderDOT(g *Graph) string`
- `RenderMermaid(g *Graph) string`

特点：

- 一个算子对应一个节点
- 直接遍历 `Node.Succs` 输出边
- 节点按 `OperatorType` 着色
- 展示完整执行依赖细节

### SubFlow 折叠视图

- `RenderCollapsedDOT(g *Graph, level int) string`
- `RenderCollapsedMermaid(g *Graph, level int) string`

层级折叠规则：

- 节点按 `Node.SubFlow` 的路径前 `level` 段分组，路径分隔符固定为 `/`
- 若 `level` 大于路径段数，则使用完整路径作为组名
- `SubFlow == ""` 的节点保持独立
- 同组内部边被省略
- 仅保留跨组边
- 多条指向同一组对的跨组边在输出中去重

示例：

- `level=1` 时，`recall/candidates/a` 与 `recall/candidates/b` 都归入 `recall`
- `level=2` 时，两者都归入 `recall/candidates`

这意味着折叠图是“按层级前缀聚合的依赖骨架图”，适合在不同抽象层次阅读，不适合诊断组内执行顺序。

## 输出语义

### DOT

- 完整视图和折叠视图都使用 `rankdir=TB`
- 完整视图节点按算子类型着色
- 折叠视图中，SubFlow 聚合节点与独立节点使用不同的固定样式

### Mermaid

- 完整视图和折叠视图都使用 `graph TB`
- 完整视图会清洗 Mermaid 节点 ID，避免名称中的 `-`、`.`、空格破坏语法
- 折叠视图使用稳定的 `g0`、`g1` 等聚合节点 ID

## 与执行图的关系

DAG 渲染永远基于 `Build()` 之后的执行图：

- 执行图已做过传递性归约
- 渲染层不再额外做归约
- 折叠视图不会新增执行边，只会过滤组内边并去重组间边

所以：

- 完整图反映最小执行约束集
- 折叠图反映同一约束集的 SubFlow 聚合投影

## 稳定约束

1. `Node.SubFlow` 只能作为可视化分组标签，不能参与调度或 hazard 推导。
2. `RenderDAG` 的 format 维度与 collapse 维度彼此正交：任一格式都可选择完整或某一级折叠视图。
3. HTTP `collapse=N` 是对 `WithCollapse(N)` 的稳定映射，且 `N` 必须为非负整数。
4. 折叠渲染必须对跨组边去重，避免多个底层算子边把高层图放大成噪声。
5. 折叠渲染必须保留未归属 SubFlow 的独立节点，不能把空 SubFlow 误并为一个伪组。

## 检索指针

- 编译期 SubFlow 映射生成：`pine-go/internal/config/load.go`
- DAG 节点上的 SubFlow 存储：`pine-go/internal/dag/dag.go`
- 渲染实现：`pine-go/internal/dag/visualize.go`
- 公共 API：`pine-go/pine.go`
- HTTP `/dag` 参数：`pine-go/pkg/server/server.go`
