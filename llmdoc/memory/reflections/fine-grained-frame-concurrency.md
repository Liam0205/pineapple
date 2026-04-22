# Frame 并发自治：调度器全局锁下沉到 Frame 内部

## 任务

将 DataFrame 并发控制从调度器外部全局 `sync.Mutex` 下沉到 Frame 实现内部，每个 Frame 持有单个 `sync.RWMutex`。

## 做得好的

- **改动精准**：scheduler.go 只删锁、加 `warningsMu`，无逻辑变更；并发安全职责完全转移到 Frame 实现。
- **RowFrame 和 ColumnFrame 统一策略**：都用单个 `sync.RWMutex`，读操作 RLock，写操作 Lock。简洁一致。
- **并发测试覆盖充分**：6 个新增并发测试全部 race-free。
- **全仓 `go test -race ./...` 通过**。

## 教训

- **细粒度锁不一定是正优化**：最初尝试为 ColumnFrame 用两把锁（`commonMu` + `structMu`）实现字段级无锁并发，benchmark 显示 Reorder 场景退化 ~1.8x。原因是结构体膨胀（24→72 bytes）导致 cache line 跨越。回退到单锁后性能恢复。
- **旧全局锁竞争极低**：scheduler 只在 `BuildInput`（微秒级）和 `ApplyOutput`（微秒级）时持锁，`Execute`（毫秒级）不持锁。多算子自然错开 frame 访问窗口，全局锁几乎无竞争。因此锁粒度细化的实际吞吐收益很难测出。
- **架构价值 > 性能价值**：本次改动的真正收益是 Frame 自治——Frame 接口约定自身并发安全，调度器不关心底层存储模式。这是正确的职责边界。
- **benchmark 应尽早做 A/B**：在实现细粒度锁之前就应该 stash + bench 建立基线，而非事后补测。
