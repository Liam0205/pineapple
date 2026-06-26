---
name: jdk25-upgrade-and-zgc-investigation
description: 2026-06-26 评估 pine-java 升 JDK 25 + 切 ZGC 的 A/B/C 路径复盘，记录"性能假设要测不要猜"与 deployment-shape vs GC-shape 匹配检查的教训
type: reflection
---

## Task

评估 pine-java 能否升 JDK 25 + 切 ZGC，按三条路径走：

- A：切 ZGC（保持 JDK 21）
- B：升 JDK 25 编译目标（pom + CI + README）
- C：A + B 串联

预期 A 能拿一些尾延迟收益、B 风险来自 LuaJ 3.0.1 + BCEL verifier 严格化。

## Expected vs Actual

| 路径 | 预期 | 实际 |
| --- | --- | --- |
| A (ZGC) | calibrated stddev 35ms 应该是 G1 STW 来源，ZGC 收紧 stddev、QPS 持平或微升 | calibrated 形态下 net loss **−5.5% / −7.1% / −5.5%** QPS，stddev 无变化，证伪 |
| B (JDK 25) | LuaJ 3.0.1 + BCEL 6.10.0 可能因 25 verifier 严格化而炸 | 246 tests / 0 failures，含 luajc/luac 双后端等价测试，风险证伪 |
| C (A+B) | 取决于 A 的结果 | A 证伪后直接不需要 |

最终落地：commit `62475e27`（B：bump target 25 + CI + README）+ `c157242a`（脚本 `JAVA_BENCH_OPTS` 环境变量钩子，用于以后 JVM flag 实验）。**ZGC 不切默认**。

## 实测数据点（GC log 实跑，可复用）

A 实验设计：同机串行、各 calibrated × 3 fixture（`calibrated_2c4g` / `calibrated` / `itemlua`）、10k req × 16 conc。

**QPS**

| Fixture | G1 baseline | ZGC | Δ |
| --- | --- | --- | --- |
| calibrated_2c4g | 127.9 | 120.9 | −5.5% |
| calibrated | 128.1 | 119.0 | −7.1% |
| itemlua | 126.5 | 119.5 | −5.5% |

stddev 33–36ms，G1 与 ZGC 无差。

**GC log 实测（2026-06-26）**

- G1：729 pauses，avg **3.49 ms**，max **12.82 ms**，**0 次** 超 50 ms。
- ZGC：753 STW pauses，avg **0.008 ms**，max **0.022 ms**（580× 短于 G1）。
- ZGC concurrent phase：108 events / 总 **1087 ms** / 34 个 >10 ms。
- 2C cgroup 下 1087 ms concurrent ≈ **6.7% CPU 偷窃**，与 −5~7% QPS 吻合。

→ G1 STW 根本不够大（max 12.82 ms），stddev 35ms 完全不是 GC 来源。ZGC 的 0.022ms STW 收益被 concurrent CPU 偷窃在 2C cgroup 下完全吃回去且倒贴。

## What Went Wrong

### 1. A 路径推荐基于错误先验

最初推荐 A 的理由是"calibrated stddev 35ms 应该来自 G1 STW pause、ZGC 应该收紧 stddev"。**没先采 G1 GC log 看 max pause**，直接拿 stddev 倒推 GC 是 hot spot。实测 G1 max 才 12.82ms，整体 STW budget 远小于 stddev，假设链从源头就错了。

### 2. ZGC 适用场景未在调研前列检查表

ZGC 优势场景（≥16 G 堆 / ≥8 C 核 / 1–10 ms 单请求 / 延迟敏感）与 pine-java 当前形态（4 G 堆 / 2 C cgroup / 单请求 100+ ms / throughput-bound）完全反向。如果调研前先列 ZGC 适用场景 vs 当前 deployment shape，30 秒就能判 "我们这种形态根本不该切 ZGC"。

### 3. benchmark host runtime 与 maven target 脱节未先确认

机器 PATH 第一个 `java` 已是 OpenJDK 25.0.2，但 pom `<maven.compiler.target>` 还停在 21。v0.10.9 README bench 数据其实早已跑在 JDK 25 runtime 上，只是字节码 target 21。B 路径升级实质只是补齐编译目标，**不是 runtime 切换**。开调研前没先 `java -version` 确认实际 runtime 版本，差点把 "runtime 切换" 与 "compile target 切换" 混为一谈。

### 4. LuaJ 21→25 风险纯凭直觉判定

升级前怀疑 LuaJ 3.0.1 + BCEL 6.10.0 在 25 verifier 严格化下会炸——这是基于历史 BCEL 在跨大版本 JDK 升级时 stackmap frame 兼容问题的直觉，但没先跑一遍 test suite。如果先跑 `mvn test`，几分钟就能证伪，不必把 LuaJ 列为高风险阻塞项。

