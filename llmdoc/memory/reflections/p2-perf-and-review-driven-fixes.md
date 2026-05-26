# P2 性能优化批次与审查驱动修复周期

## 背景

v0.8.0 后，pine-cpp 在 `feat/pine-cpp` 分支上经历了约 120 个 commits、10 轮代码审查（首轮 + inc 1-9）。审查从 v0.8.0 基线开始，累计发现 67 个跟踪项（55 原始 + 9 inc-6 新增 + 3 inc-8 新增），最终全部关闭。本 reflection 覆盖最后 22 个 commits（`c84147c..8f13a31`），即 P2 性能/架构优化批次和审查修复响应。

## 主要变更

### 性能/架构优化（P2 deferred 项）

- **P2-01** `dump_impl` 从返回 `std::string` + `ostringstream` 改为 `void dump_impl(..., std::string& out)` 直接追加，消除递归临时分配。
- **P2-05** `ColumnFrame::make_window_view` 零拷贝窗口视图替代 parallel_execute 中的逐行物化，消除 2×column-cell-touch。
- **P2-06** `OperatorOutput::item_writes_` 从 `map<int, map<string, JsonValue>>` 改为 `vector<ItemWrite>`，`set_item` 从 O(log n) 降到 O(1) 摊销。
- **P2-08/09** `OperatorTraits<T>` 编译期标记检查 + `PINE_REGISTER_OPERATOR_T` 宏，所有 17 个内置算子迁移，消除注册时 dynamic_cast probe。
- **P2-12/20** `require_common_by_name` / `require_item_by_name` 共享 helper 替代算子内联副本。
- **P2-28** Redis `ConnectionPool` idle bound（kMaxIdlePerKey=16）+ idle timeout（60s）+ acquire 时 stale discard。
- **P2-29** `ConnectionPool::ScopedClient` RAII handle 替代两处 inline `PoolGuard`。
- **P2-31** `redis_client::read_into` 直接读入调用方缓冲区，消除 4KB 中间拷贝。
- **P2-32** `kSkipBuiltins` 从 `std::set<std::string>` 改为 `constexpr string_view[]` + `binary_search`。

### 审查修复响应

- **inc-6 阻塞 B1** `Releaser::operator()` 中 `try/catch(...)` 包裹 `release_vm`，防止 noexcept 析构器边界触发 `std::terminate`。
- **inc-8 R8-1** `to_result` 在 window view 上加 `is_window_view()` guard 防 null-deref。
- **inc-8 R8-2** `make_window_view` 加越界校验 + doctest 覆盖。
- **P2-26** Java `SetItemOrder` 错误类型从 `IllegalArgumentException`/`IndexOutOfBoundsException` 统一为 `ExecutionError`。
- **P2-27** `merge_shard_output` panic 消息削减尾部括号注释，字节级对齐四运行时。
- **P2-30** Java `truncateBody` 前置 null guard。

## 教训

### 1. 审查驱动开发周期的正反馈效率

10 轮审查 → 67 项跟踪 → 全部关闭，这个周期验证了"审查发现 → 逐条修复 → 增量审查确认"的工作模型有效。关键要素是 **progress.md 作为状态真相来源**——没有它，到 inc-5 之后就无法追踪哪些是新发现、哪些是遗留。

### 2. progress.md 的局限性：遗漏增量轮次发现的新项

inc-6 发现了 `StatePool release_vm rethrow` 这个新阻塞项，但 progress.md 的"55 项全部处理完毕"统计未包含这个新增项。这导致在 inc-7/inc-8 期间，进度看起来是"零剩余"而实际有一个 open 阻塞。**教训：progress.md 的"总计"必须随增量轮次更新，包含新发现。**

### 3. P2 deferred 不等于低优先级——架构改进有传播效应

`OperatorOutput::ItemWrite` 和 `ColumnFrame::make_window_view` 改变了核心数据结构和调度模型的 API 面。虽然它们是"P2 性能优化"，但它们引入了新的 public API 和新的不变量（window view 的只读契约、ItemWrite 的顺序语义），需要文档同步。**教训：数据结构级变更即使没有外部可见行为变化，也需要 llmdoc 同步，因为它们改变了未来开发者的心智模型。**

### 4. CRTP 注册宏是约定级变更

`PINE_REGISTER_OPERATOR_T` 替代 `PINE_REGISTER_OPERATOR` 作为首选注册入口。这不只是一个 perf 优化——它改变了"新增算子时该用哪个宏"的答案。**教训：影响开发者日常决策的变更必须同步到 conventions.md，而不仅仅是 architecture doc。**

## llmdoc 缺失项

- `pine-cpp-runtime.md` 中的 CLI flag 列表包含已被移除的 `-read-header-timeout` / `-idle-timeout`（已在本次更新中修正）。
- `OperatorOutput` 的内部数据结构（`ItemWrite` vector）未在任何文档中描述（已在本次更新中补充）。
- `ColumnFrame::make_window_view` 作为新 public API 未被文档化（已在本次更新中补充）。
- Redis `ConnectionPool` 的 idle bound/timeout/ScopedClient 未被文档化（已在本次更新中补充）。
