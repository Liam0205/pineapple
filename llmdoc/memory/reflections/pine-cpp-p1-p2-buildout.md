---
name: pine-cpp-p1-p2-buildout
description: pine-cpp P1/P2 全面建设阶段（18 commit）的 llmdoc 偏差与缺口复盘
type: reflection
---

# pine-cpp：P1/P2 全运行时建设阶段复盘

## 任务背景

上次 llmdoc 实体更新发生在 commit `6798faa`（observe active-reader 语义修正）。其后 18 个 commit 把 pine-cpp 从"已接入 cross-validate 的 MVP+"推进到与 pine-go 行为/扩展面对等的完整第四运行时，涵盖：

- **算子框架重构**：`Operator` 基类、marker 类型、`register_operator()` API、`PINE_REGISTER_OPERATOR` 宏、17 个内置算子从 `run_xxx` 函数迁移到 subclass、operators 目录按 category 重组、warning operator-name 前缀统一在框架层。
- **P1 系列 (B–F)**：skip 字段必须出现在 `$metadata.common_input` 校验；根级 `log_prefix` 解析与 `WithLogPrefix` option；`_PINEAPPLE_VERSION` / `_PINEAPPLE_CREATE_TIME` / `resource_config` 解析；pineapple-server 新增 `-read-header-timeout` / `-read-timeout` / `-write-timeout` / `-idle-timeout` / `-max-body-size` 五项 CLI flag；CLI 错误前缀与 pine-go 字节级对齐。
- **P2 系列 (A–H)**：scheduler `peak_concurrency` 追踪；`/execute` nil vs empty result 区分（`has_result` flag）；HTTP body 读取双重边界（Content-Length + hard_cap）和 malformed Content-Length 处理；graceful shutdown 引入 `in_flight_` 计数；`pkg/metrics::Provider` 抽象 + `NopProvider`；`resource::Manager` + `FetcherFactory` 注册子系统（后台刷新线程、`snapshot()`、`stop()` 等价于 pine-go `pkg/resource`）；HTTP middleware 注入（`ServerConfig::middlewares` + `MiddlewareContext`）；内置 HTTP metrics middleware（与 pine-go `http_metrics.go` 同名指标和桶）。

llmdoc 在这一阶段全程未更新：`architecture/pine-cpp-runtime.md` 仍停留在 MVP 描述，`reference/metrics-observability.md` 仅描述 pine-go 通道，`overview/project-overview.md` 把 pine-cpp 描述为"已启动建设"，`must/conventions.md` 的注册副作用条目和入口点描述都不包含 pine-cpp。

`pine-cpp-mvp-to-full-runtime.md` 是上一轮已经识别出大面积偏差但 **尚未被 recorder 落地** 的 reflection（仍处于未提交状态）。本轮在它之上额外累积了 18 个 commit。

---

## 关键教训

### 1. "上一轮 reflection 未落地"是新偏差的放大器

`pine-cpp-mvp-to-full-runtime.md` 提到"在新运行时的开发开始时，预先为关键数据类型和执行模型留章节占位符"，但因为它的 recorder 步骤被跳过，文档主体没有更新，下一轮（P1/P2）开发又继续在偏差状态下工作，差距进一步扩大。

reflection 不写入稳定文档时，对未来的提醒作用近乎为零——LLM 在 startup 阅读 must/overview 时不会读 reflections。

**避免方式**：reflection + recorder 必须连续完成；任何 reflection 在未落地前，下一次 llmdoc 更新应优先处理上一轮悬挂的 promotion 项，再处理新增 delta。

### 2. "C++ 不在 pine-go 边界内"的隐含心理是偏差源

P2 系列的多数提交是 pine-go pkg 的逐项对等迁移（pkg/metrics、pkg/resource、pkg/server middleware）。开发节奏强调"pine-go 是规范参考"，导致 llmdoc 仍把 `reference/metrics-observability.md` 仅作为 pine-go 文档而非"跨运行时 observability 契约"。

但实际上，metrics provider 抽象、http_metrics middleware、resource Manager 是 **跨运行时能力契约**，每次任一运行时新增对等实现都应在该文档加锚点。

**避免方式**：把 `reference/metrics-observability.md` 的标题与权威文件段落改为"跨运行时观测参考"，每个稳定能力列出"四运行时实现位置"表格。新增运行时的对应 API 时，必须在该表格补一行。

### 3. CLI flag / server option 是用户契约，不应作为"实现细节"略过

`pineapple-server -read-header-timeout` 等 5 项 flag 是面向用户的 CLI 契约。pine-go 文档把 server timeout 默认值写入 `reference/metrics-observability.md`（间接），但 pine-cpp 这一对等并未在任何稳定文档中体现。

**避免方式**：CLI/HTTP server 公开 flag 应在 `overview/project-overview.md` "入口点与打包边界"段落用同一张表罗列三/四运行时的等价 flag，避免某个运行时新增 flag 后无人记录。

### 4. "register_operator" + 宏注册是新的 C++ 端约定，但 `conventions.md` "注册基于副作用"段落未提

`must/conventions.md` 列出了 Go 的 `pine.Register` / Java 的 static initializer / Python 的 `__init__.py` 三种注册模式，但 C++ 端的 `PINE_REGISTER_OPERATOR` 宏（也是 static init 副作用）从未加入。这意味着 LLM 在 onboarding pine-cpp 时无法从约定文档获得"在哪里注册算子"的最小信息。

**避免方式**：本次更新必须把 C++ 的 `PINE_REGISTER_OPERATOR` 与 Go/Java/Python 一同列出，并指明聚合机制（CMake 链接 `pine_operators` 静态库）。

### 5. 第三个版本数硬编码错误（再次）

