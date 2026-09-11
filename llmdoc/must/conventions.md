# 关键约定

以下约定贯穿 Pineapple 大部分工作，应视为稳定默认行为。

## JSON 配置是 Apple DSL 与各运行时引擎之间的契约

`apple/` 中的 Apple DSL 声明流水线，但各运行时引擎仅消费符合 `pine-go/internal/config/types.go` 结构的 JSON。该 JSON 是以下场景的解耦边界：

- Apple DSL 编译
- Go 引擎加载
- Java 引擎加载
- C++ 引擎加载
- `testdata/` 中的测试数据（位于 `pine-go/testdata/`）
- 生成产物和跨语言集成测试

跨 Apple DSL / Go / Java / C++ 边界的变更应优先保持或有意演进该 JSON 契约，而非引入运行时桥接。

## 算子名称编码算子类型

内置算子采用类型前缀命名：

- `recall_*`
- `transform_*`
- `filter_*`
- `merge_*`
- `reorder_*`
- `observe_*`

该命名在多处有意义：

- 开发者从前缀即可推断运行时语义
- Apple DSL 在 `apple/flow.py` 中通过 `recall_` 前缀推断 `recall=true`
- 生成文档和 helper 类保持这些稳定名称

不要引入隐藏类型分类的算子名称。

## 注册基于副作用

算子和资源通过 `init()` 函数和公共包装器自注册：

- Go 侧算子调用 `pine.Register(...)`，资源调用 `pine.RegisterResource(...)`
- Java 侧在 `AllOperators.java` 的 static initializer 中调用 `Registry.register(...)`
- C++ 侧通过 **`PINE_REGISTER_OPERATOR_T(Type, schema)` 宏**在每个 `operators/<category>/<name>.cpp` 中 static init 注册——编译期 `OperatorTraits<T>` 解析标记位，跳过 `dynamic_cast` probe。资源 fetcher 通过 `pine::resource::register_fetcher_factory(type, factory)` 注册

Go 的 blank import 是标准的聚合机制。`pine-go/operators/all.go` 使得 `pine-go/cmd/pineapple-server/main.go` 和 `pine-go/cmd/pineapple-codegen/main.go` 等入口点可通过 import 副作用注册全部内置算子。Java 侧通过 `AllOperators.ensureRegistered()` 触发类加载。C++ 侧的内置算子被 CMake 链接进 `pine_operators` 静态库，所有可执行入口都依赖该库以触发 static init 注册。新增 C++ 算子时使用 `PINE_REGISTER_OPERATOR_T(Type, schema)` 宏。

当二进制文件或测试依赖内置算子时，先检查 blank import、`ensureRegistered()` 调用或 C++ 静态库链接。

## 版本同步跨多组文件

Pineapple 版本号在以下位置有意同步（权威清单见 `scripts/bump-version.sh` 头部注释）：

- `pine-go/version.go`（Go 常量）
- `pine-java/pom.xml`（Java Maven artifact）
- `apple/_version.py`（Apple DSL 包版本）
- `pine-cpp/include/pine/pine.hpp`（`kVersion` 常量）
- 包含 `_PINEAPPLE_VERSION` 的 JSON fixture / C++ 测试 / fuzz 脚本 / Java 示例，包括 `pipeline.json` 和 `pine-go/testdata/` 中的文件

`scripts/bump-version.sh` 是保持对齐的标准路径，已覆盖 C++ 版本常量。仅修改一侧语言常量的版本升级是不完整的。

`scripts/tag-release.sh` 是创建双 tag 的标准路径，自动校验各版本源一致后创建 `vX.Y.Z` + `pine-go/vX.Y.Z` tag 并推送。

## 生成代码必须保持最新

生成产物已提交到仓库，必须与当前 Schema 一致。关键生成输出：

- `apple_generated/`
- `doc/operators/`

CI 通过 `.github/workflows/ci.yml` 强制检查新鲜度：运行 codegen 二进制并在 `git diff --exit-code` 时失败。若变更涉及算子 Schema、codegen 模板或资源 Schema，在认为工作完成前必须重新生成产物。

