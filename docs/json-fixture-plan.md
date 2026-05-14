# JSON Fixture 统一测试 & Java-Pine 计划

## 目标

将 Pineapple 的算子测试从 Go 代码内嵌形式迁移为独立 JSON fixture，
使 Go 和 Java 双端可以加载同一组 fixture 进行 cross-validation，消除 training-serving skew。

## 阶段

### Phase 0: 调研与计划
- [x] 编写计划文件

### Phase 1: 现有测试统计
- [x] 统计每个算子的测试数量和类型
- [x] 识别哪些测试可以转为 fixture

### Phase 2: JSON fixture 格式设计与迁移
- [ ] 设计 fixture JSON schema（config + input + expected_output）
- [ ] 编写 Go test runner 加载 fixture 文件
- [ ] 逐算子迁移测试为 fixture 形式
- [ ] 保留原有 Go 测试作为回归保护

### Phase 3: Go 引擎适配
- [ ] 确保引擎支持 fixture runner（单算子隔离执行模式）
- [ ] 验证原有测试行为不变
- [ ] 验证 fixture 测试等价性

### Phase 4: 整理
- [ ] 项目结构整理（fixture 目录、runner 位置）
- [ ] 文档更新

### Phase 5: Java-Pine 开发
- [ ] Java 项目初始化
- [ ] 核心数据模型（OperatorInput/Output, Common/Items）
- [ ] Operator 接口 + Registry
- [ ] 逐算子实现（共享 fixture 验证）
- [ ] CI cross-validation

## 当前进度

Phase 1 完成，进入 Phase 2。

---

## Phase 1 调查结果

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
| transform_redis_get | 12 | 0 | 7 | 需 miniredis，infra-error 测试 |
| transform_redis_set | 11 | 0 | 5 | 需 miniredis，error cases |
| transform_by_remote_pineapple | 10 | 0 | 2 | 网络行为(SSRF/timeout/500) |
| transform_resource_lookup | 6 | 0 | 4 | 需 resource.Context |
| transform_size | 3 | 0 | 2 | 1 Init |
| merge_dedup | 5 | 3 | 3 | 2 Init 参数校验 |
| observe_log | 5 | 0 | 2 | Init + SetMetadata + 无副作用验证 |
| recall_static | 7 | 7 | 3 | 内存隔离(Go-specific) + 3 Init |
| recall_resource | 5 | 0 | 3 | 需 resource.Context |
| reorder_sort | 8 | 5 | 5 | 3 Init |
| reorder_shuffle_by_salt | 4 | 0 | 2 | shuffle 非确定性 |
| **Lua sandbox** | 8 | 0 | 0 | 全部 Go-specific 安全测试 |
| **Lua pool** | 11 | 0 | 0 | 全部 Go-specific 生命周期 |
| **合计** | 141 | 28 | ~68 | |

### 关键发现

1. **约 68 个测试可转为 JSON fixture**（有明确的 params + input → output 断言）
2. **不可转的测试**主要是：Init 参数校验、Go 并发/生命周期、需要外部服务（Redis/HTTP）
3. **没有真正的 E2E 正确性测试**使用真实注册算子跑完整管线并断言输出
4. **Redis/Remote 算子**依赖外部服务，fixture 只能覆盖纯计算逻辑部分

### Fixture 迁移优先级（由易到难）

1. **第一批（纯计算，无外部依赖）**：filter_condition, filter_truncate, filter_paginate, transform_copy, transform_dispatch, transform_normalize, transform_size, merge_dedup, reorder_sort, recall_static
2. **第二批（Lua 脚本）**：transform_by_lua（需要 fixture 中嵌入 lua_script）
3. **第三批（需 mock 外部服务，可能不转）**：transform_redis_get/set, transform_by_remote_pineapple, recall_resource, transform_resource_lookup
4. **不转**：Lua sandbox/pool 测试、shuffle（非确定性）
