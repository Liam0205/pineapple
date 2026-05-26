# Differential Fuzz 发现与修复复盘（7 commits）

## 范围

自上次 llmdoc 更新（`bbdc2f2`）至 `1017643`，7 commits 覆盖：

- 30k-round differential fuzz (seed=20260524) 发现 67 divergences
- 3 类根因归类 + 2 个真实 bug 修复 + 1 个生成器修正
- cross-validate Section 3 补 set comparison 防护 + 并行 recall fixture
- CI workflow 合并（weekend-deep-fuzz → nightly 内置周六升级）

## 关键发现

### IEEE 754 -0.0 vs +0.0 在 HashSet/map 中的坑

- **Go**: map 用 `==` 做 float64 key 比较 → -0.0 == +0.0 → 去重
- **Java**: `Double.hashCode(-0.0) = -2147483648` ≠ `Double.hashCode(0.0) = 0` → HashSet 视为不同 key
- **C++**: `go_format_g(-0.0)` → `"-0"` ≠ `go_format_g(0.0)` → `"0"` → 字符串 key 不同

两方修复模式相同：`if (d == 0.0) d = 0.0` 在 dedup key 计算前去掉 sign bit。

### item_defaults 投影时机

Go 的 BuildInput 在 OperatorInput 投影阶段用 defaults 替换 nil → 算子看到替换后的值。C++ 的 Operator::execute 接收的是 raw Frame,需要算子自己查 item_defaults。filter_condition 没查 → nil 与 value=null 匹配 → 错误删除。

Java/Python 无此问题（它们的 execute 接收 OperatorInput 已投影）。

### 并行 recall + paginate 的非确定性

- 两个 recall_static DAG 并行 → item 插入顺序不确定
- filter_paginate 按位置切 → 切到不同 item 子集
- Set comparison 无法解决（不是顺序问题，是内容问题）
- 生成器修正：检测 ≥2 recall + paginate → 在 paginate 前插 stabilizing sort

## 教训

1. **IEEE 754 -0.0 是跨语言 parity 的暗礁** — 每种语言的 hash/equality 语义不同，需在 key 计算层显式归一化
2. **C++ Operator::execute(Frame&) 设计的代价** — 不经过投影层意味着每个算子都要自己处理 defaults，容易遗漏。Go/Java/Python 走 OperatorInput 天然安全
3. **位置依赖操作不能放在非确定性输入后面** — paginate/truncate-with-offset 在并行 DAG 后是语义陷阱
4. **cross-validate 需要 set comparison 防护** — 之前全靠手写 fixture 碰巧不触发并行 recall 而幸存
