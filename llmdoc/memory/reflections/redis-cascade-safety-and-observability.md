# [Redis cascade-safety 超时暴露 + 跨引擎 doc parity + per-command 可观测]

## Task

- 分支 `fix/redis-timeouts-issue-incident-0622`，13 commits（5f94098b → 10db0402），针对 2026-06-22 tipsy-recsys redis 雪崩故障的工程修复。
- 三条主线：(A) `redis_connection` 资源新增 5 个 cascade-safety 参数（dial/read/write/pool timeout + pool_size），三引擎对齐；(B) pine-cpp Client 的 connect/AUTH 失败收敛进 `connected()==false` 静默降级，并补 SIGPIPE 守卫；(C) codegen markdown doc 跨引擎字节级对齐 + per-command Redis 指标 + cross-validate 收口。
- 三轮本地 code review：第一轮 REQUEST_CHANGES（1 阻塞 + 4 重要 + 2 小），第二/三轮 APPROVE。

## Expected vs Actual

- Expected：故障定位 → 暴露超时 → 三引擎一次性对齐 → 文档 parity → 加可观测，单线推进。
- Actual：A/B 单线，但 (C) 在第一轮 review 后**重新发散**——`b2bca14d` Java doc render drift 暴露三处文案差异，`75ce3c03` 收尾时又被发现 required-first 顺序不一致；`fa2459f0` 加 byte-equal gate 后才发现 cpp 完全没 markdown emit；`6c6478b6` 用一个完整方案补齐（schema 加 metadata + cli flag + 22 算子手填）。最初我推荐"cpp 只 emit Parameters/DSL 节"折中方案，被用户否决"不要后退、不要打折扣"。

## What Went Wrong

- **方向反了**：第一次想给 Go OperatorSchema 加 Metadata 字段以便 Java 对齐，被用户矫正"为啥要改 pine-go? 不应该让 pine-{cpp,java} 对齐 pine-go 吗?"——Go 是 source of truth，Java/cpp 单向对齐 Go。这条原则在 `must/conventions.md` 的"跨引擎能力等价"语境隐含但未点明 codegen 路径。
- **工作量低估推动了打折方案**：review 反馈"补 cpp markdown emit"我推荐折中跳过 Metadata 节作为 followup，被用户否决。最终全做完只比折中多约 1.5 小时——review 反馈点的工作量评估应更严，"打折"应是最后选项而非默认起点。
- **Java write_timeout 决策需要写入 schema**：Jedis 只暴露单 `socketTimeout`，必须把 `read_timeout_ms` 与 `write_timeout_ms` 折叠成 `max(read, write)`。一开始考虑 min（更"安全"），但 LRANGE/ZRANGEBYSCORE 等"等服务器响应"的读命令时间长，min 会误杀正常请求；选 max + 在 schema description 补 engine-specific note 让用户知情。
- **cpp 漏 SIGPIPE**：测试 fixture（accept 后立刻 close）测的是 AUTH/SELECT 失败收敛，意外发现 cpp redis client 用裸 `write()`，redis 中段断连会 SIGPIPE 整死进程；pine-cpp/server.cpp 早就用 `MSG_NOSIGNAL`，redis client 漏了。**生产 bug 借测试副发现暴露**——测试设计带"边角能见度"非常值。
- **doc parity 三轮才收敛**：第一轮单元测试 pin pascalCaseEnum / toPythonLiteral，第二轮才发现 required-first 排序还差，第三轮才发现 cpp 根本没 emit markdown。单测无法替代 byte-equal diff——`fa2459f0` 把 Go vs Java/cpp 字节比较锁进 cross-validate 01-codegen-schema 后，类似漂移会即刻暴露。

## Root Cause

- **故障背景**：PING p99 970ms 触发，`redis_connection` 沿用 go-redis v9 默认（dial 5s / read,write 3s / PoolSize 10×GOMAXPROCS=20），Java 也是 Jedis 默认。SLA 是 60ms 但默认是数秒级 → 请求堆积、heap_inuse 单 pod 飙到 3.87 GiB OOM。**根因是默认值与业务 SLA 错配 + 资源参数未暴露**，schema 暴露 + 默认 2000ms 是治本。
- **doc 漂移根因**：Go 用字符串 `"Transform"`、Java 用 `enum.name().toLowerCase()`；Go 字符串 default 走 `pythonLiteral` 用 `fmt.Sprintf("%q")`，Java 走 `toString()`；Go bool default `false`，Java/Python 风格 `False`。三处独立漂移因为各自实现路径独立——只有 byte-equal gate 才能发现实现路径独立但输出必须相同的契约。
- **knowledge source 在哪**：codegen Python doc 的"engine-specific note"必须由 Go schema description 注入，因为 Go 是 codegen 唯一源。
- **review 增量循环价值**：3 轮 review 都给出精确反馈，作为输入推进了 PR 范围（per-command metrics 是第一轮 review 后用户主动追加的，非原计划）；如果只跑一轮 review 收尾会少 4 个 commit 的修复深度。