Python lint 使用 `ruff`，配置位于 `pyproject.toml` 的 `[tool.ruff]`。其中 `apple_generated/` 是 codegen 产物，已通过 `extend-exclude` 排除；若生成代码不符合 lint 规则，应修复 codegen 或其输入，而不是手工修改产物。

## 现有运行时的独立 Schema 源 + CI 多层交叉验证

Go、Java 与 C++ 各自维护独立的算子 Schema 注册表（Go: `pine-go/internal/registry`；Java: `Registry`；C++: `pine::register_operator`），互不依赖。各运行时在 CI 中通过 `scripts/cross-validate.sh` 的多层交叉验证保持对齐。

### Codegen 单向对齐方向（Go 是 source of truth）

跨引擎 codegen 类改动（schema 形状、Apple DSL 产物、`apple_generated/`、`doc/operators/`）的对齐方向是**单向**的：pine-go schema 与 `pine-go/pkg/codegen` 的输出是 source of truth，pine-java / pine-cpp **单向对齐 pine-go**。改动方向不可反向——禁止"为了让 pine-java 容易对齐而在 pine-go schema 上加字段"或"让 pine-go 输出适配 pine-cpp 现有结构"等动作。

实际意义：
- 当 cross-validate `01-codegen-schema.sh`（含 1e Go-vs-Java + Go-vs-cpp doc byte-equal gate）报跨引擎 diff 时，先看 pine-go 输出，再让 pine-java/pine-cpp 改向其对齐
- pine-cpp 因无源解析能力（无类似 Go go/parser、Java JavaDoc parser），需在 `OperatorSchema.metadata` 字段中以 designated initializer 显式声明 metadata contract（CommonInput / CommonOutput / ItemInput / ItemOutput），与 pine-go / pine-java 解析自源码注释得到的字段保持等价
- 当 pine-go 缺少某种声明能力（如 metadata 字段）时，应在 pine-go 侧先确立模型再让其它运行时跟上，而非把缺口推给"为对齐而加"的反向 schema 字段

层级以 `scripts/cross-validate/` 目录下的脚本为准（codegen-schema、render-dag、execution、column-store、error、server-http、cancellation、concurrent、raw-byte、hot-reload、redis-integration、extensibility-parity、metrics-parity 等）。**禁止在文档中硬编码层数或运行时数量**——如需引用具体层级，请直接指向 `scripts/cross-validate/<NN>-*.sh`，避免引入新的硬编码失效点。

各 section 是否包含 C++ 比对取决于脚本中对 `CPP_RUN` / `CPP_DAG` / `CPP_SERVER` / `CPP_CODEGEN` 环境变量的引用；C++ 二进制由 `scripts/cross-validate/_prebuild.sh` 检测并条件性构建。

实践中意味着：

- 新增/修改算子 Schema 需各运行时同步更新
- CI schema diff gate 是对齐的最终仲裁
- 生成的 Python 类和 Markdown 文档仍为派生输出
- 任一侧可独立生成 codegen 产物（Go 从自身 Registry，Java 通过 `--schema-from-registry`，C++ 通过 `pineapple-cpp-codegen -schema-json`）
- 共享 fixture 位于仓库根 `fixtures/` 目录（三子目录：`operators/`、`pipelines/`、`errors/`），各运行时通过相对路径访问

#### 这个模式不只适用于 codegen

任何"同一条声明在三方各有一份副本"的东西都适用同一形式：定一份权威副本，其余只留指针、不复述，副本就无法漂移。

codegen 之外的第一个应用点是**接口契约文档**（issue #193）：`pine-go/pkg/metrics/metrics.go` 的 package doc（"Implementer's contract" 一节）是权威副本，pine-java 的 `metrics/Provider.java`、pine-cpp 的 `include/pine/metrics.hpp` 与 `design_doc/08_observability.md` 都只写"权威副本在哪里、请去读"，不复述内容。

**例外：正确性级别的语义允许有意冗余。** 该契约里影响正确性而非精度的那一条（Provider 的方法会被并发调用，实现必须并发安全）**刻意在三方各重复一遍**。理由是读某个语言接口的人不一定会跳去另一个语言的文件，而这条错了会产生静默 data race（出厂实现恰好都并发安全，所以下游写出竞争的 Provider 不会被本仓任何测试抓到）。

