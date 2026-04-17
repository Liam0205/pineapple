# 动态资源管理 (ResourceManager)

## 定位

`pkg/resource` 是 Pineapple 提供的公开包，为壳子层（HTTP / RPC / Runner 等任意形态）提供动态内存资源的生命周期管理。任何壳子都可以直接 import 使用，不局限于特定协议或部署方式。

```
壳子 (HTTP / RPC / Runner / ...)
 ├── ResourceManager          ← pkg/resource，独立生命周期，后台定时刷新
 │     ├── resource_a         → atomic.Pointer （无锁读）
 │     └── resource_b         → atomic.Pointer
 │
 └── Pine Engine              ← 不可变，可随时原子替换
```

ResourceManager 与 Engine 的生命周期完全独立：
- Pipeline 配置变更 → 替换 Engine，ResourceManager 不受影响
- 资源刷新 → 更新 ResourceManager 内部指针，Engine 不受影响

## 背景

部分算子在执行时需要读取周期性更新的数据，例如：

- 特征索引表（定时从存储/服务拉取）
- AB 实验配置（定时刷新）
- 轻量级模型参数

这类数据的特点：

- **数据量不大**，可以整体放在内存中
- **需要定时刷新**，但刷新频率远低于请求频率
- **读多写少**，高并发读、低频写

## 接口设计

### ResourceProvider（算子侧只读接口）

```go
type ResourceProvider interface {
    Get(name string) (any, bool)
}
```

算子只依赖此接口，不感知刷新逻辑。

### ResourceManager（壳子侧完整管理器）

```go
rm := resource.NewManager()

// 注册资源及其刷新策略
rm.Register("user_feature_index", fetchFeatureTable, 5*time.Minute)
rm.Register("abtest_config", fetchABConfig, 30*time.Second)

// 启动后台刷新（首次同步拉取，后续异步定时刷新）
rm.Start(ctx)
defer rm.Stop()
```

ResourceManager 自身实现 `ResourceProvider` 接口，可直接注入 context。

### Context 注入

```go
// 壳子在每次请求时注入
ctx = resource.WithResources(ctx, rm)

// 算子在 Execute 中提取
rp := resource.FromContext(ctx)
val, ok := rp.Get("user_feature_index")
```

通过 `context.Context` 传递，与 Pine 的 `Engine.Execute(ctx, req)` 自然对接。

## 算子使用示例

```go
func (o *MyOp) Execute(ctx context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
    rp := resource.FromContext(ctx)
    if rp == nil {
        // 未注入 ResourceProvider，降级处理
        out.SetCommon("score", 0.0)
        return nil
    }

    idx, ok := rp.Get("user_feature_index")
    if !ok {
        // 资源尚未就绪，降级
        out.SetCommon("score", 0.0)
        return nil
    }

    table := idx.(*FeatureTable)
    // 使用 table 查询特征 ...
    return nil
}
```

## 壳子集成示例

```go
func main() {
    rm := resource.NewManager()
    rm.Register("user_feature_index", fetchFeatureTable, 5*time.Minute)
    rm.Start(context.Background())
    defer rm.Stop()

    // 请求处理
    http.HandleFunc("/execute", func(w http.ResponseWriter, r *http.Request) {
        ctx := resource.WithResources(r.Context(), rm)
        result, err := engine.Execute(ctx, req)
        // ...
    })
}
```

## 设计要点

### 1. 首次加载同步，后续异步

`Start()` 对每个已注册资源执行一次同步拉取。若首次拉取失败，`Start()` 返回错误，壳子可决定是否继续启动。后续刷新异步进行。

### 2. 刷新失败保留旧版本

后台刷新拉取新数据失败时，不清空旧数据，继续使用上一次成功的版本。记录日志和错误。

### 3. 资源缺失由算子决策

`ResourceProvider.Get` 返回 `nil, false` 时，算子自行决定是报错还是降级。框架不强制统一行为。

### 4. 无锁读

使用 `atomic.Value` 实现读写分离。读路径零锁竞争，不影响请求延迟。刷新 goroutine 构建完整新版本后原子替换。

### 5. 可测试性

算子依赖 `ResourceProvider` 接口而非具体 ResourceManager。测试时注入 mock：

```go
mock := resource.NewStatic(map[string]any{
    "user_feature_index": myTestTable,
})
ctx := resource.WithResources(ctx, mock)
```

### 6. 优雅关闭

`Stop()` 取消所有刷新 goroutine 的 context，等待退出。确保壳子关闭时不泄漏 goroutine。