## 关键设计决策（可重用）

- **cascade-safety 五参数**：dial/read/write/pool timeout（默认 2000ms）+ pool_size（默认 0=引擎默认）。三引擎 schema 对齐，Java write_timeout 折叠为 `max(read, write)` 并在 description 加 engine-specific note；cpp `pool_timeout_ms` 当前 no-op（acquire 不阻塞按 idle queue cap 工作），schema 仍对称保留。
- **失败收敛契约**：cpp Client AUTH/SELECT 失败需 try/catch、close fd_、`fd_=-1`，与 dial-failure 路径对称，落入 `connected()==false` 静默降级。`fail_on_error=false` 静默降级是 redis 资源外部契约，**新增 client 路径必须查这条**。
- **per-command metrics 设计**：histogram `pine_redis_command_duration_seconds` + counter `pine_redis_command_total`，labels `name`/`command`/`status`。status 四态硬编码：`ok`（含 redis.Nil cache miss）/ `timeout` / `pool_timeout` / `error`。
- **lifecycle 命令对称**：HELLO/CLIENT/PING/AUTH/SELECT 在 Java/cpp 不存在（client 不发这些协议命令），所以 Go 用 redis.Hook 时**过滤**这些 lifecycle 命令而非让 Java/cpp 加。cross-validate 16 强断对齐 6 个业务命令名，cells 因 facade 插桩位置不同剥离 cmd_*。
- **cpp 错误归类已知缺口**：cpp client 抛单一 `std::runtime_error`，timeout/pool_timeout 当前落 error 桶（known follow-up），需后续在 client 层引入错误类型分层。

## Missing Docs or Signals

- `must/conventions.md`"跨引擎能力等价"应点名"codegen 单向对齐"：Go schema 为 source of truth，Java/cpp 单向对齐，codegen 改动方向不可反向。
- `guides/cross-layer-validation.md` 缺一条：codegen markdown 输出必须有 byte-equal gate 跨引擎；单元测试 pin format helper 不够。
- `reference/operator-contract.md` Redis 句柄型资源章节应补 cascade-safety 五参数与 Jedis socketTimeout 折叠的工程注意。
- pine-cpp 网络客户端"必须 MSG_NOSIGNAL"是隐式契约，不只是 server.cpp 的事——redis client 与未来任何 raw socket 路径都该提醒。

## Promotion Candidates

1. **codegen byte-equal gate 是 codegen 类改动的最终守护者**——纳入 `cross-layer-validation` 检查项，`fa2459f0` 已落地为 01-codegen-schema 第 1e 节 Go vs Java/cpp 双 arm。
2. **跨引擎对齐方向**（Go 单向被对齐）应进 `must/conventions.md`，避免再次"想给 Go schema 加字段以便 Java 对齐"的反向尝试。
3. **failed-path 静默降级契约审计**：每次 client/资源新增失败路径（AUTH/SELECT/dial/pool）都要走完 `fail_on_error=false → connected()==false` 链路，写进 `reference/operator-contract.md` Redis 章节。
4. **资源 schema 默认值 = SLA 友好值**：库默认值（go-redis 5s/3s/3s/20）不是业务 SLA 友好值；`redis_connection` 默认 2000ms 的决策应作为通用准则记录在 `reference/operator-contract.md` 资源章节。
5. **review-driven scope expansion 是正反馈**：per-command metrics 是 review 第一轮后增补，最终拉高 PR 价值；不要把 review feedback 当作"修缺陷"处理，可接受 scope 增长——记入 `guides/standard-workflow.md`。

## Follow-up

- 由 recorder 把上面 5 条 promotion 落地到 `must/conventions.md` / `cross-layer-validation.md` / `operator-contract.md`，并在 `reference/operator-contract.md` 补 cascade-safety 五参数表 + Jedis 折叠注记。
- cpp client 错误类型分层（拆 timeout / pool_timeout / generic）作为 known follow-up，提一个 issue。
- 监控验证：故障复盘 → 上线后 7 天观察 `pine_redis_command_duration_seconds` p99 与 `status="timeout"` 计数，确认 2000ms 默认对当前 SLA 友好；若 timeout 计数显著高于业务侧实际容忍度，再调默认。
