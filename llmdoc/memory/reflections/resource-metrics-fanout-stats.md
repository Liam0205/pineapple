# 资源级指标 fan-out 与 /stats.resources 复盘

PR（issue #66）让内置 `redis_connection` 资源发出的 4 个指标（pool 总连接/空闲 gauge、PING 延迟 histogram、up gauge）对外可观测，折叠进三运行时已有的 `/stats` 端点新增 `resources` 顶层键，并补 cross-validate section 16 锁定跨运行时一致性 + 正确性。本轮（C++ 收尾 + cross-validate + 文档 + 提交）顺利完成，无返工。

## 关键设计决策：fan-out（Tee）路由

最初考虑过三种方案：
1. 现状（资源指标只进注入的 Provider，`/stats` 看不到）
2. 专用 Collector-only（资源指标只进 `/stats.resources`，下游 Prometheus 看不到）
3. fan-out：`ResourceManager` 注入 `Tee(注入的Provider, 专用Collector)`，两条路径并存

用户不熟悉 Prometheus，核心诉求是「下游无需改动即可拿到新指标，且旧路径不受影响」。fan-out 同时满足：`/stats.resources` 零配置可见 + 已接 Prometheus 的下游照常导出。引擎指标仍直接走注入 Provider、不进 Collector，因此 Collector 天然只含资源级指标（当前 4 个 redis），不掺入 18 个引擎/服务级指标——这是 scope 隔离的关键，不需要额外过滤。

## parity 强断言：resources 恒存在

三运行时的 Collector 都无条件创建（随 ResourceManager 一起），因此 `/stats.resources` 键**总是存在**：`metrics_name` 为空时为 `{}`，非空时为 metric-centric 形状。这让 cross-validate 负例（空 `metrics_name` → `resources == {}`）成为一条干净的跨运行时强断言，而非「键缺失/键为空」的模糊判定。histogram 存整数纳秒（`sum_ns`）、每层键字典序排序，沿用 `/stats.http` 子树的既有约定，保证字节级对齐。

## 探针时机便于测试

`redis_connection` 资源在 `ResourceManager.Start()` 同步初始化时立即跑一次探针（probe-once-then-tick，间隔 15s），所以 `up=1`/ping count≥1/pool gauge 在第一个请求前就绪。cross-validate 抓 `/stats.resources` 时仍加了短重试循环兜底探针 goroutine 的微小竞态。

## 教训

- **scope 靠路由而非过滤实现**：把「哪些指标进 Collector」交给「谁通过 Tee 写入」来决定（只有 ResourceManager 走 Tee），比在 Collector 内按名字过滤更简洁、更难出错。
- **always-present 键优于 conditional 键**：让子树恒存在（空则 `{}`）比「有数据才加键」更利于跨运行时 parity 断言。
