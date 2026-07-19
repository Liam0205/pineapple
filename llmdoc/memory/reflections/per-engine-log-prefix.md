# [log_prefix 从进程全局改为引擎实例级]

## Task

- issue #172 / PR #173：把 `log_prefix` 的作用域从进程全局改为引擎实例级。修复前三引擎各错各的：Go 用 `log.SetPrefix()` + 包级 `sync.Once`（first-engine-wins，后来的引擎前缀被静默忽略、日志带别人的前缀）；Java 用 CAS set 一个从未被消费的 System property（死状态）；C++ 早就是实例成员 `log_prefix_` 但没有任何日志路径消费它（也是死状态）。
- 修复后三引擎统一为引擎实例作用域：Go 每个 Engine 持 `*log.Logger`（`WithLogPrefix` > JSON `log_prefix`，flags 不变），新增 LoggerAware/LoggerHolder 算子注入（DebugHolder 内嵌 LoggerHolder，DebugAware 算子免费获得）、`runtime.Plan.Logger` 带进调度器 [pine-debug] 行、`Engine.Logger()` 暴露给嵌入方；Java Engine 实例字段 + `LoggerAware.setEngineLogPrefix` + `AbstractOperator.logf`；C++ 让 `log_prefix_` 真正被 [pine-debug] 行和 observe_log（新 LoggerAware 接口）消费。
- observe_log 的 schema description 从 "Go standard log" 改为 "engine's logger"，三引擎 schema 源同步 + codegen 再生成，byte-parity 保持。commit 链 `d061e7b9`（Go）→ `81e86ef2`（Java）→ `f9da078b`（C++）→ `4ab804d4`（codegen docs）。
- bot review 首轮 REQUEST_CHANGES（2 重要 + 1 小，全部属实），修复 `8e8a6913`（Go calldepth）+ `00526343`（Java printf）后 APPROVE，CI 全绿。

## Expected vs Actual

- Expected：三引擎一轮实现 + schema 同步 + 一轮 review 收敛。
- Actual：整体达成，但首轮实现留了两个真实缺陷（Go `Plan.logf` calldepth 抄错、Java 用户前缀拼进 printf 格式串），用户与 bot review 各自独立命中了 calldepth 问题。

## What Went Wrong

- **Go `Plan.logf` 的 calldepth 照抄了 3**：`log.Logger.Output(calldepth, ...)` 的 depth 要按包装层数逐层 +1。LoggerHolder.Logf 经两层（Logf→logOutput→Output）用 3 是对的；Plan.logf 只有一层包装（logf→Output），照抄 3 导致 Lshortfile 越过 goroutine body 指到 runtime 父帧（asm/proc.go），行号全错。用户当场问了"行号是不是不对了"，bot review 也抓到同一处。修复 `8e8a6913` 改为 2，并在两处注释里写清逐层推导。
- **Java 把用户可控前缀拼进 printf 格式串**：`printf(prefix + format, args)` 让用户配置的 log_prefix 成为格式串一部分，含 `%` 的前缀（如 `"[100%] "`）运行时抛 UnknownFormatConversionException。Go 的 `log.New` 把 prefix 当字面量、C++ 用 `<<` 拼接，天然安全；只有 printf 家族有这个坑。修复 `00526343`：prefix 单独 `print` 或作为 `%s` 实参。
- **首版差点漏掉 schema description 联动**：改 observe_log 行为后其 description 里的 "Go standard log" 已不属实，而这个字符串同时存在于三引擎 schema 源（Go schema / Java AllOperators / C++ OperatorSchema）和 codegen 产物（doc/operators），漏任何一处 cross-validate section 1 的 byte-equal gate 就会红。

## Root Cause

- **#172 本身是 #169 嵌入 API 的下游后果**：logOnce/SetPrefix 是为单实例进程设计的全局状态，多引擎在 v0.10.13 成为一等公民后，"first-engine-wins"从无害实现细节变成静默语义缺口。这是「新公开入口让旧全局状态成为缺陷」模式的又一例——前例：`NewServer` 让 stop 后状态可达（Java acquireSnapshot 自旋）、按值 Config 的 Addr 默认值副本回归（均见 upstream-serverplus-custom-routes.md）。
- **三引擎漂移成三种错误形式而 parity 审计未察觉**：三家都"有 log_prefix 这个字段"，但只有 Go 真正生效（且是全局污染），Java/C++ 是两种不同的死状态。历轮 parity 审计对比的是"字段存在性与赋值路径"，没有追到**消费点**——值存进去之后谁读、读了产生什么可观测输出。死状态在"存在性对比"下完全隐形。
- calldepth 错误的根因是把它当常量抄，而它是 wrapper 层数的函数；printf 错误的根因是没有把"用户可控字符串"与"格式串"当作两类必须隔离的东西。

## Missing Docs or Signals

