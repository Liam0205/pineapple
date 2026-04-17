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

## 各场景处理

### 算子请求外部服务失败（可恢复）

算子自行处理。例如召回算子调用外部索引超时：

- 算子返回空的 item 列表（降级结果）
- 同时返回错误信息，由引擎记录日志
- DAG 继续执行，后续算子基于空列表运行

这是算子开发者的责任——Go 算子内部应做好超时、重试、降级逻辑。

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
- 捕获 panic → 记录错误（含堆栈信息）→ 终止当前 DAG
- Pine 进程不受影响，继续服务其他请求

## Go 算子接口

```go
type OperatorOutput struct {
    // 算子正常输出的数据
    CommonOutput map[string]any
    ItemOutput   []map[string]any  // 行存模式

    // 可恢复错误：算子自行降级后，附带的错误信息
    // 非 nil 时引擎记录日志但 DAG 继续
    Warning error
}

type Operator interface {
    Execute(ctx context.Context, input *OperatorInput) (*OperatorOutput, error)
    // 返回 error (非 Warning) 时，引擎终止当前 DAG
}
```

- `return output, nil`：正常执行
- `return output, nil` + `output.Warning != nil`：可恢复，返回了降级结果 + 警告
- `return nil, err`：不可恢复，DAG 终止