判据：单副本原则管的是**会漂移、且漂移代价有限**的描述性内容；**漂移后果是静默正确性缺陷**的语义可以有意重复，重复时在注释里写明为什么值得付这份代价。

## 跨运行时缺陷动手前必须实测受影响面

**issue 的范围描述只是症状线索，不是受影响面。** 动手改之前，先实测这个机制作用在哪些字段/路径上，用实测结果定范围。

判据的差别是视角的差别：**写 issue 的视角是「症状出现在哪个字段」，受影响面的视角是「这个机制作用在哪些字段」**，两个集合天生不同。因此范围声明系统性偏窄——**包括自己写的 issue**。

五次同型教训，只是投影方向不同：#180 把范围说小一个量级（实测 15/20 分歧、缺陷分布在两个运行时）、#183 同时说大又说小（`/execute` 上只有一个运行时错、但 `/stats` 上另一个也错）、#179 issue 写得细、实测确认后按其范围推进（做对了的一次）、#187 把范围写窄（说的是一个字段的值校验，实测是四个字段的类型规则加一个字段的值白名单）、#193 说「几条 operator 级指标的 Help 不一致、server 层很可能同型」，实测分歧数量远超它，且包含一条 issue 明确判为"已一致"的。

**#187 与 #193 两次都是自己写的 issue。** 自己写的 issue 不构成任何豁免，反而最容易带着自己的错误前提——写 issue 时的心智模型和动手时是同一个，不会有第二个人来质疑那个前提。

### 教学例子：症状 vs 机制（issue #193）

这一条纪律最干净的例证。同一个缺陷的两种描述：

- **症状**：pine-java 的 metric Help 文案比 pine-go 短。
- **机制**：三方各自独立声明 Help 字符串，而且没有任何通道能看见它。

两种修法产生**结构不同的交付物**：

| | 按症状修 | 按机制修 |
|---|---|---|
| 范围 | issue 点名的那几条 | 扫全部 `pine_*` 指标 |
| 交付物 | 一批文案改动 | 文案改动 **+ 一道检查** |
| 修完之后 | 机制仍然成立，会再漂移 | 再漂移会变红 |

关键在最后一行：机制本身（各自声明 + 无人可见）在改完那一批文案之后**依然完整存在**，所以按症状修等于什么都没修。补门那一半的判据见 `guides/ci-quality-baseline.md` 的「无人可见的属性必然腐烂」。

可执行动作：改之前对三方各跑一遍最小复现，把「这个机制的输入维度 × 取值」列成矩阵实测填表；范围与 issue 不一致时以矩阵为准并在 commit message 里写明差别。矩阵测试为什么比对读代码可靠，见 `guides/ci-quality-baseline.md` 的「穷举矩阵的测试会查出读代码查不出的缺口」。

## 审计结论只对被审的那个维度成立

**函数是审计单位，维度不是。** 一个函数可以在维度 A 上审干净、在维度 B 上从没被看过。因此审计结论要写成「函数 F 在维度 D 上已闭环」，不能写成「函数 F 已闭环」，更不能据此免掉下次触碰时的检查。

与上一条是同一族，只是范围偏窄的方向不同：上一条管范围在**字段方向**上偏窄，这条管范围在**维度方向**上偏窄。

案例（issue #189/#190）：`TransformByLua.fromLua` 里的 `(long) d` 窄化由 `81c1a36c`（2026-05-18）引入，issue #175 的三个 commit（2026-07-23）**都带着这行**——#175 修的正是同一个函数的标量派发，改动点距这行只有三行。#175 的检查项是「派发方式对不对」，这行的问题是「派发之后的类型转换对不对」：同一函数、同一屏、不同维度，那次的 grep 清单（找 `is*()` 调用）在构造上不可能命中一个强转。而那次续集反思写下的「该文件 `is*()` 派发点已三处闭环、下次触碰不需额外扫」，正是这个缺陷活下来的直接条件。

