# R3 审计 + 差分模糊增强复盘（34 commits）

## 范围

自上次 llmdoc 更新（`120c944`）至 `337581a`，34 commits 覆盖：

- **R3 parity audit**：26 项（HIGH 5 + MEDIUM 6 + LOW 10 + 追加 X5）全部硬实现
- **R10-17 review 收尾**：5 项（NaN/Inf 前缀偏差、server stop_token last-mile、dump_json 紧凑、fixture 锁定、多节点 cancel 测试）
- **Fuzz 增强**：cpp 接入 differential-fuzz + 11 新维度 + nightly 10k / weekend 100k
- **Fuzz 发现修复**：17 个 go-vs-cpp divergence（2 根因）+ 1 个 go-vs-java RawValue 泄漏

## 关键架构变更

### 1. Frame 多态化（R3-L3）

`using Frame = ColumnFrame` alias 提升为**抽象基类** `pine::Frame`（`include/pine/frame.hpp`），`ColumnFrame` 和新增 `RowFrame` 作为两个物理实现。`Operator::execute` 签名从 `const ColumnFrame&` 改为 `const Frame&`。`pine::make_frame(storage_mode, common, items)` 工厂按 config `storage_mode` 路由。

影响面：所有算子（17 个）+ engine.cpp + parallel_execute + server.cpp + 全部 tests。这是自 v0.6 列存重构以来最大的 DataFrame 层结构变更。

教训：**decision-04/14 "MVP 单实现" 在对标阶段必须放弃**。当初 RowFrame 缺失被标为"合理差异"是因为 cross-validate 结果一致，但 fuzz 接入后等价测试立即揪出 PRESENT-NULL 投影 bug — 缺失的 impl 等于缺失的测试覆盖。

### 2. C++23 采纳（R3-L1）

CMake 从 C++20 升到 C++23，主要为 `std::stacktrace`（`PanicError::detailed_error()` 对偶 Go `DetailedError()`）。探测 `libstdc++exp` / `libstdc++_libbacktrace` 两种链接名；`PINE_HAS_STACKTRACE` 宏开关控制编译期分支。

### 3. HTTP/1.1 keep-alive（R3-L9b）

server 连接处理从单次 request→close 改为 while-loop 复用。`Connection: keep-alive/close` 头由 `MiddlewareContext.keep_alive` 控制。`-idle-timeout` 终于 load-bearing。

### 4. 外部取消传播（R3-H3 + R10-2）

Engine::execute 新增 `std::stop_token external_cancel`；run_dag 通过 `std::stop_callback` 桥接到内部 `cancel_source`。Server 端通过 `poll(POLLRDHUP|POLLHUP|POLLERR)` watcher 线程检测客户端断连后 `request_stop()`。

## Fuzz ROI 验证

**50 round smoke 立即暴露 17 个 divergence**（fixture cross-validate 从未触发的边缘组合）：
- 16/17：`flow_contract` cpp 强制 vs Go 可选
- 1/17：`transform_normalize` init eager OOB（skip 分支也走 init，Go 在 Execute 惰性取）
- +1 Java：`convertValue` TokenBuffer round-trip 导致 RawValue 泄漏

**核心教训**：fixture-based cross-validate 有手写偏差——作者倾向于生成"正常"的 config。fuzz 的价值在于覆盖人类不会手写的组合：缺 flow_contract、空 item_output + skip、-0.0 经过 serialize→deserialize round-trip 等。

## 四方 dual-impl 等价测试覆盖

| 运行时 | impl 数 | 等价测试 | 发现 |
|--------|---------|---------|------|
| pine-go | 2 | ✅ frameModes + FuzzApplyOutputStorageEquivalence | baseline |
| pine-java | 2 | ✅ FrameEquivalenceTest (10 cases, R3-X3) | 首跑全过 |
| pine-python | 2 (新增 RowFrame) | ✅ test_frame_equivalence.py (63 cases, R3-X1) | **首跑抓出 to_result_items explicit-null 过滤偏差** |
| pine-cpp | 2 (新增 RowFrame) | ✅ test_frame_equivalence.cpp (R3-X2) | **首跑抓出 RowFrame::to_result 同样 bug** |

## 缺失/可提升为稳定文档的候选

1. `pine-cpp-runtime.md` 需更新：Frame 多态 + RowFrame + C++23 + keep-alive + stop_token + 多项 R3 能力
2. `dag-engine.md` 需更新：ValidateOutput + NaN/Inf + external cancel + shard cancel
3. `ci-quality-baseline.md` 需更新：4 引擎 + 15 op types + 11 维度 + weekend-fuzz workflow
