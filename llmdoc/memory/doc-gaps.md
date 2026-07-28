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

### `storage_mode` 非法值兜底跨运行时分歧（issue #179）

- **现状**：已知分歧，见 issue #179，**未修**。仅在 issue 中记录，无稳定文档条目
- **待决策**：修（三方对齐兜底行为 + error fixture）还是归档为 accepted design difference（则需进 `architecture/dag-engine.md` 的接受差异段并给出理由）。在决定之前，稳定文档不得表述为已解决

### 字节级对等的校验通道覆盖面太窄（(b) 已完成，(a) 仍开放）

- **现状**：`scripts/cross-validate/14-byte-exact-execute.sh` 有 7 个 fixture（#180 加了 `06_number_format_regimes`，#183 加了 `07_non_bmp_keys` 与 `08_html_chars_in_keys`）。**数量以 `ls` 为准，不在文档里复述**——本行的数字已经过期两次（`fixtures/server_byte_exact/`），而「字节级对等」是全局契约，覆盖面与声明仍然不匹配。09 号通道的归一化回落已删（见「已做」），所以 14 号不再是唯一一条无归一化通道，但 fixture 数量这一半问题没动
- **已做**：`guides/ci-quality-baseline.md` 有「校验通道能钉住的属性（归一化 vs 字节级）」节写清各通道可见性边界与那条纪律；issue #180 给 14 号通道补了 `06_number_format_regimes.json`；**(b) 已完成**——issue #183 删掉了 `09-raw-byte.sh` 的归一化回落（原先字节比较失败后回落 `normalize_json`、相等打 `[W]` 计 pass），现在字节不同即硬失败，仅 `strict_order: false` 的 fixture 仍走 set 归一化；同期 `scripts/differential-fuzz.py` 新增 `key_order_signature()`，key 顺序不再被 `normalize_json` 的 `sort_keys=True` 抹掉
- **待决策**：(a) 继续扩 `fixtures/server_byte_exact/`，把「字节级」声明真正覆盖到主要响应形状。issue #183 没有新增 fixture，增强全部落在 09 号通道与 fuzz 上，所以这一半仍然开放。数字拼写在 `normalize_json` 下的可见性边界（`round(v,10)` + int/float 类型分裂）未变，仍需字节通道兜住。注：#183 已加 `07_non_bmp_keys.json`（BMP 外 key，钉住 `writeValueAsBytes` 的代理对转义与 UTF-8 比较器两处），但覆盖面仍远小于「字节级对等」这个全局声明，条目保持开放

## 已关闭条目

### issue #183：Java object key 插入顺序 vs Go 排序（已解决）

- **结论**：已修，commit `3d92e968`（pine-java 实现）+ `c1ae534c`（校验通道）。规则已落 `reference/json-key-order-parity.md`：Go 对 map 排序、对 struct 保持声明顺序，Java 侧用 `GoFormat.SortedByUtf8` 显式建模这条二分；排序键是 UTF-8 字节序（`GoFormat.compareUtf8`），Jackson `ORDER_MAP_ENTRIES_BY_KEYS` 与任何 `TreeMap` 写法都不能用
- **原条目里那个未知项已查清，而且答案是两半**：pine-cpp 侧当时「尚未比对过」，本次补了三方比对。走 `Variant` writer 的响应（`/execute`，含 BMP 外 key）**本来就对**——`json_writer.cpp` 的 `std::sort` 配 `std::string` 的 `<` 即字节序。但 `/stats` 是**手写拼接 JSON、不走 writer**，顶层、`server` 与 `operators` 三处都按书写顺序输出，本次一并修了。教训：**「某个运行时天然满足」只对具体代码路径成立，不对整个运行时成立**；`operators` 那处是加了校验检查之后才暴露的，而且第一版检查用的 fixture 算子名恰好已是字典序，所以连新加的检查都漏了它一轮
- **过程记录**：`memory/reflections/json-key-order-parity-183.md`
