# Issue #217：请求上下文与取消语义三运行时对等

日期：2026-10-27。分支 `fix/request-context-217`。单会话完成三运行时实现 + 跨运行时 probe，D 另开 #220。

## 结论先行

| 项 | 内容 |
|---|---|
| 缺陷 | Java（`CancellationToken`）/ C++（`std::stop_token`）的算子 execute 只拿到取消标志，没有截止时间、请求级值与资源；更要紧的是**三方都把「调用方取消后剩余节点没跑」报成成功**，下游输出静默缺失，`dag_executions_total{success}` 照计 |
| 修法 | A 专用 `CancelledError`（`pine: execution cancelled: <ctx err>`，三方字节一致）；B Java/C++ execute 第一参数改为 `RequestContext`，资源与 provider 都经它传递（删 `ResourceAware` / `CancellationToken` / `OperatorInput::resources()` / `Frame::set_resources`）；C 内置阻塞算子真正响应取消、server 停机先取消在途请求 |
| 锁定 | `scripts/cross-validate/22-embedded-cancellation.sh` 三方 probe 五场景逐字节；各运行时单测（Go `scheduler_test.go`、Java `CancellationTest`、C++ `tests/test_cancellation.cpp`） |
| 稳定文档 | `reference/operator-contract.md`「请求上下文与取消」为权威副本，`architecture/dag-engine.md`、`architecture/pine-cpp-runtime.md`、`design_doc/07` 只留指针 |
| 提交 | PR #221：Go / Java / C++ 实现各一（`fix(go)` / `feat(java)!` / `feat(cpp)!`）、三个 probe 各一、cross-validate、docs，及 probe 计时修复三个（不写 SHA：rebase 与 squash merge 都会让它失效） |

## 可复用的判断

**1. 「取消」要问的不是能不能停，而是停下之后报什么。** issue 标题是「签名缺少请求上下文」，读起来是 API 形状问题；实测第一件事——预先取消一个上下文再执行——三方都返回成功、结果缺字段。这才是用户可见的缺陷，签名只是它的载体。所以 Go 虽然已有 `context.Context`，也要修（Go 那一提交是 `fix(go)` 而不是 `refactor`）：参考运行时并不自动正确。判据：**对一个取消路径，先写「取消后返回值是什么」的断言，再谈怎么传播取消。**

**2. 结果判定的三条边界要分开测，它们彼此独立。** 「有节点被跳过 → CancelledError」「取消落在最后一个节点运行中 → 成功」「算子自己返回 ctx 错误 → 命名该算子的 ExecutionError」。只测第一条时，后两条任一实现错都不会变红。probe 场景 `cancel_mid_run` / `cancel_last_node` / `deadline_sleep` 就是按这三条切的。

**3. 跨运行时 probe 必须消除时序决定权。** 早先的 C++ R10-4 测试用「200ms 后 cancel、算子 sleep 500ms」，哪些节点跑、报什么错取决于调度——只能断言「不死锁」。probe 改成**由算子自己在 execute 里 cancel 调用方上下文**，于是哪个节点被跳过是确定的，才能逐字节比较。需要从外部线程 cancel 的场景（`cancel_sleep`）只断言「被打断的那个算子的错误」，与时机无关。

**3b. 截止时间要从「被测的那一步」开始计，不能从准备阶段开始。** 第一版 probe 在 `NewEngine` / `Engine.create` 之前就创建了 50ms 截止时间的上下文。本地三方一致，PR 的 CI 却只有 Java 分歧：繁忙 runner 上冷 JVM 建引擎就超过 50ms，运行开始时上下文已过期，根节点被跳过，`deadline_sleep` 报 `execution cancelled` 而不是 bench_sleep 自己的错误。修法是每个场景先建引擎、再建上下文与计时器，计时放宽到 500ms（只需晚于运行开始，远小于被打断的 10s）。验证方式值得复用：`taskset -c 0` 把 probe 钉在一个 CPU、旁边跑三个有 `timeout` 上限的死循环，旧 probe 5/5 分歧、新 probe 0/5——**先在本地复现 CI 上的失败，再证明修法让它消失**，否则「CI 重跑绿了」说明不了什么。

**4. 先核实参考实现的行为，再写对等描述。** 文档草稿写了「三方 server 都先取消根上下文再 drain」，核对 `pine-go/pkg/server/server.go:Run` 发现 Go 是 `Shutdown` 后 `ListenAndServe` 立即返回、进程退出，在途请求被丢弃——终态（不跑完长 pipeline）一致，机制不同。写进稳定文档的是后者。

**5. 文档里的耗时断言要问它到底测到了什么。** cross-validate 07 [3] 的 `elapsed < 1500ms` 看起来在证明「取消打断了正在跑的算子」，实际 `run_count` 在运行**开始**时就递增，poll 立即返回，elapsed ≈ curl 的 300ms，无论取消是否生效都通过。真正承重的是 `slow3 exec_count` 不变。改法是如实写注释、把「打断运行中算子」交给 §22 的 `cancel_sleep`，而不是把阈值调得更紧去假装它能测。

## 已接受差异（决策记录）

Lua 取消文案（Go 两个后端自身就不一致）、Lua 单 item 中途不可中断（luaj/LuaJIT 无指令级中断）、已发出 Redis 命令不中断、Java remote 响应体阶段不可取消、Java server 不感知断连（D，#220）。理由与位置见 `reference/operator-contract.md`「已接受差异」。

## 环境踩坑（非项目知识，留给同机后续任务）

- 本机 `date` 是 uutils 0.8.0，`date +%s%3N` 输出纳秒；cross-validate 07 [3] 的 elapsed 因此是乱值而失败。本次用 PATH 前置的临时 shim 绕过，脚本未改；若 CI 镜像也换成 uutils 会同样失败。
- `scripts/cross-validate/_prebuild.sh` 用 `make -j$(nproc)`，在 47 核机器上违反本机 `-j12` 约束；本次手动预构建后单跑 07/22。

## 未做 / 留待

- C++ `execute_traced_into` 在任何运行错误时 trace 为空，Go 保留部分 trace——早于本 issue 的差异，与取消无关，未修（`tests/test_cancellation.cpp` 有注释）。
- #214（Java fatal 路径强制完成 futures）不在范围内。