**同一函数的第三个维度（issue #200）**：#175 查「逐个值的派发谓词」、#189/#190 查「派发后的类型转换」，#200 是「**槽位跨 item 的状态**」——`toLua(String)` 与 `fromLua` 逐个值都对，但 luaj 的表槽位复用让「上一个 item 写进去的 number」决定「这个 item 的 string 读回来是什么」。既有的 `inputStringRoundTripsThroughLuaUnchanged` 用**一个** item 测 identity，从构造上看不见需要两个 item 才出现的状态耦合。判据补一条：列职责维度时，把「跨调用/跨 item 的状态」单列，凡是复用容器（VM 全局表、池化 state、`thread_local` 缓冲）都有这一维，单样本测试对它恒绿。

与「按这条表述出现在哪里清理」（`guides/investigation-to-fix-testing.md`，#183 的 `/stats` 漏两轮、#179 的五处反向注释）是**不同的失效模式，不要合并成一条**：那条是同一维度散落在多个位置，这条是同一位置承载多个维度。

可执行动作：动一个函数前先列出它承担的职责维度（派发、类型转换、错误路径、边界校验等），标明当前 issue 覆盖哪几个；写审计结论时把维度写进主语，不要留「本文件下次不需再扫」这类跨维度免检声明。

## 跨引擎对等性必须覆盖能力等价

跨引擎 parity 不仅要求"已有功能的输入输出一致"（函数等价），还要求"下游可用的集成模式一致"（能力等价）。

Pineapple 是基础设施。它的正确性不仅是 API 的输出，还包括它对构建于其上的业务施加的开发范式约束。验证维度：

- **函数等价**：已知端点、已知参数 → 相同输出（cross-validate 已覆盖）
- **能力等价**：下游能否用相同模式扩展功能（middleware 拦截自定义路径、handler 注册、回调注入）
- **负空间行为**：未注册路径、未知参数、边界条件在各引擎间表现一致
- **开发范式对等**：下游项目的典型使用方式（如通过 middleware 添加 /metrics 端点）在三引擎间可行
- **消费点追踪**：只对比"字段是否存在、是否被赋值"不够——同一特性可能在各引擎漂移成不同错误形态，要追到消费点：值存进去之后谁读、读了产生什么可观测输出。"存而不读"的死状态在存在性对比下完全隐形。

教训来源：Java PineServer 缺少根 fallback context 导致 middleware 无法拦截自定义路径，Go 侧自然支持。19 轮审计未覆盖此维度。消费点盲区案例：`log_prefix` 三家都"有这个字段"，实际是三种错误形态——Go 全局 `log.SetPrefix` first-engine-wins（一家真生效但污染全局）、Java CAS 进一个从未被读的 System property、C++ 实例成员无任何日志路径消费（两家死状态），历轮 parity 审计均未察觉（issue #172，详见 `memory/reflections/per-engine-log-prefix.md`）。

## 测试变更应遵循已有测试结构

Pineapple 的持久测试模式分层为：

1. `pine-go/internal/` 和 `pine-go/pkg/` 中运行时/配置/注册表/资源子系统的单元测试
2. 每个内置算子包的单元测试（Go / Java / C++）
3. 使用真实或仅测试用算子的 Go 引擎和集成测试
4. Apple DSL 测试，包括跨语言 JSON→Go 执行测试
5. Apple→运行时"声明→生效"端到端测试（`apple/tests/test_e2e.py`），验证 DSL 声明字段经编译 JSON 后被运行时实际消费（如 `strict_common` 传 nil → Go 引擎报错）
6. `scripts/cross-validate.sh` 多 section 跨运行时校验（具体覆盖以 `scripts/cross-validate/` 为准）

优先扩展最近的已有层，而非创建一次性测试风格。

工程质量基线默认包含 lint：

- Go 使用 `golangci-lint`，配置位于 `pine-go/.golangci.yml`
- Python（Apple DSL）使用 `ruff`，配置位于 `pyproject.toml` 的 `[tool.ruff]`
- Java 使用 `checkstyle`（自定义 `pine-java/checkstyle.xml`，4-space indent，`failOnViolation=true`），包含 `OneStatementPerLine` 规则强制每行最多一条语句
- C++ 使用 `-Werror` 严格构建作为 lint 等价（CI 中的 `cpp-lint` job）

