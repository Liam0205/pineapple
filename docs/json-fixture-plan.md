# JSON Fixture 统一测试 & Java-Pine 计划

## 目标

将 Pineapple 从纯 Go 引擎扩展为 Go + Java 双引擎架构：
- 共享 JSON fixture 进行 cross-validation，消除 training-serving skew
- Java-Pine 作为完整引擎，支持独立加载 pipeline config 并执行
- 包含 codegen（生成 Python DSL 绑定）和 HTTP server

## 架构对照

| Go 组件 | Java 对应 | 状态 |
|---------|-----------|------|
| `internal/types` (Operator, IO) | `page.liam.pine` 核心接口 | ✅ 完成 |
| `internal/registry` | `Registry` | ✅ 完成 |
| `operators/` (11 个纯计算) | `operators/` | ✅ 完成 |
| `operators/lua` (LuaJ) | `TransformByLua` | ✅ 完成 |
| `internal/config` | `Config` | ✅ 完成 |
| `internal/dag` | `DAG` | ✅ 完成 |
| `internal/dataframe` | `DataFrame` | ✅ 完成 |
| `internal/runtime/scheduler` | `Engine` (拓扑序串行) | ✅ 完成 |
| `internal/runtime/parallel` | data_parallel 支持 | ⬜ 待实现 |
| `pkg/server` | HTTP Server | ⬜ 待实现 |
| `pkg/codegen` | Codegen 工具 | ⬜ 待实现 |
| `pkg/resource` | Resource 管理 | ⬜ 待实现 |
| `pkg/metrics` | Metrics 接口 | ⬜ 待实现 |

## 阶段

### Phase 1: 算子层 fixture ✅

- [x] 设计 fixture JSON schema
- [x] Go fixture runner
- [x] 迁移 11 个算子共 44 用例
- [x] Java 算子实现（含 LuaJ），fixture 全部通过
- [x] CI java-test job

### Phase 2: Pipeline fixture 设计（进行中）

- [x] 设计 pipeline fixture schema（config + request → expected result）
- [x] Pipeline fixture runner（Java `PipelineFixtureTest`，动态加载 `fixtures/pipelines/*.json`）
- [x] 首个 pipeline fixture: `transform_then_filter.json`（transform_copy + filter_truncate，2 cases）
- [x] Go pipeline fixture runner（`fixtures/pipelines/pipeline_fixture_test.go`，复用 Engine + fixture JSON）
- [x] 覆盖场景：recall → merge → filter → sort、skip/branch（Lua 控制 flag）、barrier（normalize + paginate）、嵌套 SubFlow
- [ ] 从现有 E2E 测试迁移更多 pipeline fixture

### Phase 3: Java 引擎核心（进行中）

- [x] Config 加载（解析 RootConfig JSON，提取 OperatorConfig + RawParams，展开 pipeline tree）
- [x] DAG 构建（拓扑排序、依赖推导：field-level + row_dependency + barrier + transitive reduction）
- [x] DataFrame 实现（common/items 状态管理，BuildInput with defaults，ApplyOutput）
- [x] Engine（按拓扑序执行算子，处理 skip/branch control）
- [x] Pipeline fixture 通过（9 cases，5 fixtures cross-validated Go/Java）
- [ ] 更多 pipeline fixture 覆盖复杂场景（Lua common→item 交叉、多 recall source 分支）

### Phase 4: data_parallel 与并发

- [ ] data_parallel > 1 时的 item 分片并行执行
- [ ] 确保等价性（Go 和 Java 对同一输入产生相同输出）
- [ ] 并发安全性保证

### Phase 5: Resource 管理

- [ ] Resource 接口 + Registry
- [ ] 定时刷新机制
- [ ] resource_lookup / recall_resource 算子 Java 实现
- [ ] Redis 算子 Java 实现（Jedis/Lettuce）

### Phase 6: Server

- [ ] HTTP 服务框架（Vert.x / Netty / Spring Boot — 待定）
- [ ] `/execute` endpoint（等价于 Go `handleExecute`）
- [ ] `/stats` endpoint
- [ ] `/health` endpoint
- [ ] Pipeline 热加载

### Phase 7: Codegen

- [ ] 读取 Go 端 OperatorSchema 注册表（或 JSON 导出）
- [ ] 生成 Python DSL 绑定（等价于 Go codegen 输出）
- [ ] 验证生成代码与 Go 端一致

### Phase 8: 集成与 CI

- [ ] pipeline fixture cross-validation（Go 和 Java 对同一 pipeline 输出一致）
- [ ] 性能基准对比
- [ ] 发布流程

## 当前进度

Phase 1 完成，Phase 2-3 大部分完成。

- Go 端：算子 fixture runner（11 文件 44 用例）+ pipeline fixture runner（5 文件 9 用例）
- Java 端：引擎核心完成（Config → DAG → DataFrame → Engine），55 用例通过
  - 44 算子级 fixture + 9 pipeline 级 fixture（含 Lua、recall、skip/branch、barrier、嵌套 SubFlow）
  - Go/Java cross-validation：同一 fixture 两端均通过
- CI：java-test job 已配置
- Pipeline fixture schema: `fixtures/pipelines/*.json`（config + cases[request → expected]）

待完成：
- 从现有 E2E 测试迁移更多 pipeline fixture
- Phase 4（data_parallel）

下一步：Phase 4（data_parallel 与并发）。

---

## 附录：Phase 1 调查结果

### 算子测试统计

| 算子 | Unit | Bench | 可转 Fixture | 不可转原因 |
|------|------|-------|-------------|-----------|
| filter_condition | 5 | 1 | 4 | 1 Init 参数校验 |
| filter_truncate | 7 | 1 | 4 | 3 Init 参数解析 |
| filter_paginate | 5 | 0 | 4 | 1 参数类型 coerce |
| transform_by_lua | 12 | 7 | 6 | 并发/Init/nil/string 返回值 |
| transform_copy | 6 | 0 | 5 | 1 Init bad direction |
| transform_dispatch | 4 | 2 | 3 | 1 Init |
| transform_normalize | 7 | 2 | 4 | 2 Init + 1 BadType |
| transform_redis_get | 12 | 0 | 7 | 需 miniredis |
| transform_redis_set | 11 | 0 | 5 | 需 miniredis |
| transform_by_remote_pineapple | 10 | 0 | 2 | 网络行为 |
| transform_resource_lookup | 6 | 0 | 4 | 需 resource.Context |
| transform_size | 3 | 0 | 2 | 1 Init |
| merge_dedup | 5 | 3 | 3 | 2 Init 参数校验 |
| observe_log | 5 | 0 | 2 | 无副作用验证 |
| recall_static | 7 | 7 | 3 | Go-specific |
| recall_resource | 5 | 0 | 3 | 需 resource.Context |
| reorder_sort | 8 | 5 | 5 | 3 Init |
| reorder_shuffle_by_salt | 4 | 0 | 2 | 非确定性 |
| **合计** | 141 | 28 | ~68 | |

### 关键设计决策

1. **fixture 层级**：算子级（params + input → output）+ pipeline 级（config + request → result）
2. **包名**：`page.liam.pine`（Maven groupId: `page.liam`）
3. **Lua**：Java 端使用 LuaJ (org.luaj:luaj-jse)，与 Go 端 gopher-lua 行为等价
4. **不转的算子**：依赖外部服务的在 Phase 5 单独处理，非确定性算子不做 fixture
