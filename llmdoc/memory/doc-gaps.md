# 文档与质量检查缺口跟踪

本文件跟踪**跨任务累积**的缺口：已确认存在、但不属于任何单次任务、需要单独排期决策的文档薄弱点或质量检查空洞。由 `recorder` 维护。

与其他 memory 文档的分工：单次任务的经验教训进 `memory/reflections/`；已经定下来的取舍进 `memory/decisions/`；**尚未决定、需要跟踪的**进本文件。

条目关闭时把结论落到对应稳定文档，本文件只留一条指向该文档的短条目（放「已关闭条目」节），不保留原来的现状描述。

## 开放条目

### clang-format 没有 CI job（缺少 CI 检查）

- **现状**：`grep -rn "clang-format\|fmt-check" .github/workflows/` 零命中。`make fmt-check` 里的 clang-format 检查在 CI 里没有任何对应 job；`cpp-lint` job 只做 `-Werror` 严格构建 + trailing whitespace/tab/结尾换行卫生 + 相邻字面量拼接排查。C++ 格式当前只由本地 `pre-commit` hook 守着（staged 文件粒度、可用 `--no-verify` 绕过），且本机默认未安装 clang-format 时 `make all` 会在 `fmt-check` 处 Error 127 中止
- **已做**：`guides/ci-quality-baseline.md` 的 C++ lint 节已改成准确表述（此前把 clang-format 写在 CI `cpp-lint` 描述旁边，读起来像有 CI 覆盖）
- **待决策**：是否给 CI 加 fmt-check job。需要一并回答：CI runner 上 clang-format 版本如何锁定（版本差异会产生格式漂移假失败）、是否要求本机安装成为开发前置条件（否则 `make all` 仍会 127 中止）
- **历史**：`memory/reflections/redis-resourcemanager-migration-and-pine-python-removal.md` 记过"clang-format commit 阶段无 gate"，issue #122/#160（2026-07-25）进一步确认 CI 阶段也无 gate

### `projectMap` 空列表语义在 fixture 编写视角没有落点

- **现状**：空 `item_output` = 空输出、不回退成"返回全部字段"——这条语义只记在 `memory/reflections/fix-output-projection-semantics.md`（修复视角）。写 benchmark fixture 或搬 microbench 形状的人不会去读那篇，因此这个陷阱会重复踩（issue #160 就踩了一次）
- **已做**：`guides/benchmark-hygiene.md` 补了"搬 microbench 形状要重查投影/序列化段"，覆盖了 benchmark 场景
- **待决策**：是否在 `reference/` 层给 `flow_contract` / `item_output` 投影语义一个独立的契约条目，使非 benchmark 场景（写 cross-validate fixture、写 fuzz 生成器）也能检索到。issue #175 的 fuzzer flow_contract 投影盲区是同一语义的第三次现身，倾向于值得做

### 审计 scratch 副本的磁盘成本没有归属

- **现状**：`close-local-code-review` 工作流每轮 blind review 都指示 reviewer `cp -a` 一份仓库快照到自己的 scratch 目录（因为外部清理进程会删原快照）。单份副本 1–1.7GB，**创建有明确指令、销毁没有归属**，所以副本只累积不清理。issue #187/#188 跑测试时 `Disk quota exceeded`，清掉 #180/#183/#179 三轮遗留的 17GB 才跑得动
- **待决策**：清理责任放哪一层。两个候选：(a) 每轮审计闭环后由 reviewer 清理自己那轮的副本；(b) 任务结束时由主 agent 统一清一次。(a) 更及时但要改 reviewer 指令且每轮都可能漏，(b) 更容易保证执行但审计跨度内配额仍可能被打爆
- **判据**：这不是偶发事故——N 轮审计之后必然打爆配额，只是 N 多大取决于配额（#183 单独跑了 14 轮、#179 跑了 6 轮）。属工作流运维成本，需要一个明确的清理触发点写进审计工作流文档
- **约束**：清理是删除操作，执行前需要用户确认具体路径

### 根级配置的五处残留分歧：`debug`、重复键、键名大小写、多字段报错顺序、嵌套类型 vs 值错误层级（issue #187 审计发现）