关键输入边界应补 Go native fuzz 测试，优先覆盖 JSON/配置解析、DAG 构建等高扇出入口。C++ 端的内存与并发错误由 ASan/UBSan 与 doctest 测试套件兜底（`cpp-sanitizer` / `cpp-test` job）。

## pre-1.0 兼容性立场

Pineapple 仍处于 1.0 之前阶段，API 与行为语义可以随版本演进而调整，不承诺为了保留历史错误语义而维持向后兼容。

当任务是在修复语义性 bug 时，应优先选择正确语义，而不是继续兼容错误行为；只有任务明确要求保兼容时，才应把历史行为视为约束。

## 并发假设是刻意的

引擎构建一次后并发复用。算子初始化一次，然后为多个请求执行。任何算子实现都应假设 `Execute` 可能跨请求并发运行，除非显式同步或在 `Init()` 后不可变，否则不应依赖存储在算子结构体上的请求本地可变状态。

## Codegen 是构建时桥梁，而非运行时桥梁

Go 和 Java 各自拥有独立的 codegen 路径，均为构建时工具，不创建运行时集成路径。保持当前架构：

- Apple DSL 声明
- JSON 承载契约
- Go/Java/C++ 执行

Go 侧 `pine-go/cmd/pineapple-codegen/main.go` 从自身注册表生成；Java 侧 `Codegen.java` 支持双模式（`--schema-from-registry` 从内部 Registry 生成，`-schema <path>` 从外部 JSON 生成）；C++ 侧 `pineapple-cpp-codegen -schema-json` 从自身 Registry 导出。

## 外部 I/O 与并发安全默认值

### 跨运行时格式一致性

Go 的格式化行为是跨运行时的规范参考。Java 侧通过 `GoFormat` 工具类（`sprint`、`formatFloatF`、`formatG`）复制 Go 标准库行为。不应依赖 Java 原生 `Double.toString()` 或 `String.format("%g",...)` 的默认行为。

已统一使用 GoFormat 的算子：`TransformResourceLookup`、`TransformRedisGet`、`FilterCondition`、`ReorderShuffle`。各算子不应再自行实现 format helper（如 `formatValue`、`formatFloatG`），GoFormat 是单一事实源。

### 跨运行时 dedup key 类型标识

`merge_dedup` 算子的去重键必须使用 **type-prefixed string key** 格式（`"<type>:<canonical_value>"`），避免不同 JSON 类型产生相同字符串表示导致误判重复：

- Go: `fmt.Sprintf("%T:%v", v, v)` + 特殊处理 `-0.0 → +0.0`、composite types → `json.Marshal`
- Java: `GoFormat.sprint` 输出带类型前缀的规范化字符串
- C++: `type_tag + ":" + dump_json(v)` 对 composite types

关键边角：`true`/`1`/`1.0` 在弱类型 JSON 中有相同 string 表示，必须通过类型前缀区分。`-0.0` 必须规范化为 `+0.0`。

### 跨运行时 shuffle anyToString 一致性

`reorder_shuffle_by_salt` 算子对 salt 字段值做字符串转换时，各运行时必须遵循：

- 所有数值类型（含整数）使用 `%g` / `formatG` 格式化，而非 `%d` 或 `fmt.Sprint`
- bool 类型特殊处理（必须在数值类型判定之前）
- composite types（map/list）使用 JSON 序列化——而且必须是 **Go `json.Marshal` 的字节**：pine-java 用 `GoFormat.marshalJson`（Go 数字拼写 + UTF-8 key 排序 + `<>&` 转义），不得用裸 `new ObjectMapper()`。issue #201：Lua 返回的 `{item_score*2, item_score*3}` 在 Java 里是 `List<Double>`，裸 Jackson 写成 `[28.0,42.0]`/`2.0E100`，Go 是 `[28,42]`/`2e+100`，salt 字节不同 → hash 不同 → 整个结果顺序不同。这条规则对**任何把复合值变成字节再喂 hash / 比较**的 Java 代码都成立（bench stub `ReorderTopnBoostStub` 同步改了）；响应路径早就在用同一个 mapper，`marshalJson` 只是把它暴露给算子层，不要再长出第二份 JSON 规则
- shuffle 使用 original index 作为最终 tiebreaker，保证同 hash 值时排序确定性

