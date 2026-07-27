# 文档与质量检查缺口跟踪

本文件跟踪**跨任务累积**的缺口：已确认存在、但不属于任何单次任务、需要单独排期决策的文档薄弱点或质量检查空洞。由 `recorder` 维护。

与其他 memory 文档的分工：单次任务的经验教训进 `memory/reflections/`；已经定下来的取舍进 `memory/decisions/`；**尚未决定、需要跟踪的**进本文件。条目关闭时移除并在对应稳定文档留下结论。

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

### `storage_mode` 非法值兜底跨运行时分歧（issue #179）

- **现状**：已知分歧，见 issue #179，**未修**。仅在 issue 中记录，无稳定文档条目
- **待决策**：修（三方对齐兜底行为 + error fixture）还是归档为 accepted design difference（则需进 `architecture/dag-engine.md` 的接受差异段并给出理由）。在决定之前，稳定文档不得表述为已解决

### 字节级对等的校验通道覆盖面太窄

- **现状**：`scripts/cross-validate/14-byte-exact-execute.sh` 是唯一不做任何归一化的通道，但只有 5 个 fixture（`fixtures/server_byte_exact/`）；而「字节级对等」是全局契约，覆盖面与声明严重不匹配。`09-raw-byte.sh` 标题写 "no normalization"，实际在字节比较失败后回落到 `normalize_json` 再比一次，相等就打 `[W]` 并计为 pass（`09-raw-byte.sh:115-126`）；`scripts/differential-fuzz.py` 的 `normalize_json` 用 `sort_keys=True` + `round(v,10)`，key 顺序整维度与大部分数字拼写差异都不可见
- **已做**：`guides/ci-quality-baseline.md` 新增「校验通道能钉住的属性（归一化 vs 字节级）」节，写清各通道的可见性边界与那条纪律；issue #180 给 14 号通道补了 `06_number_format_regimes.json`
- **待决策**：两条路径任选或并行——(a) 继续扩 `fixtures/server_byte_exact/`，把「字节级」声明真正覆盖到主要响应形状；(b) 把 `09-raw-byte.sh` 的归一化回落改成硬失败。(b) 会立刻暴露 issue #183 的 key 顺序分歧，因此**它是 #183 的前置条件**：先决定通道方案，再修 #183，否则修完没有回归门

### issue #183：Java object key 插入顺序 vs Go 排序（已记录、已开 issue、未修）

- **现状**：Go `encoding/json` 对 map key 排序输出，pine-java Jackson 序列化 `LinkedHashMap` 保留插入顺序，两者 JSON key 顺序不一致。已在干净 master 的 worktree 上复现，确认既存且与数字格式无关，**未修**
- **为何长期不可见**：见上一条——fuzz 的 `sort_keys=True` 与 09 号通道的 `[W]` 降级都把这个维度抹掉了
- **待决策 / 前置**：修它之前必须先解决校验通道问题（上一条）。实现层已知陷阱：Go 的 sort 是 **UTF-8 字节序**，Java `String.compareTo` 是 **UTF-16 code unit 序**，对 BMP 外字符（surrogate pair）会分歧，必须显式给字节序 comparator，否则只是把分歧点从 ASCII 挪到 emoji。另外 pine-cpp 侧的 key 顺序尚未与另两方比对过（#180 期间只对了 go/java 这一对），需补三方比对
