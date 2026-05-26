---
name: pine-cpp-p3-series-buildout
description: pine-cpp P3-A to P3-D 阶段复盘（remote pineapple, MetricsAware, StatsProvider, LuaVM pool）
type: reflection
---

# pine-cpp：P3 系列特性建设与基础设施对齐复盘

## 任务背景

在 `pine-cpp-p1-p2-buildout` 的 reflection 之后，又合并了 4 个 commit，属于 P3 路线图的第一部分：

- **P3-A (19f241f)**: `transform_by_remote_pineapple` 算子，引入 `http_client` 与 `ssrf` 基础设施。底层使用 libcurl。
- **P3-B (56e7850)**: 增加 `MetricsAware` 接口并在 Engine 构造预处理。
- **P3-C (bc7848a)**: 增加 `StatsProvider` 接口、Engine 级调用 `operator_custom_stats`，并在 HTTP `/stats` 端点追加组合响应 `operator_detail`。
- **P3-D (25f5ed7)**: Lua 算子中引入并挂载 `StatePool`（池化并发分配 LuaVM，记录 baseline globals 并复原释放），并实现了 `StatsProvider` 和 `MetricsAware` 发布活跃指标和统计。

## 关键教训

### 1. 核心扩展接口也是能力对等的重要维度
`MetricsAware` 与 `StatsProvider` 是运行时的可感知性基建，如果 C++ 端不提供，那么用 C++ 写的定制算子就没办法获得原本在 Go 侧零成本提供的方法回调。这两者的接口必须在 `pine::` 公共 namespace 与 `Operator` 配合，通过 RTTI (`dynamic_cast`) 弱耦合。

### 2. Lua 状态重置策略对齐
在 Go 侧 LuaPool 每借出归还，也会检查 globals 变量污染。在 C++ 侧，`LuaSnapshot` 截取 LUA_GLOBALSINDEX 引用，并清理在此之后的注入。这种"隔离语义"不属于"语法层面"，而属于运行时的"可预期不变量"。

### 3. Server stats 端点 JSON 增量
此前 pine-cpp 的 `/stats` 仅有 `operators`, `scheduler`, `server`。增加了 `custom_stats`（从 `StatsProvider` 取回）之后补上了最后一个 `operator_detail` block，终于在接口输出格式上达到了 100% 对齐。

## Promotion 候选

### 已同步到到 `architecture/pine-cpp-runtime.md`
- `MetricsAware` 接口（`Engine` 预创建后自动注入）与 `StatsProvider` 接口
- `transform_by_remote_pineapple` 算子：基于 `libcurl` 实现 SSRF 安全保护、HTTP POST 超时与最大体积限制
- Lua 集成：`StatePool` 提供按需 `LuaVM` 分配与借用，维护 baseline globals 快照并在释放时清理变异

### 已同步到 `reference/metrics-observability.md`
- C++ 端暴露的接口：`StatsProvider` (`std::map<std::string, int64_t>`)、`MetricsAware`、`operator_custom_stats`
- Lua `StatePool` 挂载 `StatsProvider/MetricsAware`

---

此 reflection 代表 P3 阶段前 4 个 commit 按规补充进了现有的体系文档，不会造成架构文档滞后。