### 跨运行时数值排序比较必须按 IEEE `<`/`>`，不用 `Double.compare`

`reorder_sort` 与任何按数值排序的路径，比较器语义以 Go `sort.SliceStable` + `<` 为基准：`-0.0` 与 `0.0` **相等**，由稳定排序保留输入顺序。Java `Double.compare` 把 `-0.0` 排在 `0.0` 前（也把 NaN 排到最大），是另一套全序。issue #202：三个零值恰在 `filter_paginate` 的页边界上，Go/C++ 分页拿到 `id_2`、Java 拿到 `id_20`。与上面 `merge_dedup` 的 `-0.0 → +0.0` 归一化同源（IEEE 754 负零是跨语言分歧的固定来源，见 `memory/reflections/differential-fuzz-discoveries.md`），但落点不同：dedup 是 hash 相等、sort 是比较器相等，两处各自要守。

**前提：被比较的值里没有 NaN。** `x<y?-1:(x>y?1:0)` 遇到 NaN 不是全序（NaN 与一切「相等」而其余值有序），Java `Arrays.sort(Object[])`（TimSort）可能抛 `Comparison method violates its general contract`，Go `sort.SliceStable` 只是顺序未定义不 panic。`reorder_sort` 当前安全是因为 frame 写入校验拒绝标量 NaN/Inf（`ColumnFrame.checkValue` / `row_frame.go` 同款）且 JSON 表达不出 NaN；把这条规则套到一条 NaN 可达的路径（例如直接对 Lua 中间值排序）之前，先决定 NaN 的位置并与 Go 做相同处理，不能直接套 `<`/`>`。

### 有界读取

读取外部响应时必须使用 `io.LimitReader(body, limit+1)`，禁止裸 `io.ReadAll`。读取后若 `len(data) > limit` 则视为溢出错误。`max_response_size` 类参数的默认值为 10MB。

### 全局副作用保护

进程级 side effect 在热加载场景下可能被多次触发，需用 `sync.Once` 类机制保证仅执行一次。但先问一步：这个状态真的属于进程吗？嵌入 API 使多引擎/多实例成为一等公民后，"进程级 + Once"对实例级状态意味着 first-engine-wins 静默缺陷——`log_prefix` 曾用 `log.SetPrefix()` + 包级 `sync.Once`，issue #172 已改为引擎实例级 logger。归属判断见 `guides/standard-workflow.md` 的"新公开入口上线后，审计既有全局状态的归属"。

### Goroutine 生命周期

后台 goroutine 必须接受 `context.Context`，并在 `select` 中监听 `ctx.Done()` 以实现干净的取消传播。禁止依赖永不退出的 goroutine 存活模式。C++ 端等价约束：HTTP server 的 `Server::stop()` 在关闭监听后等待 `in_flight_` 归零（5s 超时）后返回；`resource::Manager::stop()` 通过 `stopping_` + condition variable 解除阻塞、join 所有刷新线程。

### pine-cpp 网络客户端必须 MSG_NOSIGNAL

任何 pine-cpp 中走 raw socket 的 `send()` / `write()` 调用必须使用 `MSG_NOSIGNAL` flag（或等价的 `SO_NOSIGPIPE`），避免在远端中段断连时进程级 SIGPIPE 直接终止 server。

约束适用范围：

- HTTP server 的写响应路径（`pine-cpp/src/server/server.cpp`）
- Redis client 的 `send_command`（`pine-cpp/src/runtime/redis_client.cpp`）
- 未来任何新增的 raw socket / TCP / Unix domain socket 写路径