## Root Cause

### 性能假设必须被实测打过才算事实

"stddev 35ms 来自 GC pause" 是个看起来合理的假设，但合理 ≠ 真。GC log 一开就立刻证伪。对一切将影响选型决策的性能假设，**先跑一次最小复现拿数据，再下结论**。这条已在 `bench-lua-vs-go-performance.md` 和 `isolated-bench-and-resource-ops.md` 中以"预估偏差"的形式出现过，这是第三次同类教训。

### deployment-shape vs GC-shape 匹配检查缺失

JVM tuning 选型应先做"我们的部署形态匹配该 GC 的适用场景吗"检查，这是 GC 选型的零号问题。直接跳到"试一下 ZGC"就跳过了零号问题。

### LTS→LTS 升级风险评估不应纯靠直觉

LuaJ + BCEL 这类字节码生成依赖在跨大版本 JDK 升级时确实是合理怀疑点，但 **test suite + cross-validate 实测** 是最便宜的证伪手段，应优先于"列为风险阻塞 → 开会议 → 拉清单"。

### compile target ≠ runtime

发版数据基线、bench 报告、README 数字这些"实际跑在哪个 JVM 上"，与 pom 的 `<maven.compiler.target>` 是两件事。任何 JVM 升级讨论先 `java -version` + `mvn help:effective-pom | grep target`，两条命令把状态钉死。

## Missing Docs or Signals

1. **没有 GC 选型决策档**：pine-java 当前用 G1，但没有任何文档说明为什么用 G1、什么形态下应该重新评估切 ZGC/Shenandoah/Parallel。下次再有人问"能不能切 ZGC"，会从零重做这次调研。
2. **`benchmark-hygiene.md` 缺 "stddev 来源校准"段**：calibrated 形态下 stddev 33–36ms 是 **应用层** 来源（IO、调度、Lua 调用栈），不是 GC 来源，但当前 guide 没说清楚。
3. **没有"compile target vs runtime version 必须分别核对"的明文规范**：未来再有 JDK / Maven / Gradle 升级讨论，这个坑会被踩第二次。

## Promotion Candidates

### 应立即新增到 `decisions/pine-java-gc-choice.md`

- **结论**：4 G 堆 / 2 C cgroup / throughput-bound 形态下保持 G1（默认）。ZGC 与 Shenandoah 不切。
- **实验证据**：附本次 A 实验 QPS 表 + G1 / ZGC GC log 实测数据点。
- **重新评估触发条件**：堆 ≥16 G **或** 核心数 ≥8 **或** 单请求目标 P99 ≤10 ms **或** 出现 G1 STW max >50 ms 的生产证据。任一条触发就重做选型。
- **JAVA_BENCH_OPTS 实验入口**：commit `c157242a` 给后续 GC flag 实验铺路，未来再起类似讨论直接用此钩子复跑 A 实验。

### 应补到 `guides/benchmark-hygiene.md`

- **calibrated stddev 来源校准**：calibrated 形态下 stddev 33–36 ms 的主导来源是应用层（IO、调度、Lua 调用栈），G1 STW 贡献 <10 ms。诊断 stddev 之前先采 GC log 验明出处，避免"stddev 高 → 怀疑 GC → 切 GC"这条错误链路被复制。

### 应补到 `must/conventions.md`（JVM 工具链段）

- **compile target vs runtime version 必须分别核对**：任何 JDK 升级讨论 / bench 报告，先记录 `java -version`（runtime）与 `mvn help:effective-pom | grep target`（compile target），二者可不同步，不可互推。

### 仅保留在 memory

- 三个 fixture 的 QPS 具体数字（127.9 / 120.9 等）：当时机器状态相关，不属稳定契约。
- LuaJ 3.0.1 + BCEL 6.10.0 在 JDK 25 下 246 tests 通过的具体快照：会随版本 drift。

## Follow-up

1. **本次任务实际已完成的部分**：commit `62475e27`（target 25 + CI + README）+ `c157242a`（JAVA_BENCH_OPTS 钩子），ZGC 不切。
2. **建议在下一次 llmdoc 更新中执行**：新增 `decisions/pine-java-gc-choice.md`、给 `benchmark-hygiene.md` 补 "stddev 来源校准" 段、给 `conventions.md` 补 "compile target vs runtime version" 一句。
3. **方法论沉淀**：以后任何 "切 X 性能优化" 类提案，强制三件套——(a) X 的适用场景表 vs 当前 deployment shape、(b) 当前形态的 baseline 指标 + 假设的瓶颈来源采证、(c) 最小复现 A/B 数据。三件套缺一项就不进决策。
