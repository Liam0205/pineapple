# 服务层资源管理器设计

> 本文档描述的是**服务层**（壳子）的设计，不属于 Pineapple (Pine/Apple) 核心。
> 当服务需要持有动态刷新的内存资源（索引、配置等）供算子使用时，参考本方案。

## 背景

部分算子在执行时需要读取周期性更新的数据，例如：

- 特征索引表（定时从存储/服务拉取）
- AB 实验配置（定时刷新）
- 轻量级模型参数

这类数据的特点：

- **数据量不大**，可以整体放在内存中
- **需要定时刷新**，但刷新频率远低于请求频率
- **读多写少**，高并发读、低频写

## 方案：独立 ResourceManager + context 注入

### 架构

```
壳子 (HTTP / RPC / Runner)
 ├── ResourceManager          ← 独立生命周期，后台定时刷新
 │     ├── resource_a         → atomic.Pointer[T]
 │     └── resource_b         → atomic.Pointer[T]
 │
 └── Pine Engine              ← 不可变，可随时原子替换
```

ResourceManager 与 Engine 的生命周期完全独立：

- Pipeline 配置变更 → 替换 Engine，ResourceManager 不受影响
- 资源刷新 → 更新 ResourceManager 内部指针，Engine 不受影响

### 接口设计

```go
// ResourceProvider 是算子侧看到的只读接口。
type ResourceProvider interface {
    // Get 按名字获取资源。资源不存在或尚未就绪时返回 nil, false。
    Get(name string) (any, bool)
}

// ResourceManager 是壳子侧持有的完整管理器。
type ResourceManager struct {
    mu        sync.RWMutex
    resources map[string]*managedResource
}

type managedResource struct {
    name     string
    ptr      atomic.Pointer[any]   // 当前版本，无锁读
    fetcher  func(ctx context.Context) (any, error)
    interval time.Duration
    cancel   context.CancelFunc    // 停止刷新 goroutine
}
```

### context 注入

```go
type resourceCtxKey struct{}

// WithResources 由壳子在每次请求时调用，将 ResourceProvider 注入 context。
func WithResources(ctx context.Context, rp ResourceProvider) context.Context {
    return context.WithValue(ctx, resourceCtxKey{}, rp)
}

// ResourcesFromContext 由算子在 Execute 中调用。
func ResourcesFromContext(ctx context.Context) ResourceProvider {
    rp, _ := ctx.Value(resourceCtxKey{}).(ResourceProvider)
    return rp
}
```

### 算子使用示例

```go
func (o *MyOp) Execute(ctx context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
    rm := ResourcesFromContext(ctx)
    if rm == nil {
        return fmt.Errorf("resource manager not available")
    }

    idx, ok := rm.Get("user_feature_index")
    if !ok {
        // 资源不可用时的降级逻辑
        out.SetCommon("score", 0.0)
        return nil
    }

    table := idx.(*FeatureTable)
    // ... 使用 table 查询特征
    return nil
}
```

### 壳子侧使用

```go
func main() {
    rm := NewResourceManager()

    // 注册需要定时刷新的资源
    rm.Register("user_feature_index", fetchFeatureTable, 5*time.Minute)
    rm.Register("abtest_config", fetchABConfig, 30*time.Second)

    // 启动所有刷新 goroutine
    rm.Start(context.Background())
    defer rm.Stop()

    // 请求处理
    http.HandleFunc("/execute", func(w http.ResponseWriter, r *http.Request) {
        ctx := WithResources(r.Context(), rm)
        result, err := engine.Execute(ctx, req)
        // ...
    })
}
```

## 设计要点

### 1. 刷新失败保留旧版本

刷新 goroutine 拉取新数据失败时，不清空旧数据，继续使用上一次成功的版本。记录日志和错误计数。

### 2. 资源缺失由算子决策

ResourceProvider.Get 返回 `nil, false` 时，算子自行决定是报错还是降级。框架不强制统一行为。

### 3. 无锁读

使用 `atomic.Pointer` 实现读写分离。读路径零锁竞争，不影响请求延迟。刷新 goroutine 构建完整新版本后原子替换指针。

### 4. 可测试性

算子依赖 `ResourceProvider` 接口而非具体 ResourceManager。测试时注入 mock：

```go
type mockResources struct {
    data map[string]any
}

func (m *mockResources) Get(name string) (any, bool) {
    v, ok := m.data[name]
    return v, ok
}
```

### 5. 优雅关闭

`ResourceManager.Stop()` 取消所有刷新 goroutine 的 context，等待它们退出。确保壳子关闭时不泄漏 goroutine。
