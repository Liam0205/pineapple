# Bench 归因与 Frame 锁优化战役复盘

## Task

- chore/bench_and_doc 分支上跨 3 天的性能优化战役（9 commits）：10k req 全 fixture bench 发现 C++ 在 large_5000 上 QPS 51、比 Java 214 慢 4 倍，与"标杆运行时"目标背离。先写 op-attribution 脚本（`scripts/bench-attrib-large-5000.py`，逐个删 op 对比 cpp/go 比率）定位到 merge_dedup 的 O(N²) 去重（`bd3fb75`，换 `unordered_set` 后 51→152）；再依 perf record（pthread_rwlock 占 20% CPU）做三阶段锁窗口合并（`eab4415`/`9f7db78`/`a7d3b31`）；又自研 `pine::SharedMutex` v1（`6aa87bd`，CAS-loop）并试用 folly::SharedMutex，均在真实负载退化；最终 revert（`3c87bd6`）回 per-call 锁形态，v2 重写（`40a2652`）用 Go sync.RWMutex 协议达单次 10.14ns（超 Go 的 13.75ns）但 Frame 不切换、作为备件留库。

## Expected vs Actual

- Expected: large_5000 上锁窗口合并累计 +247%，认为是通往标杆运行时的主路径；自研/第三方 SharedMutex 能进一步收割 pthread_rwlock 的 20% CPU。
- Actual: 唯一普适的真实收益是 merge_dedup 算法修复（O(N²)→O(N)，Go 用 map、Java 用 LinkedHashSet 本就是 O(N)，C++ 是掉队者）。真实生产 proxy fixture `realistic_for_you_calibrated*`（N=10 行）上 stage-2 仅 +4%、merge_dedup 修复不可见（0%）；锁在 calibrated 上只占 ~2% CPU，任何 mutex 层优化都测不出 QPS 差异。最终决策：Frame 维持 `std::shared_mutex` + per-call 形态（与 Go/Java 锁形态完全对齐，放弃 stage-2/3 的 +4%），用户价值排序为跨运行时实现对齐 > 单 fixture 4%。

## What Went Wrong

1. **fixture 代表性错误**：stage-1/2/3 的设计动机全部来自 large_5000（5000 行合成压测），其 +247% 数字误导了多轮决策。用户明确指出"在这个 bench 上跑意义不太大，calibrated 才是真实场景"后所有结论重写。
2. **zombie 进程污染整天数据**：6-07 调试 v1 死锁时跑的 `/tmp/repro_sm`（16 reader + 4 writer spin），kill 主线程后 worker 线程继续吃满 16 核（累计 25391 CPU 分钟）。6-08 整天 bench 在 load 20+ 上跑，得出"stage-1 是 -34% 负优化"的完全错误结论；直到 `ps aux --sort=-%cpu` + atop 历史打点（`atop -r /var/log/atop/atop_20260608 -b 11:19 -P CPL`）对比 bench 时段 load（6-06 是 0.11，6-08 是 21.56）才发现。清机后重测，stage-1 实际是 ±0。
3. **microbench 误导**：folly::SharedMutex 在"单 mutex 16 reader"microbench 上快 21x，但真实负载（每 request 一个 frame、低 per-mutex 并发）退化 6-45%——deferred reader 的优势场景在 pine 根本不存在。pine v1 同理。microbench 测的访问模式必须和生产一致。
4. **二进制布局噪声 ±5-7%**：v2 的真实收益（perf stat 量出 0.4-0.9 个百分点）低于 fresh build 之间的布局噪声，裸 bench 时而 -7% 时而 +2%。同日同机对照 + perf stat 微架构指标（instructions/IPC/icache-miss）才是可信信号。
5. **v1 失败根因是矫枉过正**：最初 fetch_add 原型有状态崩坏 bug（0xFFFFFFFF），真正根源是 writer 的 pending→holding 用 plain store 覆盖了 transient reader 增量；当时全改成 CAS-loop 是错的修法，引入 CAS 重试风暴 + yield 自旋烧 CPU。Go 协议证明 fetch_add 是安全的——只要 writer 用"负数宣告"（`fetch_sub(1<<30)`）而不是覆盖。

## Root Cause

- 优化目标选错了基准：把"哪个 fixture 数字最难看"当成"哪个 fixture 最重要"，缺少"以 calibrated 为代表性基准"的明文约定（parity_baseline_priority 只讲了正确性基准，没讲性能基准）。
- bench 环境卫生无 checklist：跑 bench 前不查 uptime/load、kill 进程后不双确认残留线程，导致整天数据作废且先污染了结论。
- 对锁开销的归因停在"pthread_rwlock 占 20% CPU"（large_5000 下），没有先在目标 fixture（calibrated，~2%）上复核占比就开工。
- glibc rwlock 贵的本质（反汇编实证，详见 `.code-review/sharedmutex-deep-dive/analysis.md` 9 节深潜）：rdlock fast path ~25 条指令（PLT 不可 inline、TLS 自死锁检测、运行时偏好策略分支），真正干活的只有 1 条 LOCK XADD；Go RLock inline 后 3 条指令。这解释了差距来源，但不构成在 2% 占比场景替换的理由。

## Missing Docs or Signals

- 缺一份"bench 噪声卫生"指南：load 预检、残留进程双确认、同日同机对照、微架构指标交叉验证、fixture 代表性声明。
- `llmdoc/architecture/pine-cpp-runtime.md` 未记录 Frame 锁形态（per-call、与 Go/Java 对齐）这一刻意决策，下次优化者可能再次尝试合并锁窗口。
- `pine::SharedMutex` v2（`pine-cpp/include/pine/shared_mutex.hpp` + `pine-cpp/tests/test_shared_mutex.cpp` 10 cases）作为已验证备件的存在与启用条件（锁占比显著升高时）无处可查。
- large_5000 已进 TSan 集（`scripts/cpp-tsan-smoke.sh`），但其"合成压测、非代表性"定位没有写在 fixture 旁。

## Promotion Candidates

- `llmdoc/guides/` 新增 bench 噪声卫生指南：跑 bench 前 `uptime` 确认 load、杀进程后 `ps aux --sort=-%cpu` 双确认、同日同机 A/B 对照、perf stat 微架构指标交叉验证、fixture 代表性以 calibrated 为准。
- `llmdoc/architecture/pine-cpp-runtime.md` 补：Frame 锁形态（per-call `std::shared_mutex`，与 Go/Java 对齐的决策及放弃 +4% 的理由）、`pine::SharedMutex` v2 备件的存在与启用条件。
- `llmdoc/must/conventions.md` 的"跨引擎能力等价审计维度"增加"锁形态/并发原语对齐"一条：锁的形态（per-call vs 窗口合并）属于跨运行时实现对齐范畴，不是单引擎可自由发挥的内部细节。

## Follow-up

- 落地上述三项 promotion（指南 + 架构文档 + conventions 条目）。
- 若未来 calibrated 类负载上锁占比显著上升（>10% CPU），重新评估启用 `pine::SharedMutex` v2（microbench 10.14ns，已有 10 case 测试覆盖），并以同日同机 + perf stat 验证。
- op-attribution 方法（`scripts/bench-attrib-large-5000.py`）可泛化为任意 fixture 的跨运行时回归归因工具。