- **现状**：#187 统一了四个根级**字符串**字段的类型处理，但审计查出五处仍不一致，都实测过。
  **其中重复键、键名大小写、多字段报错顺序这三条是 #187 新引入的可见差异**（嵌套层级那条同理由校验的存在而可见，但它是 pine-cpp 检查位置的问题、不是解析器差异；`debug` 则是修前状态原样保留）：校验之前没人看类型，所以「取哪个重复值 / 键名怎么拼 /
  先报哪个字段」都无后果；加了校验之后，校验结果依赖于这些解析细节。
  - **`debug`**（唯一的另一个根级标量，布尔）：pine-go 拒绝错误类型；**pine-java 的 `asBoolean()`
    强转**——`1` 与 `"true"` 会真的把 debug 打开，`"yes"` / `[1]` 强转成 false；pine-cpp 的
    `is_bool()` 守卫静默忽略、保持关闭。**三方三种行为**。这条是修前状态原样保留，不是新引入的。
  - **重复键**：同一键出现两次时 pine-go 与 pine-java 取后者、pine-cpp 的 `FlatMap` 取前者。
    `{"log_prefix":"ok","log_prefix":123}` 被前两方拒绝、被 pine-cpp 接受。实际风险低（无生成器
    产出重复键，`apple/compiler.py` 用 dict），但确实是「同一份配置一方通过、另一方被拒」。
  - **键名大小写**：pine-go 的 `encoding/json` 精确匹配失败后会忽略大小写回退匹配 struct tag，
    另两方精确匹配。`{"STORAGE_MODE":"colunm"}` 被 pine-go 拒绝、被另两方接受。
  - **嵌套类型错误 vs 值错误的层级**：`storage_mode` 非法**且**某个嵌套字段类型错（如
    `$metadata.common_input` 给了字符串而非数组）时，pine-go 报**嵌套类型错**，
    pine-cpp 报 **`storage_mode` 值错**（实测，8 个嵌套字段同型，含
    `pipeline_group.main.pipeline`、`sources`、`flow_contract.common_input`、`data_parallel`）。
    根因是 pine-go 的 `encoding/json` 在**任意深度**的类型错上就让整份 unmarshal 失败，
    而 pine-cpp 的 `validate_storage_mode` 只排在**四个根级**类型检查之后、嵌套解析之前。
    **pine-java 落在中间**，分界是「抛错 vs 强转」而不是「叶子 vs 容器」：`readStringList` 读的
    数组字段会抛错、于是与 pine-go 一致；而 `asText()` / `asBoolean()` 读的 8 个标量叶子
    （`type_name` / `recall` / `debug` / `consumes_row_set` / `mutates_row_set` /
    `additive_writes_row_set` / `for_branch_control` / `skip`）以及 `.fields()` 读的容器字段
    都静默强转、于是和 pine-cpp 一样报值错。逐字段的断言见 `StorageModeValidationTest.nestedTypeErrorPrecedenceDependsOnWhetherTheReadThrows`（`architecture/dag-engine.md` 只留指针，不复述机制）。
    **不打算靠移动调用点修**：审计实测过把它挪到 `apply_registry_traits` 前——能修好这 8 个嵌套
    场景，但会重新打破「算子错误不得抢先」那条（`colunm` + 缺 `type_name` 又变成 cpp 报算子错）。
    两个约束无法靠移动一行同时满足，要修得把 pine-cpp 的类型校验与值校验拆成两遍
    （pass 1 全树只查类型、pass 2 值白名单、pass 3 语义），代价是重构 `load_config_from_json`
    与 `parse_operator` 的抛错分层。
  - **多字段报错顺序**：两个以上根级字段同时类型错时，报错点名哪个字段可观察。pine-java 与
    pine-cpp 已对齐（同一检查顺序，两侧单测按相邻对钉住全部四个位置），但 **pine-go 无法用任何
    固定顺序匹配**——`encoding/json` 点名的是**在 JSON 文档里最先出现**的那个错误字段，答案随
    输入键序变化（实测：同样三个坏字段、两种键序，pine-go 分别点名 `log_prefix` 与 `storage_mode`）。
    这也是它不能做成 cross-validate error fixture 的原因：section 05 要求三方匹配同一子串。
- **待决策**：
  - (a) `debug` 是否按同一规则对齐——改动小，但与 #187 一样属用户可见契约变更。
  - (b) 重复键的解析语义是否统一——需要动 pine-cpp 的 `FlatMap` 插入语义或在解析层显式拒绝重复键，
    影响面比 (a) 大得多，且 JSON 规范本身对重复键未作规定。
  - (c) 键名大小写是否统一——要么让另两方也做大小写不敏感回退（跟随 pine-go），要么在 pine-go 侧
    显式拒绝非精确匹配的键。后者更接近「严格 schema」的方向，但 `encoding/json` 没有开关，
    需要自己先解析成 `map[string]json.RawMessage` 再校验键名。
  - (d) 多字段报错顺序中 pine-go 的输入序依赖**不打算修**：要改就得放弃 struct unmarshal、
    改成手工遍历，代价远超收益。建议长期就按「java==cpp 有契约、pine-go 无」记账，而不是列为待办。
