# DAG Ready-Queue 调度器重写复盘

## 背景

v0.9.1-v0.9.2 期间，pine-cpp 的 DAG 调度器从 per-node `std::thread` 方案重写为 ready-queue 调度器，配合 eventfd 零延迟唤醒和跨运行时 benchmark 基础设施的建设。

## 核心变更

### Ready-queue 调度器（`0a70d17`）

- **双隔离线程池**：DAG pool（默认 `nproc * 4`）负责节点调度，shard pool（默认 `nproc * 2`）负责 `data_parallel` 分片执行。两池隔离避免 data_parallel 密集场景饿死 DAG 调度
- **In-degree 追踪**：`std::atomic<int>[]` 数组，每个节点初始值为前驱数量。前驱完成时 `fetch_sub(1, memory_order_acq_rel)` 递减后继 in-degree，归零即提交到 DAG pool
- **Completion latch**：`std::atomic<size_t> remaining` + `condition_variable` 等待所有节点完成，替代原方案中每个节点一个 `std::future` 的开销
- **CLI 可配置**：`-dag-pool-size` / `-shard-pool-size` 暴露给 `pineapple-server`

### Seed loop 竞态修复

重写过程中发现的关键 bug：seed loop（识别根节点并提交到池中）最初读取 `in_degree[i].load() == 0` 来判断根节点。但由于 pool worker 可能在 seed loop 遍历期间已经完成某些根节点并递减了非根节点的 in-degree，导致非根节点被错误地当作根节点提交，产生死锁（前驱未完成就执行）或重复执行。

修复：seed loop 改为读取不可变的 `graph.nodes[i].preds.empty()` 判断根节点，彻底消除竞态。

### Eventfd 零延迟唤醒（`9694b83`）

客户端断连 watcher 线程原使用 `poll(client_fd, 100ms)` 轮询，存在最多 100ms 的唤醒延迟。改为 `eventfd` + `poll()` 同时监听 client fd 和 wakeup fd，超时设为 `-1`（无限等待）。请求完成后主线程 `eventfd_write` 通知 watcher 立即退出。

### 跨运行时 Benchmark 工具链（`36455fe`、`13d0bd5`）

- `scripts/bench-dag-scheduler.sh`：master vs 当前分支 C++ 调度器 A/B 对比（hyperfine + hey）
- `scripts/bench-cross-runtime.sh`：四运行时 × 6 档 DAG 规模（5-500 节点），三阶段测试（顺序延迟 / 固定 QPS=500 / 最大吞吐）
- `scripts/bench-compare.py`：两次运行结果的 delta 报告

### Nightly Benchmark CI（`9bbe386`）

`.github/workflows/nightly-benchmark.yml` 每日 22:30 UTC+8 运行，自动下载上次成功运行的 artifact 做对比，通过 Bark 推送结果通知。

## Benchmark 结果

跨运行时 benchmark 显示 C++ 在吞吐场景下比 Go 快 60-80%（200-500 节点 DAG），主要收益来自：
- 线程池复用避免 per-request 线程创建开销
- In-degree 原子操作替代 per-node future/channel 同步
- 零拷贝 window view 避免 shard 物化

## 教训

1. **可变原子 vs 不可变结构**：seed loop 读取 mutable atomic 而非 immutable preds 导致竞态死锁。在并发初始化场景中，优先使用不可变数据源做判断条件
2. **轮询 vs 事件驱动**：100ms poll timeout 看似简单但引入不必要延迟，eventfd 是 Linux 上零成本的线程间通知原语
3. **Benchmark 先行**：先建立 A/B 对比工具再做优化，避免"感觉快了"的主观判断
