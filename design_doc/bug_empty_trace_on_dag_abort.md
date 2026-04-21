# Bug: DAG 中止时 trace 输出空条目

## 现象

当 DAG 执行因某个算子报错而中止时，响应的 `trace` 数组中包含若干 `name` 为空字符串、`duration_ms` 为零的条目。这些条目对应从未执行的算子，是不必要的干扰项。

## 根因分析

### 1. trace 切片预分配

`internal/runtime/scheduler.go` 的 `Run` 函数在执行前按 DAG 节点总数预分配 trace 切片：

```go
traces = make([]types.OpTrace, n)
```

Go 的零值语义意味着每个 `OpTrace` 初始即为 `{Name: "", Duration: 0, ...}`。

### 2. 错误导致提前退出

每个节点的 goroutine 在等待前驱完成时监听 context：

```go
for _, pred := range node.Preds {
    select {
    case <-done[pred]:
    case <-ctx.Done():
        return
    }
}
```

当某个算子执行失败并调用 `cancel()` 后，所有阻塞在前驱等待上的下游 goroutine 走 `ctx.Done()` 分支直接 return，不会写入 trace。它们在预分配切片中的位置保持零值。

### 3. 未过滤直接返回

`Run` 函数 `wg.Wait()` 后直接返回整个预分配的 `traces` 切片。`pine.go` 的 `Engine.Execute` 将其原样赋给 `result.Trace`，零值条目随响应输出。

## 修复建议

在 `internal/runtime/scheduler.go` 的 `Run` 函数末尾、`return` 之前，过滤掉未执行的条目：

```go
wg.Wait()

var finalTraces []types.OpTrace
for _, t := range traces {
    if t.Name != "" {
        finalTraces = append(finalTraces, t)
    }
}

return warnings, finalTraces, fatalErr
```

`Name == ""` 是安全的判定条件：每个实际执行（或被 skip）的算子都会写入 `Name`，只有从未开始执行的算子才保持零值。

改动局部且无副作用——不影响正常执行路径（所有算子都执行时 `Name` 全部非空，过滤不删除任何条目）。

## 修复

已在 `internal/runtime/scheduler.go` 的 `Run` 函数末尾实施上述过滤方案。同步补充了 `internal/runtime/scheduler_test.go` 中 `TestRunFatalError` 的 trace 内容断言。