- **不在 #187 范围内**：#187 的授权范围是「统一所有根级**字符串**字段的类型处理」，这五条都超出。
  最后一条（嵌套层级）由用户在审计第六轮明确决定记录而不修，理由是两遍解析的重构代价超出本 range。


### `Codegen.toPythonLiteral` 与 Go `pythonLiteral` 的数字分派不同轴（issue #189/#190 顺带发现）

- **现状**：修 `fromLua` 的窄化后，我按「数据路径上按值域选表示」这个模式反查了全仓，命中一处同型：
  - `pine-java/.../Codegen.java:375`：`if (d == (long) d && !Double.isInfinite(d)) return Long.toString((long) d);`
    —— 按**值**判断（无 2^53 上界），与被修掉的 `fromLua` 是同一形状。
  - `pine-go/pkg/codegen/template.go:66-69`：按**静态 Go 类型**分派——`case float64: "%g"`、`case int64: "%d"`。
- **因此分歧在原理上成立**：一个整数值的 `float64` 默认值，Go 出 `1e+16`（`%g`），Java 出 `10000000000000000`。
  实测 Go 侧 `%g` 对 42 出 `42`、对 1e16 出 `1e+16`、对 2^62 出 `4.611686018427388e+18`。
- **但当前不可达**：没有任何算子 spec 带足够大的整数值浮点默认值，`make codegen-check` 干净，
  `apple_generated/` 与 `doc/operators/` 里也搜不到 `1e+16` / `1e+20` 一类字面量。所以这是**潜在缺口而不是现存缺陷**，
  没有并进 #189/#190 的修复范围。
- **待决策**：(a) 是否把 Java 侧改成与 Go 同轴（按静态类型分派）——改动小，但需要先确认 Java 侧拿到的是什么静态类型，
  Jackson 解出的 JSON number 在 Java 里没有 Go 那种 float64/int64 之分，所以"同轴"未必可直接平移；
  (b) 是否给 codegen 加一条带大整数浮点默认值的 spec 作为回归门——这会先变红，需要 (a) 一起做。
- **不在 #189/#190 范围内**：那两条是 `/execute` 响应体的字节契约；codegen 输出是另一条通道。

### Lua 侧负零在三方的表现不一致（issue #189/#190 审计发现，先于本 range 存在）

- **现状**：审计在验证 #189/#190 的修复时顺带实测到，`-0` 这个值三方不一致，且**与本次改动无关**——
  在 base `a9830fca` 上 `GoFormat.formatJsonNumber(-0.0)` 同样返回 `-0`。
- **机制**：luaj 在 `fromLua` 拿到值之前就把 `-0.0` 折叠成 bit 全零的 `LuaInteger`，所以 pine-java 的
  负零分支根本没有机会执行；pine-cpp 走 `lua_tonumber` 保留负零。
- **Go 自己也不自洽**：审计实测 Go 在单独输出时给 `-0`，而当某个 `0` 键排在前面时给 `0`——
  所以这不是「另两方跟随 Go」就能解决的，**基准侧本身需要先定义清楚**。
- **待决策**：(a) 是否值得统一。负零在推荐场景里几乎不出现，且要动 luaj 的值折叠（或在 Lua 边界显式
  区分 `-0.0`），代价与收益不成比例；(b) 若要统一，先定 Go 侧的确定性行为，再谈另两方跟随。
- **不在 #189/#190 范围内**：那两条是「整数值 double 的拼写」；负零是符号位问题，且先于本 range 存在。

### 已解决：`transform_size` 计数值 ≥ 1e6 的跨运行时分歧（issue #189/#190，PR 审查后彻底修掉）

- **曾经的结论是「无法同时满足、接受回归」，这个结论是错的**。我原本认为 pine-java 无法同时在
  `filter_condition` 与 `transform_size` 模板参数两条路径上与 Go 一致，理由是 Java 的装箱类型追踪
  「值从哪里解析来的」而不是静态类型。据此我把装箱类型分支从 `GoFormat.sprint` 整个删掉、保住
  `filter_condition`，把计数路径记为「本 range 引入、已接受」。
- **PR #192 的审查机器人拒绝了这个取舍**，理由是 Redis 路径的数据正确性影响太重，不能固化为
  accepted regression，并指出正确做法是**在比较点处理装箱对等**而不是改共享格式化器。这是对的。