历史教训：HTTP server 早就用 `MSG_NOSIGNAL`，Redis client 0.10.10 之前漏了——AUTH/SELECT 失败收敛测试 fixture 借助 accept-then-close 行为意外暴露此 bug。新增 socket 写路径请把 `MSG_NOSIGNAL` 与失败收敛（close fd / `connected()==false`）一并 review。

## 禁止硬编码定量描述

下列定量描述容易在版本演进中失效，必须通过引用脚本/表格而非数字字面量来表达：

- 跨运行时数量（"三运行时"、"四运行时"）→ 改为"各运行时"或显式列出
- cross-validate 层数 → 直接引用 `scripts/cross-validate/` 目录
- CI job 数量 → 引用 `.github/workflows/ci.yml` 而非硬编码
- 用户可见文档（`README.md` / `doc/guide_*.md`）中的性能倍数、百分比、毫秒绝对值 → 只写定性判据 + 指向可复现的 benchmark 入口，见 `memory/decisions/user-docs-no-perf-multipliers.md`

定量数字若必须出现，应放在有维护责任人的表格内并与代码处于同一文档目录，便于一同更新。

## http_metrics middleware 必须 default-on

各运行时（pine-go / pine-java / pine-cpp）的 HTTP server 必须**无条件**注入 `http_metrics_middleware`（含 `HttpStats` 累加器作为第二写入路径），不得要求用户显式 opt-in。`metrics_provider` 为 null 时自动 tie-off 至 `NopProvider`，middleware 链与外部观测语义保持各方字节一致。

理由：R2 后续审计（2026-05-23）发现 pine-java 此前是 conditional 接入（彼时还有已下线的 pine-python 完全缺失），与 R2 时 pine-cpp 的 conditional 状态同型。本约定收口"各方装配条件统一"，由 Section 13 schema shape 检查长期监管。

## InputFieldSpec 三态模型（默认 Nullable 字段模式）

各运行时的 `InputFieldSpec`（或等价的字段访问策略）遵循三态模型，控制算子输入构建时对字段缺失/nil 的处理行为：

- **Nullable**（默认）：字段缺失 → 返回 error（字段必须存在于 frame 中）；字段存在但值为 nil → 透传 nil 给算子。这是所有字段的默认行为。
- **Strict**：字段缺失或值为 nil → 均返回 error。通过 JSON 配置中的 `strict_common` / `strict_item` 字段列表 opt-in。
- **Defaulted**：字段缺失或值为 nil → 替换为默认值。通过 `common_defaults` / `item_defaults` 配置（不变）。

该模型的核心变更是默认行为从 Strict 改为 Nullable：算子在未声明 strict 的情况下，收到 nil 值时不再报错，而是将 nil 透传。这使得"字段存在但值为空"成为合法的业务语义，减少了不必要的运行时错误。

各运行时的 strict opt-in 字段在 JSON 配置中的位置：
- `strict_common`：列在此列表中的 common 字段走 Strict 模式
- `strict_item`：列在此列表中的 item 字段走 Strict 模式

Apple DSL 侧通过 `_add_op` 或动态分发的 `strict_common=["field"]` / `strict_item=["field"]` kwargs 声明 Strict 模式。编译器将其作为 `OpCall.strict_common` / `OpCall.strict_item` 存储，并在步骤 4 以 `"strict_common"` / `"strict_item"` JSON 键输出。`unique_name()` 的语义元组包含这两个字段，因此 Strict 声明会影响自动生成的算子名。

## Operator-level debug 三态继承

各运行时的逐算子 `debug` 配置采用 nullable 三态语义：

- Go: `*bool`
- Java: `Boolean`（装箱类型）
- C++: `std::optional<bool>`

未设置时（nil/null/nullopt），算子继承 flow-level 全局 debug 设置；显式设置 `true` 或 `false` 时，覆盖全局值。这替代了旧版"全局 debug 单向传播覆写所有算子"的行为，使单个算子可以在全局 debug 开启时显式关闭自身的 debug，或在全局 debug 关闭时单独开启。

## ExecutionError/PanicError 必须保留 cause chain

各运行时的 `ExecutionError` / `PanicError` 在包装内层异常时，必须保留 inner exception 的对象引用以支持沿链下钻：

