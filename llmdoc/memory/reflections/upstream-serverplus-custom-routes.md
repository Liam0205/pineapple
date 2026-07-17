# [serverplus 上游化：跨运行时自定义路由 / Watch 开关 / 嵌入 API]

## Task

- issue #169 / PR #170：把下游 serverplus 包上游化为各运行时（Go/Java/C++）通用能力——自定义路由 `Route{Method, Path, Ingress, Egress}`、`Watch` 热加载开关、嵌入 API（Go `NewServer`/`Execute`/`Acquire`/`Close`，Java `load`/`execute`/`acquire`/`Handle`）。
- 主会话派 investigator（各运行时现状调研）+ 3 个 worker（Go/Java/C++ 实现，各自 worktree 隔离），commit 链 `e31a8302` → `603238ba`。
- CI 全绿，bot review 两轮 APPROVE；唯一 minor（Java `start()` 部分失败泄漏 watcher）在 `603238ba` 修复（load() 副作用回滚）。

## Expected vs Actual

- Expected：各运行时并行实现 + 黑盒 cross-validate 对等 + 一轮收敛。
- Actual：整体达成，但过程有四类摩擦——worktree 改动搬运一次误判、Java 一个既有 bug 被新嵌入 API 暴露（stop 后 `acquireSnapshot()` 无限自旋）、Java 测试诊断链踩了 mvn/-q/孤儿 JVM 三个坑、fixture 结构误用键名浪费两轮。

## What Went Wrong

- **worktree 改动搬运误判**：收集模式是 worker 不 commit，主会话 `git -C <worktree> diff > patch && git apply patch` 收 tracked 改动 + `cp` 收 untracked 新文件。有一次 `cp` 报 "same file" 被误判为 "worktree 与主 checkout 是同一文件"。该错误的真实含义是源和目标解析为同一文件身份（同一路径或同 inode）——GNU cp 不按内容比较；当时更可能是路径参数解析出了同一路径。worktree 是独立副本，改动必须显式搬运；定位这类报错要用规范化路径或 inode 检查（`stat -c %i`），判断内容是否已搬运用 `cmp`/`diff`，不要靠解读 cp 的错误文案。
- **Java 既有 bug 被新入口暴露**：`PineServer.stop()` 原来只 release 快照不清引用，退休快照 refs=0、CAS acquire 恒 false，stop 后调 `acquireSnapshot()` 会无限自旋。以前无人发现是因为 stop 后没有任何入口再调它；新增嵌入 `execute()` 让这条"不可达状态"变成可达。修复为 `getAndSet(null)`，对齐 Go `Close()` 的 `Swap(nil)`。
- **mvn 诊断链三个坑**：(1) `mvn -q` 吞 surefire 摘要，诊断测试结果要用非 `-q` 或读 `target/surefire-reports/`；(2) 用 `timeout` 杀 mvn 会留孤儿 forked JVM（surefire fork 的 JVM 不随父进程死），孤儿吃 CPU 把 load 顶到 3.9 污染负载——应改用 `-DforkedProcessTimeoutInSeconds=N` 让 surefire 自己兜底，`timeout` 包 mvn 后必须 `pgrep -f ForkedBooter` 清孤儿 + `uptime` 复核（与 feedback_kill_zombie_check_load 同一教训再现）；(3) `jstack` 定位 JVM hang 非常高效、直接指到自旋栈帧，但 jacoco javaagent 会让输出被 grep 判为 binary，需加 `-a`。
- **fixture 结构误用**：`fixtures/pipelines/*.json` 的 case 结构是 `{name, request:{common,items}, expected:{...}}`，请求键是 `request` 不是 `input`；误用 `input` 拿到空请求，浪费两轮排查。
- **本机缺 clang-format 二进制**：clang-format 不在 CI（cpp-lint job 只做 WERROR 构建 + hygiene + concat guard），只走本地 `make fmt-check` 和 `.githooks/pre-commit`；本机缺二进制时需 venv pip 装（PEP 668 限制系统 pip）。

## Root Cause

- worktree 误判的根因是把 `cp` 的错误信息当成了文件系统层事实，没有先验证 "patch 是否已 apply" 这个更近的解释。收集流程本身可靠，误判发生在对异常信号的解释环节。
- Java 自旋 bug 的根因是**状态机审计缺位**：给旧组件加新入口时，旧的"不可达状态"会变成可达。`stop()` 的实现假设"stop 后无人再 acquire"，这个假设从未写成契约，嵌入 API 一来就破了。Go 侧 `Close()` 用 `Swap(nil)` 天然没这个问题，说明这本可以在 parity 对照时发现。
- fixture 键名误用的根因是没先读一个现成 fixture 文件确认 schema 就开始写代码。

