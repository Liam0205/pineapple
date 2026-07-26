# Benchmark 噪声卫生

本指南描述跑性能 benchmark 时的环境卫生纪律：跑前/跑后检查、对照纪律、fixture 代表性与 microbench 戒律，避免被环境噪声、合成负载和访问模式失真误导出错误的优化结论。

## 适用范围

当任务涉及以下情况时使用本指南：

- 跑任何跨运行时或单运行时的 QPS / 延迟 benchmark
- 对比不同 build / 不同优化方案的性能数字
- 用 microbench 数据预测生产收益
- 为跨运行时性能回归做逐算子归因

## 跑前检查

- `uptime` 确认 load < 1，否则先清机
- `ps aux --sort=-%cpu | head` 确认无残留高 CPU 进程
- bench 与 profiling 决不并行：两者共享 CPU，互相污染数字

## 跑后检查

- 再 `uptime` 一次：load 飙升说明 bench 留了尾巴（残留进程/线程）
- 杀进程后必须用 `pgrep -f <name>` + `uptime` 双确认。教训：detached worker 线程在主线程死后继续 spin，曾吃满 16 核（累计 25391 CPU 分钟）污染整天 bench 数据，先得出完全错误的负优化结论
- atop 历史打点可回溯验证任意 bench 时段的真实 load：`atop -r /var/log/atop/atop_YYYYMMDD -b HH:MM -P CPL`

## 对照纪律

- 不同 build 的对比必须**同日同机同时段连跑**；跨天数字不可比（机器状态漂移）
- fresh build 之间存在 **±5-7% 的二进制布局噪声**（函数地址/对齐漂移），小于该幅度的 QPS 差异不可下结论
- 落在噪声带内的差异需 `perf stat` 微架构指标交叉验证：instructions / IPC / L1-icache-miss / branch-miss / context-switches。两个 build 微架构指标持平，即可判定"统计无差异"

## stddev 来源校准

- calibrated fixtures 的 stddev 33–36 ms **不是噪声来源，是负载的固有抖动**——切 GC / 调 JVM flag / 改 JIT 后端都不会收紧
- 三大主导源（按贡献排序）：
  1. **DAG 多 op 并发调度抖动**：calibrated_itemlua 38-op DAG 内 ready-queue 调度顺序非确定，单请求耗时浮动 ~20 ms
  2. **LuaJ JIT warmup 在前 N 个请求的非均匀分布**：luajc 编译触发点漂移，前段请求耗时尾部偏长
  3. **HTTP keepalive / server 调度抖动**：网络栈与 server 端 work-stealing 微秒级浮动放大到毫秒
- **GC pause 不在主要源中**：pine-java G1 实测 max STW pause **12.82 ms** 远低于 calibrated stddev 35 ms，整体 STW budget 远小于 stddev × bench 窗口。详见 `llmdoc/memory/decisions/pine-java-gc-choice.md` "ZGC 实测数据" 段的 GC log 实测
- **推论**：诊断 stddev 之前先采 GC log 验明出处。试图通过 GC tuning 收紧 stddev 是错误方向（除非 GC log 数据反证 STW 主导）；"stddev 高 → 怀疑 GC → 切 GC" 这条错误链路已被 2026-06-26 ZGC A 实验证伪一次，不要复制

## Fixture 代表性

- `fixtures/benchmarks/realistic_for_you_calibrated*` 是生产 proxy（按真实流量 calibrate，N≈10 行），是**性能决策的唯一裁判**
- `large_*` / `small_*` / `medium_*` 是合成压测，只用于定位算法级 bug（如 O(N²) 增长曲线），其增幅数字**不得作为优化收益声明**

### 合成 guardrail fixture 与 calibrated fixture 的分工

两者是不同种类，不可互换：

- **calibrated**（`realistic_for_you_calibrated*`）：实质特征是每个算子带 `bench_profile`（真实流量画像 + 依赖 bench stub 算子）。它是生产性能决策的唯一裁判
- **合成 guardrail**：职责是守护某条代码路径不静默整体退化，**不是生产代理**。其上测出的 delta **不得作为优化收益声明**

