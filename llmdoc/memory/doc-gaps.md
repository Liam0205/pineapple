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

## 已关闭条目

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