- pine-go: `Err error` 字段 + `Unwrap() error`，通过 `errors.Is/As` 解包
- pine-java: 构造时 `super(msg, cause)`，通过 `Throwable.getCause()` + `instanceof` 解包
- pine-cpp: 多继承 `std::nested_exception` + `std::throw_with_nested` 重抛，通过 `pine::error_as<T>()` helper 走链（注意 helper 须先检查 `nested_ptr() != nullptr`，避开标准 `std::rethrow_if_nested` 的 footgun）

理由：cause chain 属于"语言层 API 形态对等"的一部分，虽然不在 HTTP /execute 返回 JSON 中可见（已被 string flatten），但下游用户对该能力有预期。Section 15（`15-error-cause-chain.sh`）通过三方 probe binary stdout 字节级一致验证（`PASS:key=user:42 not found`）。

## 错误类型分类约定(ConfigError / ValidationError / RegistryError)

各运行时在 init / build / dispatch 阶段抛出的错误类型必须按以下边界统一:

- **ConfigError**:配置文件结构错(缺字段、空 operators、JSON parse、name 冲突)。`pine: config error: ...` 前缀。
- **ValidationError**:语义错(`data_parallel < 0`、`skip` 字段非 `_` 开头、`skip` 字段未出现在 `common_input`、forward source reference、resource_name 引用未注册资源)。`pine: validation error: ...` 前缀。
- **RegistryError**:算子注册系统错(未知算子类型、参数 schema 校验失败、Init 失败 wrap、参数语义违反如 `top_n` 必须为数字 / `unsupported order`)。`pine: registry error [<op>]: ...` 前缀。

**Section 5 (error-parity) 不强制 enforce type 字段**(只用 `message_contains` 子串匹配),所以历史上各运行时分类略有 ad-hoc。本约定的目的是给未来贡献者一个边界判断标准,避免再扩大漂移。reviewer P1-O1 审计现状大致已符合此分类(C++/Java 都把 init-time wrap 走 RegistryError,跟 Go `BuildOperator` 一致;ValidationError 用于明确"语义不变量")。

边角案例 — `additive_writes_row_set + mutates_row_set` 冲突:Go 端原是 `fmt.Errorf` plain error;C++/Java 当前用 RegistryError。后续如需统一,选 ValidationError(语义边界冲突)。

## 模板参数 `{{field}}` 错误前缀字节级对等（issue #74）

`ParamSpec.Templatable`（详见 [apple-compiler.md](../architecture/apple-compiler.md) 与 [operator-contract.md](../reference/operator-contract.md)）的模板解析在 build / runtime 两阶段产出的错误，三引擎前缀与文案必须**字节级一致**（CLI stderr 与 `/execute` HTTP 错误对等）：

- **Build-time（template plan 构建）**：非 bare marker、参数非 string、字段未在 common frame 中可见等形状违规 → `ConfigError`，前缀 `pine: config error: operator "X": param "Y" value "Z" must be a bare {{field}} marker`（或对应文案）。pine-go 此前 `BuildTemplatedParamPlan` 漏包装 `&types.ConfigError{}`，运行 CLI 时呈现为 `error creating engine: operator "X": ...` 缺前缀，已修复对齐 C++/Java。
- **Runtime（请求级 resolver）**：模板字段缺失、字段值无法 coerce 到 string → `ExecutionError`，前缀 `pine: execution error in operator "X": <inner>`。pine-cpp 此前 `parallel_execute` 在 `resolve_templated_params` 抛出后未做 op-name re-wrap，已修复为 `try { ... } catch (const ExecutionError& e) { throw ExecutionError(op.name, e.inner()); }`。

cross-validate `scripts/cross-validate/17-templated-params.sh` 通过 stderr byte-exact probe 钉死本契约。新增 `Templatable=true` 参数或修改模板解析路径时，请在该 section 增加对应 probe。

Java 侧 probe 注意：SLF4J no-op binder 会向 stderr 写若干提示行（`SLF4J: ...`），probe 脚本对 Java case 使用 `grep -v '^SLF4J:'` 过滤后再做 byte-exact 比对。
