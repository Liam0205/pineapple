# 用户可见文档不写性能倍数

记录 issue #160（`storage_mode` 用户文档）时确定的文档口径决策。适用层：面向用户的 `README.md` 与 `doc/guide_*.md`；不约束 llmdoc、PR 描述、reflection 里的实验记录。

## 决策

用户可见文档在描述性能相关的配置选择时，**完全不写倍数、百分比或毫秒绝对值**，只写：

1. 定性判据（什么形状的负载该选哪个值）
2. 指向可复现入口的路径（读者可以自己在自己机器上测）

`storage_mode` 的落点：`doc/guide_pipeline.md` / `doc/guide_pipeline-en.md` 的「Flow 级配置」节给出 `storage_mode` / `log_prefix` / `debug` / `skip_dead_code` 四参数表 + storage_mode 选择判据 + 复现入口 `pine-go/benchmarks/bench_storage_ab_test.go`；`README.md` 核心特性那行扩成带判据摘要 + 指向该 guide 的链接。

## 理由

- **同一实验的数字已经在互相打架**：transform-heavy 的列存收益，llmdoc 记 `~30%`（2.7 vs 3.9 ms），PR #155 描述记 `~37%`（3.56 vs 5.68 ms）——不只是百分比不同，两组绝对值也不同。写进用户文档就是把某一轮的快照凝固成看起来永久的事实
- **口径不可混排**：上述数字都是 Apple M5 Pro 上 pine-go 的 Go microbench，而 README Benchmark 表是 Linux 2C/4G 三运行时 HTTP e2e。把两者并排会违反 `guides/benchmark-hygiene.md` 的"测量路径对称性"
- **数字是活的，判据是稳定的**：同一条路径在 #156（typed columns）/ #157（批量列写）之后连改三轮。判据（扫描密集、批量列访问、无频繁行增删 → 列存）三轮都没变

## 与其他约定的关系

- 是 `must/conventions.md`「禁止硬编码定量描述」的一个具体应用：那条约束的是引擎数量、cross-validate 层数、CI job 数量这类结构性计数；本条把同一逻辑推到性能数字上
- 需要引用具体数字时的正确落点：`memory/decisions/perf-evolution-roadmap.md`（有维护责任人）或 reflection 的实验记录段，它们都标注了机器、口径与日期

## 检索指针

- 用户文档：`doc/guide_pipeline.md`、`doc/guide_pipeline-en.md`、`README.md`
- 可复现入口：`pine-go/benchmarks/bench_storage_ab_test.go`（`BenchmarkStorageAB_*`）
- 口径纪律：`llmdoc/guides/benchmark-hygiene.md`
