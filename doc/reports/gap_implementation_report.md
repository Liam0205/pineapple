# 设计文档 Gap 实现报告

本报告汇总了设计文档审计后发现的 gap 及其实现情况。共 7 个阶段，涉及 21 个文件，新增/修改约 935 行代码。

---

## Phase 1: Apple DSL 补齐 `common_defaults` + `debug`

**背景**: 设计文档要求算子支持 `common_defaults`（公共字段默认值）和 `debug`（调试模式）参数。Pine 引擎侧已完整实现（`dataframe.BuildInput` 处理 defaults，`config.OperatorConfig` 有 Debug 字段），但 Apple DSL 和 codegen 缺少传递链路。

**改动**:

| 文件 | 内容 |
|------|------|
| `apple/base.py` | `OpCall` 增加 `common_defaults` 和 `debug` 字段；`BaseOp._apply()` 增加对应参数 |
| `apple/flow.py` | `_add_op()` 的 `meta_keys` 增加这两个字段，构造 `OpCall` 时传入 |
| `apple/compiler.py` | 编译时输出 `common_defaults` 和 `debug` 到 JSON |
| `cmd/pineapple-codegen/template.go` | 生成的 Python `__call__` 签名和 `_apply()` 调用增加这两个参数 |

**测试覆盖**:
- `test_compiler.py`: `test_common_defaults_in_json`, `test_item_defaults_in_json`, `test_debug_flag_in_json`, `test_no_defaults_no_debug_omitted`

**使用方式**:
```python
flow.lua(
    common_input=["age"],
    common_output=["result"],
    common_defaults={"age": 25},
    debug=True,
    lua_script="function f() return age end",
    function_for_common="f", function_for_item="",
)
```

---

## Phase 2+3: 白盒化回查（请求级 Trace）+ Debug 日志

**背景**: 设计文档 08 标记为 MVP 必须。每次 DAG 执行自动记录经过了哪些算子、每个算子耗时、是否被跳过；`debug: true` 的算子额外快照 I/O 数据并输出日志。

**改动**:

| 文件 | 内容 |
|------|------|
| `internal/types/trace.go` | **新建** — `OpTrace` 结构体 |
| `internal/types/request.go` | `Result` 增加 `Trace []OpTrace` 字段 |
| `internal/runtime/scheduler.go` | `Run()` 返回 trace 切片；每个算子记录时间戳；skip 记录 `Skipped: true`；debug 模式快照 I/O 并 `log.Printf` |
| `pine.go` | 将 trace 挂到 `Result.Trace` |
| `cmd/pineapple-server/main.go` | `/execute` 响应增加 `trace` 字段 |

**设计要点**:
- Trace 总是记录（算子名 + 耗时 + 是否跳过），零额外分配
- 仅 `debug: true` 的算子快照 I/O（避免大数据量拷贝）
- 日志格式: `[pine:debug] operator=%q duration=%v input=%v output=%v`

**测试覆盖**:
- `scheduler_test.go`: trace 数量、duration > 0、skip 标记验证

**使用方式**: 执行后检查 `Result.Trace`，或通过 HTTP `/execute` 响应的 `trace` 字段查看。

---

## Phase 4: `$code_info` DSL 源码位置跟踪

**背景**: 设计文档要求 Apple 编译时记录每个算子调用的 DSL 源码位置，便于调试和代码审查。

**改动**:

| 文件 | 内容 |
|------|------|
| `apple/base.py` | `BaseOp._apply()` 使用 `inspect.stack()` 捕获调用位置 |
| `apple/flow.py` | `_add_op()` 和 `__getattr__` 路径均捕获 `code_info` |

**格式**: `filename:line in func_name(): .type_name(...)`

**测试覆盖**:
- `test_compiler.py`: `test_code_info_present`, `test_code_info_via_getattr`

**使用方式**: 编译后 JSON 中每个算子的 `$code_info` 字段自动包含源码位置。

---

## Phase 5: Observe 算子

