# Benchmark 基础设施建设与跨运行时性能优化战役

## 背景

56 commits 跨度的大型优化周期，涵盖：
1. 从零搭建 fixture-driven benchmark 基础设施（脚本、CI 集成、profiling 工具）
2. 四运行时 benchmark stub 算子（含延迟分布模拟 + 校准模式）
3. C++ 性能优化系列（PERF-9 到 PERF-18）
4. Go 性能优化系列（lazy proxy、bitmap、flat ItemWrite、GOGC=400）
5. Java 性能优化系列（lazy proxy、bitmap、initialCapacity）

最终 benchmark 结果（realistic_for_you_calibrated, 5000 req, concurrency 50）：
- C++ 212.3 QPS（行存模式）
- Java 191.3 QPS
- Go 146.8→152 QPS（优化后）

## 关键决策

1. **Benchmark stub 算子设计**：使用真实 FP 计算（而非 sleep）模拟 CPU 延迟，通过 iteration-based 校准确保跨运行时可比性。每个 stub 有 `latency_ms` + `latency_stddev_ms` 参数控制延迟分布
2. **C++ FlatMap 替代 std::map/unordered_map**：`JsonValue::object_t` 从 `std::map` → `std::unordered_map` → 自定义 `FlatMap`（sorted vector），减少 hash 开销和内存碎片
3. **Variant 重命名**：`JsonValue` → `Variant`，反映其作为通用值类型的定位（不仅限于 JSON 边界）
4. **RapidJSON 替代手写序列化器**：dump_json 热点（22.4% profile）通过 RapidJSON 的 Writer 消除
5. **jemalloc 默认启用**：CMake `PINE_USE_JEMALLOC=ON`，通过 `target_link_libraries` 链接，减少多线程 malloc 竞争
6. **Go GOGC=400**：server 启动时设置，减少 GC 频率换取吞吐（内存充裕场景）
7. **跨运行时 lazy OperatorInput proxy**：Go/Java/C++ 三方统一从 eager reify 改为 lazy 按需读取，避免 O(N×M) 预复制

## 教训

### 1. Benchmark stub 的 SetCommon 副作用陷阱

初版 bench stub 用 `output.SetCommon("sink", result)` 防止编译器优化掉计算。但这导致：
- 多算子写同一 common 字段时 last-write-wins 语义使结果不确定
- Java 的 volatile field + Go 的 `runtime.KeepAlive` 是更正确的 sink 方式

**How to apply:** benchmark/test 算子需要 sink 时，优先用语言级 volatile/KeepAlive 而非框架 API 副作用。

### 2. Profiling 驱动优化的 ROI 远高于猜测

gprof 数据明确指出 `std::map::find` 10.2% + JSON 序列化 22.4% 两个热点。按 profile 数据排序实施，每个优化都有可量化收益。相比之下，"感觉应该快"的优化（如 reserve 从 64→4096）收益微乎其微。

**How to apply:** 性能优化必须先 profile，按热点百分比排序实施。不要凭直觉猜测瓶颈。

### 3. Cross-validate Section 8 的端口残留问题

并发测试 section 假设端口干净，但前一次运行的残留进程会累积 exec_count 导致 stats 校验失败（期望 10，实际 70/80/90）。修复：测试开始前 `lsof -ti:$port | xargs kill`。

**How to apply:** 任何使用固定端口的集成测试都应在启动前清理残留进程。

### 4. 跨域 commit 拆分的必要性

本轮 56 commits 中大量 `[split (xxx)]` 前缀，是 reorder-commits skill 的 split-then-reorder 产物。跨域 commit（同时改 scripts/ + pine-cpp/）在 reorder 时必然冲突，拆分后按域归组才能无冲突 cherry-pick。

**How to apply:** 开发时就应按域隔离 commit，避免事后拆分的额外成本。