## 关键设计决策（可重用）

- **编程 API 的黑盒 cross-validate 模式——demo-routes 注入**：custom routes 是编程扩展点（函数指针/闭包），黑盒验证无法直接构造。解法：各运行时 server 二进制各加一个演示开关（Go `-demo-routes` flag / Java `-Dpine.demoRoutes` / C++ `-demo-routes`），注册一条行为完全对齐的 `POST /api/echo` 演示路由，cross-validate section 20 对 echo 200/405/400/404/metrics label/watch=false 做字节比对。这是「编程扩展点如何进跨引擎黑盒验证」的可复用模式，补上了 audit-extensibility-blindspot 那次"能力等价"盲区的验证手段。
- **错误文案逐字对齐从设计期锁定**：`validateRoutes` 的 6 条错误文案在 Go 实现时就写进 Java/C++ worker 的任务卡（含 Go `%q` = 双引号的转换规则），两侧直接按文案实现 + 单测断言字节相等，零返工。相比 pine-java parity 审计 19 轮事后收敛文案，设计期锁定的成本低一个量级（印证 runtime_error_parity_byte_exact）。
- **C++ 有意不做嵌入 API**：C++ server 不在 `include/pine/` 下、非公开库 API，保持 shared_mutex 生命周期方案（handler 持读锁 = Go 引用计数快照的语义等价物），只做 Route + Watch 的黑盒对等。跨引擎对等的判据是**「行为与下游可用范式」而非「实现结构」**。
- **Java 错误模型不对称被接受**：Go `Server.Execute` 把校验错误和执行错误统一为 `err` 传 Egress；Java `Engine.execute` 是抛（ValidationError）/ 返回（`Result.error` 字段）二分，routeHandler 只把抛出的异常传 egress，`result.error` 需 Egress 自查。这是有意接受的语言习惯差异，黑盒行为（demo route 输出）仍字节一致。
- **C++ 可测逻辑抽独立编译单元**：pine-cpp `server.cpp` 依赖 POSIX socket 不进 test target；`validate_routes`/`normalize_path` 抽到 socket-free 的 `routes.cpp`，同时链进 `pineapple-server` 和 `pine_cpp_tests`——对齐 Go validateRoutes 包内可测函数的模式。

## Missing Docs or Signals

- worker worktree 改动收集模式（diff→apply + cp untracked、统一验证提交、删 worktree）没有任何文档描述，本次靠现场摸索；"same file" 这类信号的正确解释也无处可查。
- fixture case 的 `request` 键结构在稳定文档里没有一处写明，只能读源文件反推。
- demo-routes 黑盒验证模式是新发明，`guides/cross-layer-validation.md` 的"扩展点对等验证"一节目前只讲能力等价审计维度，没讲编程扩展点怎么落到可执行验证。
- "给旧组件加新入口需重审旧状态机可达性"这条审计动作在任何 guide 里都没有。

## Promotion Candidates

- 进 `guides/cross-layer-validation.md`：demo-routes 模式——编程扩展点（函数指针/闭包类 API）通过 server 二进制演示开关注册对齐路由，进 cross-validate 做字节比对。
- 进 `architecture/pine-cpp-runtime.md`：C++ 不做嵌入 API 的决策及理由（shared_mutex 生命周期方案 = Go 引用计数快照的语义等价物；对等判据是行为与下游范式）。
- 进 `architecture/dag-engine.md` 接受差异归档：Java 错误模型抛/返回二分 vs Go 统一 err 传 Egress。
- 进 `guides/standard-workflow.md` 或 llmdoc skill：worker worktree 改动收集模式 + "给旧组件加新入口需重审状态机可达性"。
- 仅留 memory：mvn -q / 孤儿 JVM / jstack -a 诊断细节、fixture `request` 键名、clang-format venv 安装——检索到本篇即可。

## Follow-up

- 由 recorder 同步 index.md，并把上面前四条 promotion 落到对应稳定文档。
- Go 侧 `Close()` 与 Java `stop()` 的快照清引用行为现已对齐，后续任何一侧改生命周期须两侧同看（可在 parity 审计 checklist 提一句）。

## 第二轮：深度 code-review 修复（54922de8）

首轮 CI 全绿、bot review 三轮 APPROVE 之后，本地深度 code-review（`.code-review/from-c353c04/from-c353c04-to-603238b.md`）判 REQUEST_CHANGES，找出 8 项真实问题（阻塞 1 + 重要 6 + 小 1），全部属实、无一误报。四个修复 commit（`6509c4a0` Go / `9ae0fa4f` Java / `2201e7a1` C++ / `54922de8` cross-validate+docs）后 CI 全绿、bot 增量审查 APPROVE。

