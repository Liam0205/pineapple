---
name: new-engine
description: 构造一个新语言的 pine-foo 引擎实现，遵循「先对齐再重构」策略确保行为一致性。
---

# new-engine

指导如何从零构建一个新语言版本的 Pineapple 引擎（如 pine-rust, pine-python），基于 pine-java 实现经验。

## 核心策略：先对齐，再重构

Pine-Java 验证了这一路径的有效性：

1. **Phase 1 — 逐字翻译**：以 Go 实现为 ground truth，直接翻译数据结构和逻辑，不追求目标语言的惯用写法。
2. **Phase 2 — 行为对齐**：通过 cross-validate 字节级对比消除 wire format 差异。
3. **Phase 3 — 语言重构**：在行为完全一致的前提下，应用目标语言的最佳实践。

这种顺序确保了每一步的重构都有 cross-validate 作为安全网，不会引入行为回归。

## 目录结构

```
pineapple/
├── pine-go/           # ground truth (参考实现)
├── pine-java/         # 已完成的对等实现
├── pine-foo/          # 新引擎
├── fixtures/          # 共享测试 fixtures (语言无关)
│   ├── operators/     # 单算子 fixture: {operator, cases: [{params, input, expected}]}
│   ├── pipelines/     # 多算子 pipeline fixture: {config, cases: [{request, expected}]}
│   └── errors/        # 错误场景 fixture: {config, expected_error}
└── scripts/
    └── cross-validate.sh  # 跨引擎验证脚本
```

## 实现顺序（从 pine-java 经验总结）

### Step 1: 基础框架

1. **数据模型**: Frame (common + items), ColumnFrame (列式存储)
2. **Config 解析**: JSON 反序列化为 Config 结构
3. **Registry**: 算子注册表 (name → factory)
4. **Engine.create()**: config → validated pipeline → Engine instance

### Step 2: 算子实现（按复杂度递增）

优先级顺序（基于 fixture 覆盖和依赖关系）：

| 批次 | 算子 | 原因 |
|------|------|------|
| 1 | transform_set, filter_condition | 最简单，验证基础框架 |
| 2 | reorder_sort, filter_truncate, filter_paginate | 单一职责，参数简单 |
| 3 | merge_dedup, recall_static | 涉及 ResourceProvider |
| 4 | transform_by_lua | 复杂度最高，需要嵌入 Lua |
| 5 | data_parallel, subflow | DAG 调度相关 |

每个算子实现后立即运行对应的 operator fixture 测试。

### Step 3: DAG 调度器

1. 拓扑排序
2. data_parallel 并行执行
3. subflow 嵌套 pipeline
4. barrier / skip 语义

### Step 4: Shell 层（CLI + Server）

1. **RunCli**: `-config`, `-request`, `-static-resources` → pretty-print JSON 输出
2. **RenderDAGCli**: `-config`, `-format dot|mermaid`, `-collapse N`
3. **PineServer**: HTTP endpoints `/health`, `/execute`, `/stats`, `/dag`

## 关键对齐点（Pine-Java 踩过的坑）

### JSON wire format

Go 的 `encoding/json` 有特殊行为，目标语言必须显式对齐：

| 行为 | Go 默认 | 需要手动实现 |
|------|---------|-------------|
| HTML-safe 转义 | `<` → `<`, `>` → `>`, `&` → `&` | 是 |
| U+2028/U+2029 转义 | ` `, ` ` | 是 |
| Map key 排序 | 字母序 | 是 |
| 空对象/数组 | `{}`, `[]` (不是 `{ }`) | 看目标 JSON 库 |
| Pretty-print 缩进 | 2 space, `": "` (冒号后空格) | 是 |
| 大写 hex | 小写 `<` (不是 `<`) | 是 |

### 数值精度

- Go `json.Unmarshal` 默认将 JSON number 解析为 `float64`
- 整数保真: 如果目标语言区分 int/float，确保 `83` 不变成 `83.0`

### 错误输出格式

- CLI 错误: 简短单行消息 + exit(1)，不要 stack trace
- 格式: `"error reading config: " + message`
- Server 错误: JSON `{"error": "message"}` + 对应 HTTP status

### HTTP 行为

- `/health` 只接受 GET，其他方法 → 405
- `/execute` 只接受 POST
- 缺少 `common` 字段 → 400 ValidationError（不要默认给空 map）
- Response body 末尾有 `\n`
- Content-Length = JSON bytes + 1 (for trailing newline)

## Phase 3 重构检查清单

在 cross-validate 全绿之后，可以安全地进行：

- [ ] 缩窄异常捕获（`catch(Exception)` → 具体类型）
- [ ] 字段可见性（package-private → private + getter）
- [ ] 线程安全（final 字段、不可变集合）
- [ ] 语言惯用命名（Go camelCase → 目标语言 convention）
- [ ] 性能优化（对象池、缓存等）

每次重构后重跑 cross-validate 确保无回归。

## 完成标准

- [ ] `scripts/cross-validate.sh` 新引擎与 Go 全部对比通过
- [ ] 所有 operator fixtures 通过
- [ ] 所有 pipeline fixtures 通过
- [ ] 所有 error fixtures 通过
- [ ] Server HTTP 行为一致（method guard, status code, response format）
- [ ] CLI 输出字节级一致（pretty-print, error format）
