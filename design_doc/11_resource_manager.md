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

统一配置文件中同时包含 pipeline 和资源配置，server 只需一个 config 路径：

```go
func main() {
    server.Run(server.Config{
        ConfigPath: *configPath,
        Addr:       *addr,
    })
}
```

`Run()` 内部从统一 JSON 提取 `resource_config`，自动创建 Manager、加载、启动、校验依赖。

若需要在配置文件之外额外手动注册资源，可传入预注册的 `Config.Resources`。

## ResourceSchema 注册

与算子注册（`pine.Register(OperatorSchema, factory)`）完全对称，资源类型也通过 schema + factory 注册。

```go
// ResourceSchema 描述资源类型的元信息，供注册、codegen 和文档生成使用。
type ResourceSchema struct {
    Name            string                 // 资源类型名（如 "feed_data"）
    Description     string                 // 一行描述
    DefaultInterval int                    // 默认刷新间隔（秒），0 → 10min
    Params          map[string]ParamSpec   // 复用算子的 ParamSpec
}

// FetcherFactory 根据配置参数创建 Fetcher。
type FetcherFactory func(params map[string]any) (Fetcher, error)

// 注册入口（顶层 pine 包便利函数）
pine.RegisterResource(schema pine.ResourceSchema, factory resource.FetcherFactory)
```

业务代码在 `init()` 中注册，放在业务仓库的 `resources/` 目录下（与 `operators/` 对称）：

```go
// resources/feed_data.go
func init() {
    pine.RegisterResource(pine.ResourceSchema{
        Name:            "feed_data",
        Description:     "Full recommend_feed_item table with bitmap indices",
        DefaultInterval: 600,
        Params: map[string]pine.ParamSpec{
            "mysql_dsn": {Type: "string", Required: true, Description: "MySQL connection DSN"},
        },
    }, func(params map[string]any) (resource.Fetcher, error) {
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

重复注册同名资源类型时 panic（和算子注册行为一致）。

`resource.All()` 返回所有已注册的 ResourceSchema，供 codegen 读取。

## Codegen 生成 Python 资源类

与算子 codegen 完全对称：Go ResourceSchema → codegen → Python typed 资源类。

codegen 读取 `resource.All()`，为每个 ResourceSchema 生成 Python 类：

```python
# apple_generated/resources.py（codegen 生成，勿手动编辑）
from apple.base import BaseResource

class FeedDataResource(BaseResource):
    """Resource: feed_data — Full recommend_feed_item table with bitmap indices."""
    _name = "feed_data"
    _default_interval = 600
    _params_schema = {"mysql_dsn": {"type": "string", "required": True}}

    def __init__(self, *, mysql_dsn: str, interval: int = 600):
        super().__init__(interval=interval, mysql_dsn=mysql_dsn)
```

`BaseResource` 是 Apple DSL 提供的基类（`apple/base.py`）。

## DSL 资源声明

用户在 pipeline Python 文件中使用 codegen 生成的资源类声明资源：

```python
from apple import Flow
from apple_generated.resources import FeedDataResource

flow = Flow("recommend_feed", ...)

# 声明资源
flow.resource("feed_data", FeedDataResource(
    mysql_dsn="user:pass@tcp(127.0.0.1:3306)/tipsy?charset=utf8mb4&parseTime=True",
))