### 教训

- **bot review APPROVE ≠ 无问题**：bot 三轮 APPROVE 只提了 1 个 minor；深度 review 找出 8 项，含 1 个安全阻塞（custom route 绕过 body limit）。深度 review 的价值在于逐项验证边界——请求体上限、ServeMux pattern 语义、生命周期回滚、统计对齐、测试脚本的静默跳过——而不是走查 diff。
- **Go 1.22+ ServeMux pattern 语义陷阱**：把用户提供的 path 直接 `mux.Handle(path, ...)` 注册，尾随 `/` 会变成匿名子树通配符、`{name}` 变通配段、非法花括号直接 panic。声明为「精确路径」的 API 必须用精确字符串查表分发（`fallbackHandler` + map lookup），不能借道 pattern 注册。
- **按值 Config 参数的默认值修改只影响副本**：`NewServer(cfg Config)` 里 `cfg.Addr = ":8080"` 改的是自己的副本；`Run` 后续读原始 `cfg.Addr` 拿到空串 → net/http 用 `:http`（80 端口）。「构造 / 运行分离」重构时，默认值归一化必须抽成共享函数（`normalizeConfig`）在两个入口都调用。这是 Run→NewServer 拆分引入的回归，原先单函数写法里不存在。
- **共享分发层统一施加请求体上限**：编程扩展点（Ingress 读原始请求）不能依赖每个业务实现自己限流——Go 在 routeHandler 里用 `http.MaxBytesReader` 包 body、`MaxBytesError` 集中回 413；Java 用 `LimitedBodyStream`（读到 limit+1 抛异常）对齐 Go 边界语义；C++ 天然在 socket 读取层集中限流（先例证明「核心层统一」是对的形状）。section 20 新增 check [8] 验证 custom route 超限 413 各运行时字节一致。
- **shutdownNow 只中断不等待**：Java `stop()` 原来 `shutdownNow` 后立即拆 snapshot，在途 reload（不检查中断标志）可在拆除后 `getAndSet` 重新发布快照——服务器「复活」+ 泄漏。修复为 `awaitTermination` join + `stopLock` 下 `closed` 标志防落后发布。这与首轮修的「stop 后自旋」是同一状态机的两个不同竞态窗口——**给状态机修一个洞之后要重扫整个状态机**，首轮只修了可达性洞，没扫并发洞。
- **旁路共享执行路径 = 统计静默丢失**：C++ custom route 直接调 `engine_->execute` 绕过 `execute_with_trace`，scheduler run_count 和算子 exec/skip 统计全部不计数（Go/Java 计数）。black-box section 20 只查了 http metrics label，没查 scheduler/operator 统计增量，所以 7/7 绿也没发现。修复后 section 20 加 check [7]（/api/echo 后 stats 增量各运行时比对）。教训：**对等验证要覆盖「执行路径的可观测副作用」，不只是响应字节**。
- **可选测试臂的静默消失**：section 20 里 `CPP_SERVER` 已配置但 readiness 失败时，既不 fail 也不加 total，C++ 臂静默消失、整段仍 pass。修复：配置了二进制但 readiness 失败 → 硬 fail + 终态断言 `cpp_cr_total > 0`。这是 fail-open 反模式——可选组件的跳过必须显式区分「未配置」（跳过合法）和「配置了但坏了」（必须失败）。
- **硬编码数量再犯**：README 写「20 个 section」+ Makefile help 还是「19 section」，违反 `llmdoc/must/conventions.md` 明令禁止的硬编码层数约定（首轮还自己把 19 改成 20，等于两个入口互相矛盾）。规则已存在但写 README 时没触发检索——**改 README 的定量描述前先查 conventions.md**。

### Promotion Candidates（本轮）

- 进 `guides/standard-workflow.md`：与首轮已收录的「新公开入口需重审旧状态机可达性」合并成完整审计动作——修一个洞后重扫整个状态机，可达性洞与并发洞分开扫。
- 进 `guides/cross-layer-validation.md`：对等验证需覆盖执行路径的可观测副作用（scheduler/operator 统计增量，不只响应字节）；可选测试臂必须 fail-closed（「配置了但坏了」硬失败并断言检查数 > 0）。
- 进 `architecture/dag-engine.md`（server 生命周期一节）：custom route 的请求体上限由共享分发层统一施加（Go MaxBytesReader / Java LimitedBodyStream / C++ socket 层），属跨引擎行为契约。
- 仅留 memory：ServeMux pattern 语义陷阱、按值 Config 副本回归、limit+1 边界对齐细节——检索到本篇即可。
