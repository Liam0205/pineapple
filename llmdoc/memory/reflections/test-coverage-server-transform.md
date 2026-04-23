# 测试覆盖率补齐复盘

## 任务

补齐 `pkg/server` 与 `operators/transform` 两个包的测试覆盖率，重点覆盖 HTTP handler 与 Redis 读写算子的主路径、降级路径和错误路径，并将整体 Go 覆盖率从 70.5% 提升到 77.6%。

## 预期与实际

- 预期结果
  - `pkg/server` 应补齐可稳定单测的 HTTP handler 逻辑，验证状态码、响应体和格式分支，同时明确不测包含 `log.Fatal` 或无限循环的启动/监听路径。
  - `operators/transform` 中的 Redis 读写算子应在无外部 Redis 依赖的前提下覆盖 `Init`、`Execute`、多种 `data_type`、nil client 降级与错误分支。
  - 覆盖率提升应来自真实行为断言，而不是只为数字补 trivial test。
- 实际结果
  - `pkg/server/server_test.go` 新增 14 个 HTTP handler 测试，使用 `httptest.NewRequest` + `httptest.NewRecorder` 直接验证 `/health`、`/execute`、`/stats`、`/dag` 各分支；通过 `enginePtr.Store(engine)` 注入测试引擎，最终覆盖 `handleHealth` 100%、`handleExecute` 85.7%、`writeJSON` 100%、`handleStats` 100%、`handleDAG` 100%。
  - `operators/transform/redis_get_test.go` 与 `operators/transform/redis_set_test.go` 分别新增 11 个与 10 个测试，引入 `github.com/alicebob/miniredis/v2` 提供内存 Redis 服务，覆盖 string/set/list 三种 `data_type`、缺省参数、nil client 降级、unsupported type、空集合和 TTL 等行为。
  - `operators/transform/size_test.go` 增加 `TestSizeOpInit`，并为 `buildKeySuffix` 增加辅助函数覆盖，最终 `operators/transform` 包覆盖率从 56.6% 提升到 88.8%，`pkg/server` 从 10.6% 提升到 52.3%，整体覆盖率从 70.5% 提升到 77.6%。

## 过程中的关键判断

- `pkg/server` 的覆盖策略不是追求包内所有函数 100%，而是只测可稳定隔离的 handler 与配置重载逻辑；`Run` 和 `watchConfig` 分别包含 `log.Fatal` 与无限循环，不适合纳入常规单测。
- handler 测试不必启动真实 HTTP server。直接调用 `handleHealth`、`handleExecute`、`handleStats`、`handleDAG` 能更稳定地锁定分支行为，也避免把失败归因混入路由或端口绑定。
- `handleExecute`、`handleStats`、`handleDAG` 依赖包级原子指针中的引擎状态，因此测试需要像运行时一样通过 `enginePtr.Store(engine)` 设置共享引擎，并在 cleanup 中恢复，确保测试隔离。
- Redis 算子测试若依赖外部服务，CI 稳定性和本地复现成本都会变差。`miniredis` 让测试既能验证真实客户端交互，又不需要 docker 或额外环境准备。

## 做错了什么 / 差点踩坑的点

- 如果只从“哪部分覆盖率最低”出发，很容易把精力花在 `Run`、watcher 这类不稳定路径上，最后得到难维护的测试。事实证明先区分“可稳定断言的行为边界”和“应该通过设计说明显式排除的路径”更重要。
- 对 Redis 算子来说，只测 happy path 不足以真正提高质量；nil client 降级、unsupported `data_type`、空 set/list、miss 场景才是最容易在重构时被破坏的分支。
- `size` 与 `buildKeySuffix` 这类小函数虽然简单，但如果长期无人覆盖，包级覆盖率会留下明显空洞；补这类 trivial case 时仍应明确其目的，是锁定公共 helper 或默认行为，而不是凑数字。

## 根因分析

- 之前 `pkg/server` 覆盖率极低，不是因为 server 不可测，而是默认把入口包视为“集成层”，没有继续往下拆到 handler 级别；一旦改为直接调用 handler，绝大多数 HTTP 逻辑都可以稳定单测。
- Redis 算子此前缺少覆盖，主要障碍不是代码复杂度，而是测试基础设施缺位：没有现成的内存 Redis 方案时，容易默认跳过这类带外部依赖的算子测试。
- 覆盖率提升最快的方式并不是增加更多端到端测试，而是为已有清晰行为边界的组件补细粒度单测，特别是 handler 分支和算子 `Init`/`Execute` 合约。

## 缺失文档或信号

- 现有稳定文档已经说明 CI 有 Go 测试与覆盖率产物，但还可以更明确补充一条实践：对带 Redis 依赖的算子单测，优先使用 `miniredis` 这类内存服务，而不是依赖外部实例。
- 现有文档没有显式总结 server handler 的测试模式：对依赖全局原子状态的 HTTP handler，优先使用 `httptest.NewRequest` + `httptest.NewRecorder` 直接调 handler，并通过原子指针注入测试依赖。这是可复用的测试套路。
- `Run` / watcher 这类包含进程级退出或无限循环的路径应视为“不适合常规单测”的边界，也值得作为覆盖率策略的一部分被记录下来。

## 可提升为稳定文档的候选项

- 适合提升到 `llmdoc/guides/ci-quality-baseline.md`
  - 为有外部依赖的算子测试补一条模式：优先使用内存替身（例如 Redis 使用 `github.com/alicebob/miniredis/v2`）来覆盖真实客户端交互与错误分支。
  - 补一条覆盖率补齐原则：优先覆盖 handler/算子级可稳定断言的行为边界，明确排除 `log.Fatal`、无限循环等不适合常规单测的路径。
- 暂时保留在 memory
  - 本次具体覆盖率数字（52.3%、88.8%、77.6%）更像阶段性快照，适合保存在 reflection，而不应写入稳定文档作为长期事实。

## 后续建议

- 后续若再补齐带外部依赖的算子测试，先检查是否已有对应内存替身库，再决定是否需要集成测试环境。
- 若 `pkg/server` 后续继续演进，可考虑把当前 handler 测试模式沉淀为单独 guide，专门说明全局状态注入、响应断言与不测边界。