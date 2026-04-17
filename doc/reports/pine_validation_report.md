# Pine 引擎验证报告

> 测试环境：Apple M5, arm64, Go 1.24, darwin  
> 测试时间：2026-04-17  
> 引擎版本：Pine MVP + 7 内置算子

## 1. 正确性验证

### 1.1 测试覆盖

| 包 | 覆盖率 | 测试数 |
|----|--------|--------|
| `operators/recall` | 95.2% | 7 |
| `operators/merge` | 92.9% | 5 |
| `operators/feature` | 91.5% | 12 |
| `operators/filter` | 84.0% | 12 |
| `operators/reorder` | 94.1% | 8 |
| Pine 根包 | 89.7% | 12 |
| `internal/dataframe` | 98.7% | — |
| `internal/registry` | 97.4% | — |
| `internal/runtime` | 94.5% | 12 |
| `internal/dag` | 94.9% | 11 |
| `internal/config` | 88.5% | — |

### 1.2 端到端集成测试

使用完整的 7 算子流水线（2 路召回 → 合并去重 → 过滤 → 特征分发 → 归一化 → 排序）验证：

- 召回算子正确注入 `_source` 字段
- 合并算子按 `item_id` 去重，保留首次出现
- 过滤算子移除 `item_status == "offline"` 的 item
- 分发算子将 common 字段复制到每个 item
- 归一化算子将 `item_score` 映射到 `[0, 1]`
- 排序算子按 `item_score` 降序排列
- 最终输出顺序和字段值均与预期一致

### 1.3 DAG 依赖正确性

DAG 单元测试覆盖：

- RAW（写后读）、WAW（写后写）、WAR（读后写）三种数据冒险
- 召回算子 `item_output` 不参与字段级 DAG 推导（允许并行）
- 合并算子 `sources` 建立显式 DAG 边
- 菱形依赖（diamond dependency）
- 读-改-写链（read-modify-write chain）
- 自读写不产生自环
- 拓扑排序正确性

## 2. 稳定性验证

### 2.1 并发安全

| 测试场景 | 结果 |
|----------|------|
| `go test -race ./...` | 无数据竞争 |
| 20 goroutine 并发执行同一 Engine | 通过，无状态泄漏 |
| 10 goroutine 并发执行（scheduler_test） | 通过，结果一致 |

### 2.2 异常处理

| 场景 | 行为 |
|------|------|
| 算子 panic | 捕获为 `PanicError`，包含堆栈信息 |
| 算子返回 error | 包装为 `ExecutionError`，取消下游执行 |
| 算子发出 warning | 收集 warning，继续执行后续算子 |
| context 取消 | 无 panic，无挂起，优雅退出 |
| nil request | 返回 `ValidationError` |
| 缺失必填 common 字段 | 返回 `ValidationError` |
| 缺失必填 item 字段 | 返回 `ValidationError` |

### 2.3 静态分析

```
go vet ./...  → 无问题
```

## 3. 执行效率

### 3.1 单请求延迟

| 流水线规模 | Item 数 | 延迟 | 分配次数 | 内存 |
|-----------|---------|------|----------|------|
| 小型（3 算子） | 10 | 12.7 µs | 148 | 18 KB |
| 小型（3 算子） | 100 | 48.3 µs | 753 | 110 KB |
| 中型（6 算子） | 100 | 142 µs | 2,118 | 338 KB |
| 中型（6 算子） | 1,000 | 1.22 ms | 20,159 | 3.4 MB |
| 大型（7 算子） | 1,000 | 1.33 ms | 22,883 | 3.6 MB |
| 大型（7 算子） | 10,000 | 11.5 ms | 227,389 | 35.6 MB |

### 3.2 并行与并发

| 场景 | 延迟 | 说明 |
|------|------|------|
| 双路并行召回（各 500 items） | 383 µs | 两路召回并发执行 |
| 并发 Execute（10 goroutine） | 118 µs/req | 多请求共享同一 Engine |
| Engine 创建 | 115 µs | 配置热重载成本 |

### 3.3 吞吐量伸缩性（7 算子，100 items）

| 并发 Goroutine 数 | 延迟 | 相对加速比 |
|-------------------|------|-----------|
| 1 | 152 µs | 1.00x |
| 2 | 103 µs | 1.47x |
| 4 | 123 µs | 1.23x |
| 8 | 129 µs | 1.18x |

### 3.4 效率评估

**延迟**：7 算子 + 1000 items 的完整流水线在 1.33ms 内完成，满足在线推荐/搜索场景的延迟要求（典型 SLA 10–50ms）。

**分配开销**：每个 item 约 23 次分配，这是行存储 `map[string]any` 的固有成本。设计文档中规划的列存储方案可显著降低分配量和 GC 压力。

**并发瓶颈**：吞吐量在 2 goroutine 时达到峰值（1.47x），4+ goroutine 后因 scheduler 的全局 mutex 争用而回落。这是行存储 MVP 的已知取舍——per-operator mutex 序列化了 DataFrame 的读写。列存储可将锁粒度缩小到独立的内存列，减少争用。

**配置热重载**：Engine 创建仅需 115µs，配置切换对在线流量无感知影响。

## 4. 已实现算子清单

| 算子 | 类别 | 说明 |
|------|------|------|
| `recall_static` | Recall | 返回配置中指定的静态 item 集合 |
| `merge_dedup` | Merge | 按指定字段去重，保留首次出现 |
| `feature_normalize` | Feature | 对 item 字段做 min-max 归一化 |
| `feature_dispatch` | Feature | 将 common 字段值复制到每个 item |
| `filter_condition` | Filter | 移除字段值匹配指定值的 item |
| `filter_truncate` | Filter | 保留前 N 个 item |
| `reorder_sort` | Reorder | 按数值字段升序/降序排列 |

## 5. 服务外壳

`cmd/pineapple-server` 提供最小化 HTTP 服务：

- `POST /execute` — 接收 JSON Request，返回 JSON Result
- `GET /health` — 健康检查
- `-config` 参数指定配置文件路径
- 文件变更自动热重载（`atomic.Pointer` 替换 Engine）
- SIGINT/SIGTERM 优雅关闭
- 仅依赖标准库，无第三方依赖
