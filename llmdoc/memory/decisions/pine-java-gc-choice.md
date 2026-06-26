# pine-java GC 选型决策

记录 2026-06-26 JDK 25 升级 + ZGC 评估收口后确定的 pine-java GC 选型决策。本文档覆盖"JVM 进程参数 / GC 选型"层，与 `perf-evolution-roadmap.md`（引擎侧 typed-ColumnFrame / common-mode / VM 适配层）互补不冲突。

## 决策

**保持 JDK 21+ 默认的 G1 GC**，不切 ZGC、不切 Shenandoah、不切 Parallel。仅当后续 deployment 形态发生质变且重测后明确有正向证据时才重启选型。

## 当前部署形态

- 堆：4 G 上限
- CPU：2 C cgroup（`pine-bench-server.unit` 隔离单元）
- 单请求延迟：100+ ms（DAG 38-op + per-item Lua + stub I/O）
- 负载形态：**throughput-bound**（QPS 决策），不是 latency-bound
- JVM：OpenJDK 25 runtime（v0.10.10 起 compile target 同步升 25，见 commit `62475e27`）
- GC：JDK 21+ 默认 G1

## ZGC 实测数据（2026-06-26）

A 路径实验：同机串行同 fixture / 10k req × 16 conc，G1 baseline 与 ZGC 各 calibrated × 3 fixture。

**QPS**

| Fixture | G1 QPS | ZGC QPS | Δ |
| --- | --- | --- | --- |
| `calibrated_2c4g` | 127.9 | 120.9 | **−5.5%** |
| `calibrated` | 128.1 | 119.0 | **−7.1%** |
| `calibrated_itemlua` | 126.5 | 119.5 | **−5.5%** |

stddev 33–36 ms，G1 与 ZGC 无差。

**GC log 实测（单 fixture 验证）**

- G1：729 pauses，avg **3.49 ms**，max **12.82 ms**，**0 次** 超 50 ms。
- ZGC：753 STW pauses，avg **0.008 ms**，max **0.022 ms**（580× 短于 G1）。
- ZGC concurrent phase：108 events / 总 **1087 ms** / 34 个 >10 ms（80s bench 窗口内）。
- 2 C cgroup 下 1087 ms concurrent ≈ **6.7% CPU 偷窃**，与 −5~7% QPS 吻合。

原始报告：`bench-results/report-20260625-113855.txt`（G1 baseline）、`bench-results/report-20260625-114324.txt`（ZGC）。

## 根因

1. **G1 已无长 pause 痛点**：max STW 12.82 ms ≪ calibrated stddev 35 ms，整体 STW budget 远小于 stddev。stddev 35 ms **完全不是 GC 来源**，因此切任何 GC 都无法收紧 stddev。
2. **ZGC trade-off 在小核 cgroup 下输**：ZGC 把 STW 换成 concurrent CPU 工作，2 C cgroup 下 6.7% CPU 被 concurrent phase 偷走，直接体现为 QPS −5~7%。STW 的 580× 收益对 100+ ms 单请求不可见。
3. **ZGC 适用场景与当前形态反向**：ZGC 优势在 ≥16 G 堆 / ≥8 C 核 / 1–10 ms 单请求 / 延迟敏感 SLO；pine-java 当前 4 G / 2 C / 100+ ms / throughput-bound 完全反向。
4. **calibrated stddev 35 ms 与 GC 无关**：实测主导源是 DAG 38-op 调度抖动 + LuaJ JIT warmup + 网络抖动，证伪了"ZGC 收紧 stddev"的先验假设。详见 `llmdoc/guides/benchmark-hygiene.md` "stddev 来源校准"。

## 重启选型的触发条件

以下任一条触发即重做 GC 选型评估：

- **deployment 形态变更**：堆 ≥16 G **或** 核心数 ≥8 **或** 单请求目标 P99 ≤10 ms（latency-bound SLO 出现）
- **G1 worst-case pause 失控**：生产数据显示 G1 STW max > 50 ms
- **calibrated stddev 主导源转移到 GC**：重测 GC log 验证 STW 总贡献接近 stddev 量级（当前 STW total 远小于 stddev × bench duration 时不触发）

重启时复用 commit `c157242a` 引入的 `JAVA_BENCH_OPTS` 钩子（脚本侧 JVM flag 注入入口），直接复跑 A 实验对照。

## 不该做的实验

- `-XX:+UseSerialGC`：单线程必输，无对比价值。
- Shenandoah：与 ZGC 同类 concurrent trade-off，且 OpenJDK 25 Temurin 不默认 ship，引入额外依赖且预期与 ZGC 同向负优化。
- Parallel GC：throughput-only、无 concurrent class unloading、与 G1 同型号但更老，无替换收益。

## 与 perf-evolution-roadmap 的关系

- `llmdoc/memory/decisions/perf-evolution-roadmap.md` 圈定**引擎侧**演进（typed-ColumnFrame / common-mode 列内核 / VM 适配层），其性能假设建立在"运行环境层稳定"之上。
- 本决策圈定**运行环境层**（JVM 进程参数 / GC 选型），是 roadmap 假设的底座。
- 两者互补不冲突；任何引擎侧优化的 calibrated 数据均需声明 GC 形态（当前 G1），跨 GC 比较不可直接套用。

## 引用

- 完整复盘：`llmdoc/memory/reflections/jdk25-upgrade-and-zgc-investigation.md`
- 编译目标升级：commit `62475e27`（pom + CI + README target 21→25）
- JVM flag 实验钩子：commit `c157242a`（`JAVA_BENCH_OPTS` 环境变量）
- 数据存档：`bench-results/report-20260625-113855.txt`（G1）、`bench-results/report-20260625-114324.txt`（ZGC）
- stddev 来源校准：`llmdoc/guides/benchmark-hygiene.md`