- **实际修法**：`GoFormat.sprint` 恢复整数装箱分支（Go 的 `%v` 对原生 `int` 任意量级都原样打印，
  `transform_size` 写的 `in.ItemCount()` 就是原生 int，所以 Redis 键、Redis 成员值、模板参数三个
  消费者都需要这个语义）；而 `FilterCondition` **自己把比较两侧都归一到 double**——Go 那边两侧都
  经 `encoding/json` 成了 float64，所以不对称本就属于比较点、不属于格式化器。
- **结果：两条路径同时与 Go 一致**，四个 `sprint` 消费者（模板参数、`filter_condition`、Redis 键、
  Redis 成员值）全部不再分歧。两侧各有 mutation 验证的门：去掉 `FilterCondition` 的归一化会重现原
  分歧，去掉 `sprint` 的装箱分支会让 `sprintPreservesIntegralBoxSpellingLikeGoDoes` 变红。
- **教训**：我把「共享格式化器的输出」当成了唯一可调的旋钮，于是得出「必须二选一」。真正的自由度在
  **比较点是否自己归一化**。声称「两个约束无法同时满足」之前，先确认约束真的作用在同一个地方。

### `cross_storage_diverge` 计数器只增不报（issue #189/#190 审计第五轮顺带发现）

- **现状**：`scripts/differential-fuzz.py:1810` 在检测到「同一配置在 row 与 column 存储下输出不同」时
  递增 `stats["cross_storage_diverge"]`，但这个计数器**从不出现在结尾摘要里**、**不进 `failed_rounds`**、
  **不影响退出码**。也就是说一次跨存储分歧只会在 stdout 里闪过一行，nightly 依然报绿。
- **为什么值得记**：这与 #189/#190 是同一族问题——**检测到了却没有让任何门变红**。本次刚给 fuzz 加了
  `REPRODUCE:` 行来解决「失败无法复现」，而这一条是「失败根本不算失败」。
- **待决策**：(a) 把 `cross_storage_diverge` 计入 `failed_rounds` 并写进摘要（最直接，但需先确认历史上
  是否有长期存在的跨存储分歧——若有，加门会立刻让 nightly 变红，那本身是需要单独排期的信息）；
  (b) 只打进摘要、暂不影响退出码，先观察若干轮 nightly 再决定是否升级为门。
- **不在 #189/#190 范围内**：那两条是 go-vs-java 的数字拼写；这条是同一运行时内 row-vs-column 的
  报告机制，且先于本 range 存在（`git log -S` 可查）。

### 已解决：histogram 桶边界跨运行时无检查（issue #193）

- **当时的判断**：`Help` 补了检查、桶边界没有，而桶的后果更重——桶只是给下游后端的建议值，但**每个运行时
  建议得不一样，会让下游在三方之间算出的分位数不可比**。
- **已就地解决**：`scripts/check-metrics-help-parity.py` 同时比对桶数组，接入 `make lint`。
  边际成本接近零（同一次纯文本扫描），所以没有理由留成开放条目。
- **实现取舍**：比对的是「某个运行时声明了哪一批桶数组」这个集合，而不是把每个数组映射回 metric 名——
  三种语言的声明写法差异足以让名字关联变脆，而集合比对同样能抓到漂移，且不会静默错配。
  只在两侧各有对方没有的数组时报错，避免把「某运行时不埋这个 histogram」误判成分歧。
- **两侧都用 mutation 验证过**：改掉 pine-java 一个桶边界会变红并同时打印两侧数组，恢复后变绿。

## 已关闭条目

### issue #193：`metrics.Provider` 契约定义与 metric `Help` 文案（已解决）

- **结论**：已修，issue #193 / commit `67890029`。契约权威单副本落 `pine-go/pkg/metrics/metrics.go` 的 package doc，pine-java / pine-cpp 接口注释与 `design_doc/08_observability.md` 只留指针（并发那条刻意三方各重复一遍，理由见 `must/conventions.md` 的「这个模式不只适用于 codegen」）；`Help` 文案全部对齐 pine-go 并由 `scripts/check-metrics-help-parity.py` 接 `make lint` 守着；一处失效文档断言已改为陈述事实。能力边界落 `reference/metrics-observability.md`、两条纪律落 `guides/ci-quality-baseline.md` 与 `guides/investigation-to-fix-testing.md`。桶边界那半随后由同一脚本一并守住（按 metric 名比对，见上面「已解决」条目）

### issue #187：运行时层 fail-fast 拒绝非法 `storage_mode`（已解决）

