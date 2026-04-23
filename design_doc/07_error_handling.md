# 错误处理

## 核心原则

1. **算子自行处理可恢复错误**：返回降级结果 + 错误信息，DAG 继续执行。
2. **不可恢复错误终止 DAG**：报出清晰的错误信息，当前请求的 DAG 终止。
3. **绝不 crash 进程**：无论发生什么逻辑错误，Pine 进程不能崩溃。即使算子代码 panic，引擎必须 recover。

## 错误分级

| 级别 | 处理方式 | 示例 |
|------|----------|------|
| 可恢复 | 算子自行处理，返回降级结果 + 错误信息，DAG 继续 | 召回服务超时 → 返回空 item 列表 |
| 不可恢复 | 引擎终止当前 DAG，报出清晰的错误 | Lua 脚本运行时出错、算子输出类型不匹配 |
| panic | 引擎 recover，终止当前 DAG，记录错误 | 算子代码 bug 导致空指针等 |

## 类型化错误

Pine 在 `internal/types/errors.go` 中定义了 5 种结构化错误类型，调用方可通过 `errors.As` 精确判断错误来源：

| 错误类型 | 阶段 | 含义 | 示例 |
|----------|------|------|------|
| `ConfigError` | 引擎加载 | JSON 配置结构或版本问题 | 缺少 `pipeline_config`、版本号不匹配 |
| `RegistryError` | 引擎加载 | 算子注册或参数校验问题 | `type_name` 未注册、必填参数缺失、类型不匹配 |
| `ValidationError` | 引擎加载 / 请求校验 | 配置校验或请求 contract 校验失败 | DAG 中有环、`data_parallel` 约束违反、请求缺少 common 字段 |
| `ExecutionError` | DAG 执行 | 算子返回 error（不可恢复） | Lua 脚本出错、算子输出类型约束违反 |
| `PanicError` | DAG 执行 | 算子 panic 被 recover | 空指针解引用、数组越界 |

`ExecutionError` 和 `PanicError` 均包含 `Operator` 字段，标识出错的算子名。`PanicError` 额外包含 `Stack` 字段（完整堆栈信息）。

## 各场景处理

### 算子请求外部服务失败（可恢复）

算子自行处理。例如召回算子调用外部索引超时：

- 算子返回空的 item 列表（降级结果）
- 同时返回错误信息，由引擎记录日志
- DAG 继续执行，后续算子基于空列表运行

这是算子开发者的责任——Go 算子内部应做好超时、重试、降级逻辑。

### `fail_on_error` 降级模式

部分算子通过 `fail_on_error` 参数支持显式降级切换：

- `fail_on_error=true`（默认）：错误直接 `return err` → 引擎终止 DAG
- `fail_on_error=false`：错误转为 `output.SetWarning(err)` → DAG 继续，结果中携带警告

当前实现此模式的算子：`transform_by_remote_pineapple`。其他需要降级能力的算子可参考同一模式：

```go
func (o *MyOp) handleError(out *pine.OperatorOutput, err error) error {
    if o.failOnError {
        return err
    }
    out.SetWarning(err)
    return nil
}
```

### Lua 脚本运行时出错（不可恢复）

Lua 脚本中发生除零、类型错误、函数未定义等：

- 引擎捕获 Lua 错误
- 报出清晰的错误信息（含算子名、Lua 脚本位置、错误详情）
- 终止当前 DAG

### 算子输出类型不匹配（不可恢复）

算子返回的数据类型与 schema 或 `$metadata` 声明不一致：

- 引擎在写回 DataFrame 前做类型校验
- 校验失败 → 报错终止当前 DAG

### 算子代码 panic（保护进程）

算子 Go 代码中的未预期 panic（空指针、越界等）：

- 引擎在调用每个算子时用 `recover()` 保护
- 捕获 panic → 包装为 `PanicError`（含完整堆栈）→ 终止当前 DAG
- Pine 进程不受影响，继续服务其他请求

### 请求级 contract 校验

`Engine.Execute` 在 DAG 执行前，根据 `flow_contract` 校验请求数据：

| 条件 | 结果 |
|------|------|
| `req == nil` | `ValidationError` |
| `req.Common == nil` | `ValidationError` |
| `flow_contract.common_input` 中的字段在 `req.Common` 中缺失 | `ValidationError`（标明缺失字段名） |
| `flow_contract.item_input` 中的字段在某个 item 中缺失 | `ValidationError`（标明 item 索引和缺失字段名） |

这些校验在 DAG 调度之前执行。校验失败时不会执行任何算子，直接返回错误。

## Warning 收集路径

Warning 从算子产生到最终响应的完整传播路径：