`pine-cpp-mvp-to-full-runtime.md` 提出"删除所有文档中对运行时数量和 cross-validate 层数的硬编码"，但 conventions/overview 还在使用"三运行时"。本次仍未彻底改造成查表/查脚本指针。

**避免方式**：本次更新做最小有效改动——把"三运行时"改写为"各运行时"或"Go/Java/Python/C++ 运行时"，并在 conventions.md 新增明确条款禁止后续再次硬编码。

---

## Promotion 候选（合并上一轮悬挂项 + 本轮新增）

### 必须落地到 `architecture/pine-cpp-runtime.md`

- 删除 "MVP 边界" 章节，替换为 "已实现能力" 段落，覆盖：
  - HTTP server `/execute` `/stats` `/dag` `/health`、ServerConfig 五项 timeout flag、`-max-body-size`、middleware 注入与内置 http_metrics middleware、graceful shutdown 与 `in_flight_` 计数
  - hot-reload（mtime watcher）
  - ColumnFrame / Column 类型层级、JsonColumn 升迁、shared_ptr<const Column> COW
  - `Operator` 基类、marker 类型、`PINE_REGISTER_OPERATOR` 宏、operators 目录 category 划分
  - `Engine::peak_concurrency()`、debug snapshot + StartTime trace
  - `metrics::Provider` 抽象（含 NopProvider）与 `EngineOptions::metrics_provider`
  - `resource::Manager` + `register_fetcher_factory()`，与 pine-go `pkg/resource` parity
  - root-level `log_prefix` / `_PINEAPPLE_VERSION` / `_PINEAPPLE_CREATE_TIME` / `resource_config` 解析
- 测试策略一节补 4 个 CI job（cpp-build / cpp-sanitizer / cpp-lint / cpp-test）以及当前 cross-validate cpp 覆盖范围
- 错误对等契约一节补"CLI stderr 前缀（error reading config / error creating engine / execution error: ...）与 pine-go 字节级一致"

### 必须落地到 `reference/metrics-observability.md`

- 在权威文件段落补 pine-cpp 文件：
  - `pine-cpp/include/pine/metrics.hpp`
  - `pine-cpp/src/runtime/metrics_nop.cpp`
  - `pine-cpp/src/server/http_metrics.cpp`
  - `pine-cpp/src/server/server.hpp`（`ServerConfig::middlewares`、`Middleware`、`MiddlewareContext`）
- 在"内置 HTTP 请求指标中间件"段落补充：四运行时同名指标与桶；C++ 端通过 `pine::server::http_metrics_middleware(provider)` 显式注入到 `ServerConfig::middlewares`，区别于 Go 默认内置在 mux 包装层
- 在"双通道观测模型"段落补充：pine-cpp scheduler peak_concurrency 已通过 `Engine::peak_concurrency()` 暴露到 `/stats.scheduler`

### 必须落地到 `must/conventions.md`

- "注册基于副作用" 段落补 C++ 的 `PINE_REGISTER_OPERATOR` 宏与 operators 静态库链接模型
- 跨运行时引用统一改写为"各运行时"或显式列出 Go/Java/Python/C++，删除"三运行时"硬编码
- "外部 I/O 与并发安全默认值"补 C++ 的 graceful shutdown 在 `Server::stop()` 中等待 `in_flight_ == 0` 或 5s 超时
- 新增条款：禁止在文档主体硬编码运行时数量与 cross-validate 层数

### 必须落地到 `overview/project-overview.md`

- 把 pine-cpp 从"规划中"改为"已落地的第四运行时"，并在入口点段落新增 `pineapple-cpp-run` / `pineapple-cpp-render-dag` / `pineapple-cpp-server`（含 timeout / max-body-size flag）
- "为何如此拆分"段落保留 pine-cpp 作为标杆运行时定位的描述

### 必须落地到 `guides/ci-quality-baseline.md`

- 在 CI workflow 表格补 cpp-build / cpp-sanitizer / cpp-lint / cpp-test 四个 job 行
- 在 cross-validate 段落记录当前 cpp 覆盖范围（codegen-schema、render-dag、execution、column-store、error、server-http、cancellation、concurrent、raw-byte、hot-reload、redis-integration、extensibility-parity、metrics-parity 中接入 cpp 的 section 列表）

### 留在 memory（不进入稳定文档）

- 各 commit 的 P1-x / P2-x 编号映射
- `thread_local MiddlewareContext* t_mw_ctx` 这一 send_response 注入细节
- parse_duration_seconds 的 Go-style 后缀解析具体实现
- CLI 错误前缀字节级 diff 列表
- 单独的 `resource_config` JSON 解析 unit test 名称

---

## 下次类似任务的检查清单

1. **每次 llmdoc:update 任务开始时，先扫描 `llmdoc/memory/reflections/` 中存在但未落地（即对应稳定文档仍体现旧状态）的 reflection；优先合并落地，再处理新 delta。**

2. **新增运行时能力（HTTP 路由、新 CI job、新数据类型、新 server flag、新中间件）的 commit 必须在同一 PR 中携带 llmdoc delta。如果当时跳过，commit message 必须显式打 `docs-debt` 标签。**

3. **运行时数量、cross-validate 层数等定量描述禁止出现在 must/overview/architecture 主体文字中，统一指向 `scripts/cross-validate/` 目录列表或运行时表格。**

4. **跨运行时能力契约文档（metrics-observability、operator-contract、cross-layer-validation）任一运行时新增对等实现时，必须在权威文件段落补对应 C++/Java/Python 文件锚点。**

5. **CLI flag / server option 是用户契约，必须在 `overview/project-overview.md` 的入口点表格里同步罗列各运行时等价 flag，不要分散到各运行时架构文档。**