- 「审计全局状态在多实例下的归属」没有任何 guide 提及：`sync.Once` / 静态 CAS / System property / 全局 setter 这类形式在嵌入 API 出现后都应过一遍"多实例时这个状态该属于谁"。
- parity 审计维度（`must/conventions.md` 跨引擎能力等价审计）只覆盖端点行为与扩展能力，没有"配置字段要追到消费点"这一条——死状态（存了没人读）是现有维度的盲区。
- calldepth 的逐层推导规则、printf 格式串与外部输入的隔离规则，此前无处可查（Go 侧 `global-log-prefix.md` 反思只记录了全局 logger 约束，未涉及 wrapper depth）。

## Promotion Candidates

- 进 `guides/standard-workflow.md`（与已收录的「新公开入口需重扫状态机」合并）：嵌入/多实例 API 引入后，审计所有进程级状态（`sync.Once`、静态 CAS、System property、全局 setter），逐个回答"多实例时这个状态该属于谁"。
- 进 `must/conventions.md` 跨引擎审计维度：parity 对比不止字段存在性，要追到消费点——值存了谁读、读了产生什么可观测输出；"存而不读"的死状态与"全局污染"同为缺陷。
- 进 `reference/operator-contract.md` 或安全约束：外部/用户可控字符串永不进 printf 格式串，要么作为 `%s` 实参、要么单独输出（仅 printf 家族有此坑，`log.New` prefix 与 `<<` 天然安全）。
- 已完成：`architecture/dag-engine.md` 的 log_prefix 作用域描述已在 `b15f0e14` 更新为 engine-scoped。
- 仅留 memory：calldepth 按 wrapper 层数逐层 +1 且注释写清推导（LoggerHolder.Logf=3 / Plan.logf=2 的具体数值）；改 schema description 前先 grep 三引擎 schema 源 + codegen 产物再 `make codegen`——检索到本篇即可。

## Follow-up

- 由 recorder 把前三条 promotion 落到对应稳定文档并同步 index.md。
- 下次为 #169/#172 这类嵌入场景做回归时，可顺手扫一遍剩余的包级 `sync.Once` 与静态可变状态，确认没有下一个 first-engine-wins。

## 第二轮：深度 code-review 修复（efbd43fd）

首轮 bot review 修完 3 项（calldepth、printf 注入、残留 import）之后，本地深度审查（`.code-review/from-v0.10.13/from-v0.10.13-to-c7c129d.md`）再判 REQUEST_CHANGES：3 项重要问题，全部属实。修复 commit：`004befd7`（Go 三态）、`77517416`(Java 单次写出) 、`a8609d61`（C++ 测试）、`efbd43fd`（docs）。

### 教训

- **option 语义的跨运行时 parity 也要审「哨兵值」**：Go 用空串同时表示「未设置」和「显式设空」，`WithLogPrefix("")` 无法覆盖 JSON 前缀；Java（nullable String）与 C++（std::optional）同一调用产生空前缀——三家 option 行为分歧。修复：Go 改 `*string` 对齐 `WithDebug` 的 `*bool` 模式。规律：**可选参数的"未设置"必须用类型系统表达（nullable/optional/指针），不能用值域里的哨兵值**——`debug` 的 `*bool` 三态先例就在同一个文件里，写 logPrefix 时没有类比过去。
- **修一个注入坑打开一个并发窗——修复要在原约束集合下重新验证**：首轮为规避 printf `%` 注入把 logf 拆成 print(prefix) + printf(body) 两次调用，恰好破坏了本特性要保证的并发归属（PrintStream 只保证单次调用内串行）。正确解法是先格式化 body 再单次写出——两个约束（格式串隔离 + 原子写出）同时满足。与 #169 第三轮「修统计旁路引入锁窗口回归」同构：**每次修复后要把该代码路径的全部既有约束列出来重新过一遍，而不是只验证新修的那一条**。
- **改语义必须全量 grep 文档面（llmdoc + design_doc）**：实现改成 engine-scoped 后，dag-engine.md 同一文档内出现两套相反描述（27 行进程级 vs 37 行实例级），metrics-observability / pine-cpp-runtime / design_doc 08/06 五处仍是旧语义。llmdoc 是首要事实源，残留旧语义会诱导后续实现回退。改语义时 `grep -rn` 旧关键词（如 `SetPrefix`、"进程级"）全仓清一遍，不能只改实现邻近的那一篇。
- **并发归属断言要捕获真实输出**：新增的 Java 并发测试捕获真实 stderr（双引擎并行 execute），断言每行单一前缀开头且无 doubled prefix——只断言存储的 prefix 字符串无法发现写出路径的交错。

## 第三轮：注入顺序契约矛盾（23eac48b）

增量审查（`.code-review/from-v0.10.13/increment-1-to-b37a5b1.md`）指出 1 项重要问题，属实：上一轮只更新了一处注入顺序描述，同一权威文档内出现多套互斥定义，且声明的"统一顺序"与任何运行时的真实代码都不符（Go 根本没有 `ResourceAware` 接口——资源在 execute 时经 ctx 注入；C++ 当时是 Metrics → Resource → Logger）。修复 commit：`4b9831b1`（C++ 顺序对齐 Logger → Metrics → Resource）、`23eac48b`（docs）。

### 教训

