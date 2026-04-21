# Lua vs Go 原生算子性能对比

对比 `transform_by_lua`（gopher-lua VM）与等价 Go 原生算子在不同计算复杂度下的执行效率差异。

## 测试环境

- Apple M5, macOS, Go 1.24
- 每个测试运行 3 次取中位数
- 端到端引擎执行（recall_static → 被测算子），含引擎开销

## 复杂度档次

| 档次 | 名称 | 计算逻辑 | 复杂度要素 |
|------|------|----------|-----------|
| L1 | identity | `return item_x` | 纯字段透传，零计算 |
| L2 | arithmetic | `item_price * 0.85 + 10.0` | 单次乘加 |
| L3 | branching | 4 级 if/elseif 分段折扣 | 条件分支 + 算术 |
| L4 | multi-field | `0.5*a + 0.3*b + 0.2*c`，clamp [0,1] | 多字段读取、多步计算 |
| L5 | iterative | Horner 法 5 次多项式求值 | 循环、多轮浮点运算 |

## 结果（100 items）

| 档次 | Lua ns/op | Go ns/op | Lua/Go 比 | Lua allocs | Go allocs |
|------|-----------|----------|-----------|------------|-----------|
| L1 identity | 68,556 | 52,964 | 1.3x | 1,363 | 1,058 |
| L2 arithmetic | 73,166 | 53,957 | 1.4x | 1,381 | 1,158 |
| L3 branching | 76,268 | 53,690 | 1.4x | 1,385 | 1,158 |
| L4 multi-field | 92,666 | 60,069 | 1.5x | 1,547 | 1,156 |
| L5 iterative | 96,314 | 57,684 | 1.7x | 1,626 | 1,158 |

## 结果（1000 items）

| 档次 | Lua ns/op | Go ns/op | Lua/Go 比 | Lua allocs | Go allocs |
|------|-----------|----------|-----------|------------|-----------|
| L1 identity | 648,260 | 549,671 | 1.2x | 12,703 | 10,075 |
| L2 arithmetic | 672,953 | 521,078 | 1.3x | 12,863 | 11,075 |
| L3 branching | 695,176 | 524,711 | 1.3x | 12,953 | 11,075 |
| L4 multi-field | 909,452 | 615,529 | 1.5x | 14,602 | 11,060 |
| L5 iterative | 1,234,680 | 583,077 | 2.1x | 15,098 | 11,075 |

## 分析

### 开销来源

Lua 相对 Go 的额外开销来自三个层面：

1. **VM 解释执行**：gopher-lua 是纯 Go 实现的 Lua 5.1 VM，每条指令经过解释分发，无法享受 Go 编译器的内联和寄存器分配优化。
2. **Go↔Lua 类型转换**：每次 `goToLua`/`luaToGo` 调用涉及类型断言和 Lua value 对象分配（`LNumber`、`LString` 等），是内存分配的主要来源。
3. **全局变量读写**：Lua 算子通过 `L.SetGlobal`/`L.GetGlobal` 传递数据，每个 item 每个字段都有一次 hash table 操作。

### 关键发现

- **低复杂度（L1-L3）**：Lua 仅比 Go 慢 1.2-1.4x。引擎开销（DataFrame 构建、调度）占总耗时的大头，Lua VM 开销被稀释。
- **高复杂度（L5）**：Lua 比 Go 慢 1.7-2.1x。计算密集时 VM 解释开销显现，且 Lua 中循环的每次迭代都需经过 VM dispatch。
- **内存分配**：Lua 在 1000 items 时比 Go 多约 2000-4000 次分配，主要来自 Lua value 对象的创建。

### 使用建议

- **优先 Lua**：适合快速迭代的业务逻辑（条件判断、简单计算）。1.3x 的开销在大多数推荐系统场景中可以忽略，因为 I/O（Redis、特征查询）才是瓶颈。
- **考虑 Go 原生**：当算子包含密集数值计算（特征工程、复杂评分函数）且处于热路径时，Go 原生算子可带来显著收益。
- **关注 item 规模**：item 越多，每-item 的 Lua VM 开销线性累积。如果单请求 item 数常超过 1000，密集计算应优先考虑 Go 实现。

## 复现

```bash
go test -run='^$' -bench=BenchmarkLuaVsGo -benchmem -count=3 ./benchmarks/
```

Benchmark 代码位于 `benchmarks/bench_lua_vs_go_ops.go` 和 `benchmarks/bench_lua_vs_go_test.go`。