**背景**: 设计文档定义了 Observe 类算子——只读 DataFrame 不修改，用于写入外部系统（日志、监控等）。

**改动**:

| 文件 | 内容 |
|------|------|
| `operators/observe/log.go` | **新建** — `observe_log` 算子：读声明字段写入 Go 标准日志 |
| `operators/observe/log_test.go` | **新建** — Init/SetMetadata/Execute 单测 |
| `operators/all.go` | 注册 observe 包 |
| `apple/validator.py` | observe 算子（无 output）豁免死代码检测 |

**设计要点**:
- 实现 `MetadataAware` 接口获知要读哪些字段
- 无 output（common_output 和 item_output 均空），DAG 中作为叶子节点
- 参数: `log_prefix` (string, optional)

**测试覆盖**:
- `log_test.go`: 5 个测试（Init、默认参数、SetMetadata、Execute 有数据、Execute 空数据）
- `test_validator.py`: `test_observe_op_not_dead_code`, `test_op_with_output_still_detected_as_dead`

**使用方式**:
```python
flow.observe_log(
    common_input=["user_id"],
    item_input=["item_score"],
    log_prefix="debug_checkpoint",
)
```

---

## Phase 6: 运行时执行统计

**背景**: 设计文档要求统计每个算子和控制分支的实际执行情况，用于代码治理和性能分析。

**改动**:

| 文件 | 内容 |
|------|------|
| `internal/runtime/stats.go` | **新建** — `Stats` 累加器，per-operator 原子计数器 |
| `internal/runtime/stats_test.go` | **新建** — 单测 + 并发安全测试 + Run 集成测试 |
| `internal/runtime/scheduler.go` | `Run()` 接受 `*Stats`，记录 exec/skip/error |
| `pine.go` | `Engine` 持有 `*Stats`，暴露 `Stats()` 快照方法 |
| `cmd/pineapple-server/main.go` | 新增 `GET /stats` 端点 |

**统计字段**:
- `exec_count`: 成功执行次数
- `skip_count`: 跳过次数
- `error_count`: 错误次数
- `total_duration_ns`: 累计耗时
- `max_duration_ns`: 单次最大耗时
- `avg_duration_ns`: 平均耗时

**设计要点**:
- `sync/atomic` 原子操作，无锁累加
- Engine 级别累积（跨请求），配置重载时重置
- MaxDurationNs 使用 CAS 循环更新

**测试覆盖**:
- `stats_test.go`: 5 个测试（exec/skip/error 记录、并发安全、Run 集成）

**使用方式**: `GET /stats` 返回 JSON，或代码中调用 `engine.Stats()`。

---

## Phase 7: 写回前类型校验

**背景**: 设计文档要求引擎在写回 DataFrame 前做类型校验，防止非法类型数据污染 DataFrame。

**改动**:

| 文件 | 内容 |
|------|------|
| `internal/dataframe/dataframe.go` | `ApplyOutput` 中对所有写入值调用 `validateValue`；新增 `validateValue` 函数 |
| `internal/dataframe/dataframe_test.go` | 新增类型校验测试 |

**支持的类型白名单**:
- `nil`, `bool`
- 所有整数类型 (`int`, `int8`...`int64`, `uint`...`uint64`)
- 浮点类型 (`float32`, `float64`)
- `string`
- `[]any`, `map[string]any`
- 其他 slice/map 类型（通过 reflect fallback）

**校验范围**: common writes、item writes、added items

**测试覆盖**:
- `dataframe_test.go`: 表驱动测试覆盖所有合法和非法类型（10 种），以及 item write 和 added item 路径

---

## 总览

| Phase | 提交 | 新增文件 | 修改文件 |
|-------|------|----------|----------|
| 1 | `1ca6e40` | 0 | 5 |
| 2+3 | `27dc344` | 2 | 4 |
| 4 | `4e734be` | 0 | 2 |
| 5 | `0dd3270` | 2 | 3 |
| 6 | `ce705ad` | 2 | 4 |
| 7 | `f110df8` | 0 | 2 |

**总计**: 21 文件变更，+935 行