```
算子调用 output.SetWarning(err)
    │
    ▼
scheduler 检查 output.GetWarning()
    │  append 到 []Warning（mutex 保护）
    ▼
Engine.Execute 将 []Warning 转为 Result.Warnings ([]error)
    │
    ▼
HTTP handler 将 Warnings 序列化为响应 JSON 的 "warnings" 数组
```

关键性质：

- Warning 不中止 DAG——产生 warning 的算子正常写回 DataFrame，后续算子继续执行。
- 一次请求可以产生多个 warning（来自不同算子），全部收集后一起返回。
- 并发安全：scheduler 用 `sync.Mutex` 保护 warnings slice。

## DAG 中止机制

当某个算子返回不可恢复错误或发生 panic 时：

1. 首个 fatal error 通过 `sync.Once` 保证只记录一次，包装为 `ExecutionError` 或 `PanicError`。
2. 同时调用派生 context 的 `cancel()`。
3. 正在执行的其他算子通过 `ctx.Done()` 感知取消，应尽快退出。
4. 尚未调度的下游算子不会被启动。
5. `Engine.Execute` 返回 fatal error + 部分 Result（已完成算子的结果可用于调试）。

**未出现在 trace 中的算子**：DAG 中止后，未开始执行的算子不会出现在 `Result.Trace` 中。`trace` 仅包含实际执行过或被 skip 的算子。

## HTTP 错误映射

HTTP 壳子（`pkg/server`）将引擎错误映射为 HTTP 状态码：

| 场景 | HTTP Status | 响应 body |
|------|-------------|-----------|
| Engine 未加载 | 503 Service Unavailable | 纯文本错误 |
| 请求 JSON 解析失败 | 400 Bad Request | `{"error": "invalid request: ..."}` |
| Engine.Execute 返回 error | 500 Internal Server Error | `{"error": "...", "common": ..., "items": ...}` |
| 正常执行，有 warnings | 200 OK | `{"common": ..., "items": ..., "warnings": [...]}` |
| 正常执行，无 warnings | 200 OK | `{"common": ..., "items": ...}` |
| 不支持的 HTTP 方法 | 405 Method Not Allowed | 纯文本错误 |

注意：即使 Execute 返回 error（500），响应 body 中仍可能包含部分 `common` / `items` 数据（DAG 中止前已完成的算子结果）。这是为了便于调试。

## Apple 编译期错误

Apple/Python DSL 编译器在 `compile_flow` 时执行一系列校验，错误通过 Python `ValidationError` 异常抛出：

| 校验项 | 说明 |
|--------|------|
| 下划线前缀保留 | `common_output` / `item_output` 中的字段名不得以 `_` 开头 |
| 死代码检测 | 算子的 `item_output` 中存在 flow 级 `item_output` 未声明的字段 → 警告 |
| `data_parallel` 约束 | `data_parallel > 1` 时必须是 Transform 类型且 `common_output` 为空 |
| 参数-元数据一致性 | 业务参数与元数据声明必须匹配（如 `transform_resource_lookup` 的 `lookup_key` 须在 `item_input` 中） |
| 字段覆盖检测 | 同一 `common_output` / `item_output` 字段被多个算子写入 → 警告 |
| 控制流完整性 | `if_()` 必须有配对的 `end_if_()`，空分支检测 |
| 资源引用校验 | `resource_name` 参数必须引用已通过 `flow.resource()` 声明的资源 |

Apple 编译期校验与 Go 引擎加载期校验形成**两层守门**：Apple 在用户运行 Python 脚本时即刻报错（快速反馈），Go 在 `NewEngine` 加载 JSON 时再次校验（拦截手写 JSON 或其他来源绕过 DSL 的配置）。

## Go 算子接口

```go
type Operator interface {
    Init(params map[string]any) error
    Execute(ctx context.Context, input *OperatorInput, output *OperatorOutput) error
}
```

OperatorOutput 提供 accessor 方法（详见 [03 数据抽象](03_data_abstraction.md#go-算子接口)）：

```go
// 字段级操作
output.SetCommon(field, value)      // 写入 common 字段
output.SetItem(index, field, value) // 写入 item 字段
// 结构性操作
output.AddItem(fields)              // 新增 item 行（Recall、Merge）
output.RemoveItem(index)            // 标记删除 item 行（Filter、Merge）
output.SetItemOrder(newOrder)       // 重排 item（Sort、Reorder）
// 错误处理
output.SetWarning(err)              // 设置可恢复错误
```

错误约定：

- `return nil`：正常执行
- `return nil` + `output.SetWarning(err)`：可恢复，返回了降级结果 + 警告，引擎记录日志但 DAG 继续
- `return err`：不可恢复，DAG 终止
