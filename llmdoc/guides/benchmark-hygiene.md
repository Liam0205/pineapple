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

## Fixture 代表性

- `fixtures/benchmarks/realistic_for_you_calibrated*` 是生产 proxy（按真实流量 calibrate，N≈10 行），是**性能决策的唯一裁判**
- `large_*` / `small_*` / `medium_*` 是合成压测，只用于定位算法级 bug（如 O(N²) 增长曲线），其增幅数字**不得作为优化收益声明**

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