- **声明"跨运行时不变量"前先逐运行时核对代码**：文档写下的统一顺序是想象的规范而非事实——三家实际路径各不相同。正确姿势：先 grep 三运行时的注入代码确认真实顺序，能统一的统一实现（C++ 重排零行为影响），不能统一的（Go 无 Resource 接口）按运行时如实分别记录，抽出真正共同的不变量（"metadata/debug 先于 provider 注入"）。
- **同一契约在文档里只保留一份权威定义**：注入顺序在 4 处文档各自复述，改一处漏三处是结构性必然。修复后 dag-engine.md 不变量 11 是唯一完整定义，其余站点引用它。与第二轮"改语义全量 grep 文档面"同根：**复述的契约副本本身就是缺陷温床，发现时应收敛为单点定义 + 引用**。

## 第四轮：multi-pipeline 示例遗漏生产契约（1ffdb408）

用户追加需求：三运行时各写一个"多 pipeline 绑定多 endpoint、各自 log_prefix、/execute 退役为 410 tombstone"的嵌入示例（`b36f270e`）。增量审查（`.code-review/from-v0.10.13/increment-3-to-b36f270.md`）判 REQUEST_CHANGES：6 项重要问题，全部属实。修复 commit：`180bdcc8`（Go body cap）、`4043c7b3`（Java error-map + cap + exact-path + docs 命令）、`1ffdb408`（C++ MSG_NOSIGNAL + JSON 转义）。bot 复核 APPROVE。

### 教训

- **示例代码受全部生产契约约束，"演示用"不是豁免理由**：6 项问题全是既有契约在示例里的遗漏——Java 忽略 `Result.error` 返回 200（抛/返回二分契约）、Go/Java 无 body 上限（#169 共享分发层安全契约）、C++ 裸 `::write` 无 `MSG_NOSIGNAL`（conventions.md 强制约定）、C++ 手拼错误 JSON 不转义、Java 前缀路由陷阱（#169 已修过一次的坑在示例里重现）、文档命令不可运行。示例被 README 推荐为"标准嵌入模式"，读者会原样复制——**示例里的契约缺口会成倍复制到下游**。写示例前应把该语言 bundled server 的 handler 逐行过一遍，把每个防御点显式搬过来或注释说明为何不需要。
- **已修过的坑会在新代码里按原样重现**：Java `HttpServer` 最长前缀匹配陷阱在 #169 修过（`wrapHandler` 精确路径守卫），三个月后写示例时同一作者（我）在同一 API 上重蹈覆辙。修复记忆不会自动迁移到新调用点——凡用 `createContext` 必须条件反射式配 exact-path 守卫，这类"API 自带陷阱"应在写代码时 grep 仓库内既有用法照抄防御，而不是从 API 文档直觉出发。
- **文档里的命令必须逐条真实执行过**：Java 示例首版的编译命令用了不存在的 classpath（`mvn package` 不产 `target/dependency/`），审查者原样执行即失败。写"Compile & run"注释时要在干净 shell 里从仓库根逐条跑通，把真实可用的形式（`mvn dependency:build-classpath`）写进去——"看起来对"的命令等价于没有文档。
- **冒烟验证要覆盖负空间**：首版冒烟只验证了 happy path（200/410/前缀隔离），6 个问题全部藏在负空间：超大 body、子路径、算子失败、客户端断连、含引号的错误消息。修复后的冒烟补齐了这些：413、`/api/feed/sub`→404、错误 JSON 可被 `json.tool` 解析、断连 5 次后进程存活。

## 第五轮：Java 示例接入 Maven 默认构建（increment-4）

增量审查（`.code-review/from-v0.10.13/increment-4-to-5f86db4.md`）指出 1 项重要问题，属实：第四轮刚写进 standard-workflow.md 的规则"示例二进制纳入默认构建防 rot"，Java 自己没做到——C++ 示例是默认 CMake target、Go 示例被 `go test ./...` 编译，但 `pine-java/examples/` 在 Maven source tree 之外，`mvn package`/`mvn test` 都不碰它，公共 API 变化会让这个 README 推荐的示例静默腐化。

修复：`build-helper-maven-plugin` 把 `examples/` 挂为 test-source root（`add-test-source`，generate-test-sources 阶段）——默认 `mvn test-compile` 即编译示例，CI 的 `mvn test -B` 自动覆盖；test scope 保证示例类不进库 jar（已用 `unzip -l` 验证）；surefire 类名模式（`*Test`）不会把示例当测试跑。示例文档命令同步简化为 `mvn -q test-compile` + `target/test-classes` 上 classpath，逐条真实执行验证过。

### 教训

- **写下规则的那个 commit 就要让自己合规**：第四轮把"示例纳入默认构建"晋升为稳定规则时，只有 C++/Go 满足，Java 不满足——规则落笔时应立即对全部运行时逐一核对，不合规的当场修掉或明确记录为待办。否则规则文档本身成为下一轮 review 的靶子。
- **Maven 项目让 source tree 外的代码进默认构建的最小方案**：`build-helper-maven-plugin:add-test-source`——不新建 module、不动目录结构、test scope 天然隔离出 jar；比 example module（多一个 pom）或 exec/validate 阶段手工 javac（绕过增量编译与 IDE 感知）都轻。