# 算子引用资源
flow.recall_feed_data(resource_name="feed_data", ...)
```

`Flow.resource(name, res)` 记录资源声明，编译时输出到统一 JSON。

### 编译期校验

DSL 编译器在 `compile_flow()` 中校验：所有算子 `params` 中的 `resource_name` 值必须在 `flow._resources` 中有对应声明。缺失则抛出 `ValidationError`。

这将资源配置缺失从运行时 fail 前移到编译期。

## 统一配置格式

编译后的 JSON 包含 `resource_config` 顶层字段，与 pipeline 配置合并为一个文件：

```json
{
  "_PINEAPPLE_VERSION": "0.2.8",
  "_PINEAPPLE_CREATE_TIME": "...",
  "pipeline_config": { ... },
  "pipeline_group": { ... },
  "flow_contract": { ... },
  "resource_config": {
    "feed_data": {
      "type": "feed_data",
      "interval": 600,
      "params": {
        "mysql_dsn": "user:pass@tcp(127.0.0.1:3306)/tipsy?..."
      }
    }
  }
}
```

Go 侧加载：

```go
// LoadFromRootConfig 从统一 JSON 配置中提取 resource_config 并注册。
// 若 resource_config 不存在或为空，不报错（pipeline 可能不依赖资源）。
func (m *Manager) LoadFromRootConfig(data []byte) error
```

`RootConfig` 新增字段：

```go
type RootConfig struct {
    // ... existing fields ...
    ResourceConfig map[string]ResourceEntry `json:"resource_config,omitempty"`
}
```

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

## 通用 Server 统一配置

Server 只接受一个配置文件路径，pipeline 和资源配置统一在一个 JSON 中：

```go
type Config struct {
    ConfigPath string            // 统一 JSON 配置文件路径
    Addr       string
    Resources  *resource.Manager // 可选：预注册的 Manager
}
```

`Run()` 流程：

1. 读取统一配置文件
2. `pine.NewEngine(configData)` — 加载 pipeline
3. `resources.LoadFromRootConfig(configData)` — 从同一 JSON 提取资源配置
4. `resources.Start(ctx)`
5. `ValidateResourceDeps(configData, resources)` — 校验
6. 启动 HTTP server

## 内置资源消费算子

Pineapple 提供两个内置算子直接消费资源，覆盖最常见的两种使用模式：

### recall_resource

从资源中召回候选集。资源值应为 `[]map[string]any`（item 列表）。

```python
flow.recall_resource(resource_name="candidates")
```

适用场景：从预加载的数据库表、候选列表等资源中获取 items。

### transform_resource_lookup

从资源中查找值并写入 item 字段。资源值应为 `map[string]any`（lookup table）。

```python
flow.transform_resource_lookup(
    resource_name="features",
    lookup_key="item_id",
    output_field="item_feature",
    default_value=-1.0,
)
```

适用场景：用预加载的特征表、索引表丰富 item 数据。

### 资源类型约定

| 算子 | 资源值类型 | 说明 |
|------|-----------|------|
| `recall_resource` | `[]map[string]any` 或 `[]any` | 每个元素是一个 item map |
| `transform_resource_lookup` | `map[string]any` | 键为查找值，值为目标值 |

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

### 7. 配置热加载

当统一 JSON 配置文件变更时，`pkg/server` 的 watchConfig 同时重载 Engine 和 ResourceManager。

重载策略：原子替换整个 Manager，与 Engine 重载模式一致。

```
配置文件变更
  ↓
创建新 Manager
  ↓
LoadFromRootConfig(data)
  ↓
Start() — 同步拉取所有资源
  ↓
ValidateResourceDeps — 校验新 engine 与新资源的一致性
  ↓
atomic.Pointer.Swap — 原子替换
  ↓
oldRM.Stop() — 停止旧 Manager 的后台刷新
```

并发安全保证：

- `resources` 使用 `atomic.Pointer[resource.Manager]`，与 `enginePtr` 对称
- `handleExecute` 在请求开始时捕获 Manager 指针为局部变量，整个请求使用同一份快照
- 旧 Manager 的 `atomic.Value` 数据在 `Stop()` 后仍可读，in-flight 请求不受影响
- Go GC 保证旧对象在所有引用释放前不被回收

失败回滚：

- 若新 Manager 的 `Start()` 或 `ValidateResourceDeps` 失败，新 Manager 被 Stop，旧 Engine 和旧 Manager 保持不变
- 日志记录失败原因，服务继续使用旧配置

限制：通过 `Config.Resources` 传入的手动 `Register()` 资源在重载后不会保留。声明式资源应通过 `RegisterResource` + JSON 配置管理。