- **结论**：已修，issue #187 / commit `b0dee3bb`。三方在**配置加载层**一律拒绝非法 `storage_mode`（只接受 `"row"` / `"column"` / 空 / 缺省），错误文案字节相同；值白名单刻意放在 config 校验层而非 frame factory，使 #179 的 dispatch 规则保持不变。规则、三层解释点、Go 白名单常量重复定义的原因都已落 `architecture/dag-engine.md` 的 `storage_mode` 节
- **实际范围比本条目描述更宽**：条目的决策输入 (a) 预见到「不只是加值白名单、还要先统一类型处理」，本次两半一起做了。类型层规则（present 但类型错 → 拒绝、`null`/缺省 → 默认值）适用于**全部四个根级字符串字段**而不只是 `storage_mode`，另立 `reference/root-config-string-fields.md` 承载
- **用户可见契约已同步**：`doc/guide_pipeline{,-en}.md` 原先写「手写 JSON 的非法值被三方静默接受并落行存」，已改为拒绝，旧行为保留一小段标注为 #187 之前的历史
- **过程记录**：`memory/reflections/config-validation-and-byte-exact-coverage-187-188.md`

### issue #188：字节级对等的校验通道覆盖面太窄（已解决）

- **结论**：(b) 半由 issue #183 完成（删掉 `09-raw-byte.sh` 的归一化回落，14 号不再是唯一无归一化通道）；(a) 半由 issue #188 完成——`fixtures/server_byte_exact/` 按响应形状事先枚举扩充（覆盖面以目录 `ls` 与 `scripts/cross-validate/14-byte-exact-execute.sh` 的头部注释为准，**不在此复述数量**），头注同时写清这条通道结构性不可能覆盖什么（含 `trace` 或来自 `/stats` 的响应永远不字节稳定）
- **顺带沉淀**：纪律「fixture 按响应形状枚举、不等出事才补」与「穷举矩阵查出读代码查不出的缺口」进了 `guides/ci-quality-baseline.md`。按形状枚举的第一个 fixture 就查出 pine-cpp 校验错误 envelope 的既存分歧（`{"common":{},"items":[]}` vs `null`），已一并修
- **过程记录**：`memory/reflections/config-validation-and-byte-exact-coverage-187-188.md`

### issue #179：`storage_mode` 非法值兜底跨运行时分歧（已解决）

- **结论**：已修，commit `90982071`。三方**分派**统一为「只有字面量 `"column"` 精确匹配才走列存、其余一切落行存」，以 pine-go `NewFrame` 的 `switch` + `default: newRowFrame` 为基准。规则与三处分派点已落 `architecture/dag-engine.md` 的 `storage_mode` 节
- **当时刻意保留静默兜底、不做 fail-fast** 的理由（只改两侧就是新分歧、三方同改属独立决策）已由 issue #187 处理掉；分派规则本身没变，非法值现在在加载期就被拒绝、不可达。见上一条已关闭条目
- **顺带沉淀**：这个属性的外部可观察面为空（行列存输出对等把差别吸收掉、`/stats` 与 `/dag` 无 storage 字段），门只能放在各运行时 factory 单测；两条相关纪律进了 `guides/ci-quality-baseline.md`，「既存断言与参考实现相反时先定基准」与「修错误注释按声明出现位置清理」进了 `guides/investigation-to-fix-testing.md`
- **过程记录**：`memory/reflections/storage-mode-dispatch-parity-179.md`

### issue #183：Java object key 插入顺序 vs Go 排序（已解决）

- **结论**：已修，commit `3d92e968`（pine-java 实现）+ `c1ae534c`（校验通道）。规则已落 `reference/json-key-order-parity.md`：Go 对 map 排序、对 struct 保持声明顺序，Java 侧用 `GoFormat.SortedByUtf8` 显式建模这条二分；排序键是 UTF-8 字节序（`GoFormat.compareUtf8`），Jackson `ORDER_MAP_ENTRIES_BY_KEYS` 与任何 `TreeMap` 写法都不能用
- **原条目里那个未知项已查清，而且答案是两半**：pine-cpp 侧当时「尚未比对过」，本次补了三方比对。走 `Variant` writer 的响应（`/execute`，含 BMP 外 key）**本来就对**——`json_writer.cpp` 的 `std::sort` 配 `std::string` 的 `<` 即字节序。但 `/stats` 是**手写拼接 JSON、不走 writer**，顶层、`server` 与 `operators` 三处都按书写顺序输出，本次一并修了。教训：**「某个运行时天然满足」只对具体代码路径成立，不对整个运行时成立**；`operators` 那处是加了校验检查之后才暴露的，而且第一版检查用的 fixture 算子名恰好已是字典序，所以连新加的检查都漏了它一轮
- **过程记录**：`memory/reflections/json-key-order-parity-183.md`
