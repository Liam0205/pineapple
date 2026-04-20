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

### 自定义壳子（完全控制）

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

### 通过 `pkg/server` 注入（推荐）

`server.Config` 支持传入预注册的 `*resource.Manager`。调用方在 `Run()` 之前完成所有 `Register()` 调用，`Run()` 负责 `Start()` 和 `Stop()`：

```go
func main() {
    rm := resource.NewManager()
    rm.Register("feed_data", fetchFeedData, 10*time.Minute)
    rm.Register("abtest_config", fetchABConfig, 30*time.Second)

    server.Run(server.Config{
        ConfigPath: *configPath,
        Addr:       *addr,
        Resources:  rm,
    })
}
```

若 `Config.Resources` 为 nil，`Run()` 内部创建空 Manager（向后兼容）。

## FetcherFactory 注册机制

对称于算子注册（`RegisterOperator`），资源类型也通过全局 registry 注册。业务代码在 `init()` 中注册 FetcherFactory，由配置文件驱动实例化。

```go
// FetcherFactory 根据配置参数创建 Fetcher。
type FetcherFactory func(params map[string]any) (Fetcher, error)

// RegisterFetcher 注册资源类型的工厂函数。
// typeName 与配置文件中的 "type" 字段匹配。
// 重复注册同名 type 时 panic（和算子注册行为一致）。
func RegisterFetcher(typeName string, factory FetcherFactory)
```

业务代码示例：

```go
func init() {
    resource.RegisterFetcher("feed_data", func(params map[string]any) (resource.Fetcher, error) {
        dsn, _ := params["mysql_dsn"].(string)
        if dsn == "" {
            return nil, fmt.Errorf("feed_data: mysql_dsn is required")
        }
        db, err := dao.GetDB(dsn)
        if err != nil {
            return nil, err
        }
        return NewFeedDataFetcher(db), nil
    })
}
```

## JSON 配置驱动的资源加载

`Manager.LoadConfig(data)` 从 JSON 配置批量注册资源，取代在 `main.go` 中硬编码 `Register()` 调用。

```go
func (m *Manager) LoadConfig(data []byte) error
```

JSON 格式：

```json
{
  "feed_data": {
    "type": "feed_data",
    "interval": 600,
    "params": {
      "mysql_dsn": "user:pass@tcp(127.0.0.1:3306)/tipsy?..."
    }
  }
}
```

- `type`：对应 `RegisterFetcher` 注册的类型名
- `interval`：刷新间隔（秒）
- `params`：传递给 `FetcherFactory` 的参数，由业务自行定义

`LoadConfig` 逻辑：

1. 解析 JSON
2. 对每个资源名：从全局 registry 查找对应 `FetcherFactory`
3. 调用 `factory(params)` 创建 `Fetcher`
4. 调用 `m.Register(name, fetcher, interval)` 注册

必须在 `Start()` 之前调用。与手动 `Register()` 兼容——可以先手动注册一些，再通过配置文件加载其余的。

## Names 方法

```go
func (m *Manager) Names() []string
```

返回已注册资源名列表。用于启动阶段校验：对比 pipeline 配置中引用的 `resource_name` 与 `Names()` 的返回值，尽早发现配置缺失。

## 资源依赖校验

```go
func ValidateResourceDeps(pipelineConfig []byte, rm *Manager) error
```

在启动阶段，从 pipeline JSON 配置中提取所有算子的 `resource_name` 参数值，与 `rm.Names()` 交叉检查。若存在 pipeline 引用但 ResourceManager 中未注册的资源名，返回明确错误。

这避免了运行时才在 `Execute()` 中发现资源缺失的问题——尽早 fail fast。

使用位置：壳子在 `rm.Start()` 成功后、启动 HTTP 监听前调用。

## 通用 Server 配置驱动资源

`server.Config` 新增 `ResourceConfigPath` 字段：

```go
type Config struct {
    ConfigPath         string
    ResourceConfigPath string // 资源配置 JSON 文件路径
    Addr               string
    Resources          *resource.Manager
}
```

`Run()` 流程更新：

1. 若 `ResourceConfigPath` 非空，读取文件并调用 `rm.LoadConfig(data)`
2. `rm.Start()`
3. 加载 pipeline engine
4. 调用 `ValidateResourceDeps(pipelineData, rm)`，缺失则 fatal
5. 启动 HTTP server

与手动 `Register()` 完全兼容：调用方可以同时在 `Config.Resources` 上手动 `Register()` 并提供 `ResourceConfigPath`。

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
