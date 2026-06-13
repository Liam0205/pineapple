# Lua vs Go 原生算子性能对比

对比 `transform_by_lua` 在两个 Lua VM 后端(默认 wangshu / opt-in gopher-lua)与等价 Go 原生算子的执行效率。本文使用同机同时段连跑的 benchstat 对照,数据是性能决策的依据,而非头条数字。

## 测试环境

- Intel Xeon 6982P-C, Linux, Go 1.26.2
- `GOMAXPROCS=1`、`-cpu=1`、`-count=10`、benchstat ±% 与 p 值
- 端到端引擎执行(recall_static → 被测算子),含引擎开销
- wangshu v0.1.4(默认后端,CallInto 零分配边界路径,issue #8 关闭)
- gopher-lua v1.1.2(opt-in 后端,`-tags=lua_gopher`)

## 复杂度档次

| 档次 | 名称 | 计算逻辑 | 复杂度要素 |
|------|------|----------|-----------|
| L1 | identity | `return item_x` | 纯字段透传,零计算 |
| L2 | arithmetic | `item_price * 0.85 + 10.0` | 单次乘加 |
| L3 | branching | 4 级 if/elseif 分段折扣 | 条件分支 + 算术 |
| L4 | multi-field | `0.5*a + 0.3*b + 0.2*c`,clamp [0,1] | 多字段读取、多步计算 |
| L5 | iterative | Horner 法 5 次多项式求值 | 循环、多轮浮点运算 |

## 端到端结果(100 items)

| 档次 | Go ns/op | Lua(wangshu) ns/op | Lua(gopher) ns/op | wangshu/Go | gopher/Go | wangshu vs gopher |
|------|---------:|-------------------:|------------------:|-----------:|----------:|------------------:|
| L1 identity | 36,190 | 67,730 | 66,030 | 1.87x | 1.82x | +2.6% (p=0.000) |
| L2 arithmetic | 38,780 | 69,320 | 73,440 | 1.79x | 1.89x | **−5.6%** (p=0.000) |
| L3 branching | 39,080 | 73,790 | 83,060 | 1.89x | 2.13x | **−11.2%** (p=0.000) |
| L4 multi-field | 49,770 | 95,150 | 111,630 | 1.91x | 2.24x | **−14.8%** (p=0.000) |
| L5 iterative | 39,130 | 110,100 | 132,300 | 2.81x | 3.38x | **−16.8%** (p=0.000) |

## 端到端结果(1000 items)

| 档次 | Go ns/op | Lua(wangshu) ns/op | Lua(gopher) ns/op | wangshu/Go | gopher/Go | wangshu vs gopher |
|------|---------:|-------------------:|------------------:|-----------:|----------:|------------------:|
| L1 identity | 437,000 | 667,600 | 678,800 | 1.53x | 1.55x | tie (p=0.481) |
| L2 arithmetic | 413,500 | 681,400 | 746,500 | 1.65x | 1.81x | **−8.7%** (p=0.000) |
| L3 branching | 417,900 | 705,900 | 780,900 | 1.69x | 1.87x | **−9.6%** (p=0.000) |
| L4 multi-field | 554,900 | 993,200 | 1,221,200 | 1.79x | 2.20x | **−18.7%** (p=0.000) |
| L5 iterative | 421,300 | 1,096,000 | 1,507,000 | 2.60x | 3.58x | **−27.2%** (p=0.000) |

## 内存与分配(1000 items,L5 iterative,代表性档次)

| 后端 | B/op | allocs/op |
|------|-----:|----------:|
| Go native | 521.3 KiB | 5,060 |
| Lua wangshu | 538.5 KiB | 6,116 |
| Lua gopher | 813.3 KiB | 9,413 |

wangshu vs gopher: **−33.8% B/op、−35.0% allocs/op**(L5/1000),全部 p=0.000。Go-native 列在两个 Lua tag 下完全一致(`~` p=1.000),验证测量环境对称。

## 分析

### 开销来源

Lua 相对 Go 原生的额外成本来自三层:

1. **VM 解释执行**:gopher-lua 是纯解释器;wangshu 是带 NaN-boxing 与 arena GC 的解释器。两者都没有 JIT,执行速度都比 Go 编译器内联+寄存器分配优化的代码慢。
2. **Go↔Lua 类型转换**:每次 `goToLua` / `luaToGo` 涉及类型断言与值对象分配。
3. **全局变量读写**:Lua 算子通过 globals 传递数据,每个 item 每个字段都有一次 hash table 操作。

### wangshu vs gopher-lua 的差异(同样的 Lua 语义,不同的实现)

wangshu 在以下三处胜出 gopher-lua:

- **VM 内核更快**:NaN-boxing 减少装箱开销;arena GC 让短期分配走 bump-allocate。
- **零分配边界**(v0.1.4 CallInto):每次 `Call` 不再产生 `[]Value` 结果切片堆分配。gopher-lua 的对应路径有 `make([]any, nret)` 这一层固定堆分配。
- **per-item 计算密度越大,优势越明显**:L1 在两后端基本持平(boundary 主导,VM 内核占比小);L5 Horner 循环 wangshu 比 gopher 快 27%(VM 内核占比大,wangshu 内核与零分配复合放大)。

### Lua/Go 比

- **低复杂度 + 多 items**(L1/1000):wangshu Lua 仅比 Go 慢 1.5x。引擎开销与边界传输占比大,VM 计算稀释。
- **高复杂度 + 多 items**(L5/1000):wangshu Lua 比 Go 慢 2.6x;gopher-lua 慢 3.6x。计算密集时 VM 解释开销显现。
- **关键启示**:Go 原生仍是绝对性能上限;Lua 的价值在于业务逻辑的可热改 + 安全沙箱,不是每条指令都比 Go 快。

## 端到端 vs 隔离级:负载形状的稀释效应

引擎框架(DataFrame 构建、DAG 调度、recall 写入、投影)有固定开销。这部分开销稀释 VM 层差异。

**示例**:realistic_for_you_calibrated 真实管道(38 个算子、3000 stub items、一组 17 个 common-mode `transform_by_lua` 控制流谓词),wangshu 与 gopher-lua **端到端统计持平**(p=0.21~0.97):

| fixture | gopher ms/op | wangshu ms/op | delta | p |
|---------|------------:|-------------:|------:|---|
| realistic_for_you_calibrated | 29.94 | 29.96 | tie | 0.977 |
| _calibrated_2c4g | 29.84 | 31.09 | tie | 0.514 |
| _calibrated_itemlua(per-item lua) | 30.84 | 28.49 | tie | 0.219 |

即使 itemlua 变体把每请求 Lua 调用密度推到 3000 次(item-mode 加权打分),**端到端仍持平** —— pipeline 里 Lua 只占 <3% 总耗时,DAG + I/O stub 主导 ~97%。

**结论**:VM 层差异的可见性取决于负载形状。boundary 主导的负载(短脚本、高调用密度)在隔离级最能体现差异;一旦嵌入完整 pipeline,引擎框架就稀释。这与 `llmdoc/memory/decisions/perf-evolution-roadmap.md` 校准事实 #2 一致 —— 现有 fixture 中,common-mode 列内核负载迁移才是 VM 加速可见性的真正闸门。

## 使用建议

- **优先 Lua**:适合快速迭代的业务逻辑(条件判断、简单计算、运营策略)。在多数推荐系统场景中,1.5-2x 的 VM 开销可以忽略,因为 I/O(Redis、特征查询)才是瓶颈。默认 wangshu 就够用。
- **考虑 Go 原生**:当算子包含密集数值计算(特征工程、复杂评分函数)且处于热路径时,Go 原生可带来显著收益(L5 仍有 2.6x 差距)。
- **关注 item 规模 + per-item 计算密度**:item 越多 + per-item 计算越重,VM 开销线性累积。
- **后端切换路径**:默认 wangshu;若需 gopher-lua 行为(例如已知第三方依赖耦合),build 时加 `-tags=lua_gopher`。两后端共享 `Backend/Pool/Engine` 抽象与同一测试套,行为字节级对等。

## 复现

```bash
# 隔离级:全 5 档 × 2 size,默认(wangshu)
go test -run='^$' -bench=BenchmarkLuaVsGo -benchmem -count=10 -cpu=1 ./pine-go/benchmarks/

# 隔离级:gopher-lua 对照
go test -tags=lua_gopher -run='^$' -bench=BenchmarkLuaVsGo -benchmem -count=10 -cpu=1 ./pine-go/benchmarks/

# 端到端 calibrated(需 -tags=pine_bench 启用 stub 算子)
go test -tags=pine_bench -run='^$' -bench=BenchmarkCalibrated -benchmem -count=10 -cpu=1 ./pine-go/benchmarks/

# 自动化两后端对比 + benchstat delta
make bench-lua-backends
# 等价于:scripts/bench-lua-backends.sh
```

详见:
- `pine-go/benchmarks/bench_lua_vs_go_test.go`、`bench_isolated_test.go`(隔离级)
- `pine-go/benchmarks/bench_calibrated_test.go`(端到端 calibrated)
- `scripts/bench-lua-backends.sh`(同机串行连跑两后端)
- `llmdoc/reference/lua-backend.md`(后端选择契约 + CallInto 边界契约)
- `llmdoc/guides/benchmark-hygiene.md`(测量路径对称性等卫生纪律)