现有 guardrail：`fixtures/benchmarks/transform_heavy_1000_{config,request}.json`（一个 `recall_static` + 8 个链式 `transform_normalize`，`storage_mode: column` 钉死，N=1000）。补的缺口是——所有 calibrated fixture 都声明 `storage_mode: row`（对它们 N≈10 的生产情况是正确选择），导致列存批量列访问路径此前没有任何 nightly 守护者。受 ±5-7% 二进制布局噪声限制，它只能防住"列存路径整体崩了"（~20-35% 量级），细粒度回归仍归 `pine-go/benchmarks/bench_storage_ab_test.go` 的 `BenchmarkStorageAB_TransformHeavy_*`。

写新合成 fixture 时，根级 `_comment` 应明写"synthetic, not a performance verdict"及其守护范围。

另注意 issue 措辞陷阱：#160 标题写 "column-favorable **calibrated** fixture"，但合成 fixture 没有 `bench_profile`、不依赖 bench stub 算子，从构造上不可能是 calibrated（issue body 自己纠正为 "synthetic guardrail"）。按标题做会污染"calibrated 是唯一裁判"的判据——issue 标题不是规格。

### 把 in-process microbench 的形状搬成 e2e fixture 时要重查投影/序列化段

搬形状不等于搬负载。microbench 刻意不关心的那一段，恰好可能是 e2e 的主要成本，必须逐段重新检查。

反例（issue #160）：pine-go 的 `transformHeavyConfig` 用空 `flow_contract`（它测引擎内部，这是合理的）。同一形状作为 HTTP e2e fixture 时，空 `item_output` 会让 `ToResult` 把每个 item 投影成 `{}`——`projectMap` 只拷列出的字段，空列表就是空输出，**不回退成"返回全部字段"**（该语义见 `memory/reflections/fix-output-projection-semantics.md`）。结果是序列化成本被完全抹掉，整条 transform 链变成没人读的死写入。新 fixture 因此显式声明 `{"item_output": ["item_id", "item_score_n7"]}`，把链尾字段投影出去。

规则：搬运前逐段问"这段成本在两个测量路径里各占多少"，重点是投影、序列化、请求/响应编解码这些 microbench 天然跳过的段。

### 用户可见文档不写性能倍数

面向用户的文档（`README.md`、`doc/guide_*.md`）写 `storage_mode` 之类性能相关选择时，**只写定性判据 + 指向可复现入口**（如 `pine-go/benchmarks/bench_storage_ab_test.go`），不写倍数或百分比。理由与完整论证见 `llmdoc/memory/decisions/user-docs-no-perf-multipliers.md`；它是 `must/conventions.md`"禁止硬编码定量描述"的一个具体应用。

## Microbench 戒律

- microbench 的访问模式必须与生产一致才有预测力
- 反例：folly::SharedMutex 在"单 mutex 16 reader"microbench 上快 21x，但 pine 是每 request 一个 frame、低 per-mutex 并发——deferred reader 的优势场景在 pine 根本不存在，真实负载反而退化 6-45%

## 归因方法

- 跨运行时回归的逐算子归因用**逐 op 删除对比法**：见 `scripts/bench-attrib-large-5000.py`，逐个从 fixture 中删除算子并对比各运行时耗时比率，定位贡献最大的算子
- 修改其中 config 路径与 drop 列表即可复用于任意 fixture

## 测量路径对称性

比较两个 VM / Lua 后端 / 任何嵌入式组件时，**两侧必须跑同样的宿主↔组件边界数据传递**；PureVM-only（脚本内数据硬编码、无 SetGlobal/读返回）和 Embedded（SetGlobal + Call + 读返回）是不同测量，不可互推：

- 反例：wangshu 官方 baseline 测 PureVM 报 simple 9x faster than gopher，但 LuaOp 真实嵌入路径（SetGlobal+Call+读返回）wangshu 反而慢 1.7x。两个数字都对，但口径不同——嵌入者不能从 PureVM 数字推出生产收益
- 规则：发布性能比较时显式标注口径（**PureVM** vs **CallOnly** vs **Boundary**）；PR / issue 评估嵌入收益时优先 Boundary 档
