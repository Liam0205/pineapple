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
    Get(name string) (ResourceHandle, bool)
}

// ResourceHandle 是对资源值的引用计数借用。持有者必须且只能调用一次 Release，
// 惯用法是 Get 成功后立即 defer。Value() 返回的值不得在配对的 Release 之后继续
// 使用、保存或传出作用域——最后一个借用释放后，实现了 io.Closer 的值会被关闭，
// 之后再用即 use-after-close。
type ResourceHandle interface {
    Value() any
    Release()
}
```

算子只依赖此接口，不感知刷新逻辑。`Get` 返回的是一个**引用计数借用**而非裸值，
这是 GC 语言对 C++ `shared_ptr` 的等价模拟：持有 handle 可让底层值在并发的
刷新 / 退休中保持存活，值只有在最后一个引用释放后才被销毁。算子内惯用法：

```go
h, ok := rp.Get("user_feature_index")
if !ok {
    // 资源未就绪，降级
}
defer h.Release()
table := h.Value().(*FeatureTable)
// 使用 table ...（不得把 table 保存到算子字段或传出 Execute 作用域）
```

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
h, ok := rp.Get("user_feature_index")
if ok {
    defer h.Release()
    val := h.Value()
}
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

    h, ok := rp.Get("user_feature_index")
    if !ok {
        // 资源尚未就绪，降级
        out.SetCommon("score", 0.0)
        return nil
    }
    defer h.Release() // 借用立即 defer 释放，禁止保存或传出本作用域

    table := h.Value().(*FeatureTable)
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

### redis_connection（句柄型资源）

上面两个算子消费的是**数据型资源**：资源值是可序列化的纯数据（item 列表 /
lookup 表），随刷新整体替换。Redis 连接池则是另一类资源——**句柄型资源**：
资源值是一个携带活动连接的不可序列化对象，由 ResourceManager 持有其生命周期，
算子按名借用、用完归还，绝不拥有。

`redis_connection` 是内置的句柄型资源类型，在统一 JSON 的 `resource_config`
中声明：

```json
{
  "resource_config": {
    "resources": {
      "redis_conn": {
        "type_name": "redis_connection",
        "addr": "127.0.0.1:6379",
        "password": "",
        "db": 0,
        "interval": -1
      }
    }
  }
}
```

`interval: -1` 表示永不刷新——连接池一旦建立即长期复用，由 ResourceManager 在
退休 / 关闭时统一拆除。`transform_redis_get` / `transform_redis_set` 不再内联
`redis_addr`，改为按 `resource_name` 借用共享连接池：

```python
flow.transform_redis_get(
    resource_name="redis_conn",   # 借用 redis_connection 资源
    key_prefix="k:",
    data_type="string",
)
```

借用失败语义与数据型资源一致，由算子降级决策：

- 借用失败（无 provider / 资源未就绪 / 类型不匹配）→ 静默降级：
  `redis_get` 报 `cache_hit=false` 且不写 value，`redis_set` 直接 no-op，
  均不尝试连接。
- 借用成功但命令 / 连接出错 → 记 warning 日志；若算子配置了
  `fail_on_error` 则抛错，否则吞掉继续。

这样连接池得以在多个算子、多个 pipeline 之间共享，并在热重载时随 Manager 原子
替换，而非每个算子各自持有一份连接。

## 设计要点

### 1. 首次加载同步，后续异步

`Start()` 对每个已注册资源执行一次同步拉取。若首次拉取失败，`Start()` 返回错误，壳子可决定是否继续启动。后续刷新异步进行。

### 2. 刷新失败保留旧版本

后台刷新拉取新数据失败时，不清空旧数据，继续使用上一次成功的版本。记录日志和错误。

### 3. 资源缺失由算子决策

`ResourceProvider.Get` 返回 `nil, false` 时，算子自行决定是报错还是降级。框架不强制统一行为。

### 4. 无锁读 + 引用计数借用

每个资源的当前值用 `atomic.Pointer[refValue]` 持有，读路径零锁竞争。`refValue`
是对底层值的引用计数包装（GC 语言对 C++ `shared_ptr` 的等价模拟）：

- 创建时持有一个基线引用（refs=1），代表「Manager 当前指向它」。
- `Get` 通过 acquire-if-positive 的 CAS 借用一份引用；只要计数 > 0 就 +1 并返回
  handle，计数已归零（值正在退休）则重试读取最新指针。这个偏置计数消除了
  「先 Load 再 Add」之间的竞争窗口。
- handle 的 `Release` 将计数 -1；最后一个引用释放（计数归零）时，若值实现了
  `io.Closer` 则 `Close` 一次（`sync.Once` 保护，错误记日志）。

刷新 goroutine 构建完整新版本后 `Swap` 指针，并对旧 `refValue` 调用一次
`release()` 丢弃基线引用——若此刻仍有 in-flight 借用，Close 被推迟到最后一个
借用释放，杜绝 use-after-close。

### 5. 可测试性

算子依赖 `ResourceProvider` 接口而非具体 ResourceManager。测试时注入 mock：

```go
mock := resource.NewStatic(map[string]any{
    "user_feature_index": myTestTable,
})
ctx := resource.WithResources(ctx, mock)
```

### 6. 优雅关闭

`Stop()` 取消所有刷新 goroutine 的 context 并等待退出，确保壳子关闭时不泄漏
goroutine。同时对每个资源的当前 `refValue` 丢弃基线引用（`release()`）：

- 若无 in-flight 借用，基线引用即最后一个引用，立即触发实现了 `io.Closer` 的
  值的 `Close`（连接池、文件句柄等被释放）。
- 若仍有 in-flight 借用，Close 被推迟到最后一个借用 `Release` 之后，保证
  in-flight 业务代码读到的值始终存活。

注意：因 Close 可能被推迟到借用释放线程上执行，`Stop()` 无法同步上报 Close
错误，错误一律记日志。`Stop()` 幂等。

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

- `resources` 使用 `atomic.Pointer[resource.Manager]`，与 `enginePtr` 对称；
  `pkg/server` 进一步将 Engine 与 Manager 打包为 `serverSnapshot`，用一个
  引用计数统一管理整个快照的退休（见下）。
- `handleExecute` 在请求开始时 `acquire` 当前快照（计数 +1），整个请求使用
  同一份快照，请求结束 `defer release`。
- 旧快照被换下时调用 `release()` 丢弃基线引用；若仍有 in-flight 请求持有它，
  Engine.Close 与 Manager.Stop 被推迟到最后一个请求释放后才执行——in-flight
  请求既不会读到半关闭的 Engine，也不会读到已 Close 的资源值。
- 资源值层面再有一层 `refValue` 引用计数：即便 Manager 已 `Stop`，被算子借用
  的资源值在 handle `Release` 前不会被 Close。两层引用计数共同保证零
  use-after-close。

> Parity 说明：C++ 用原生 `shared_ptr` + RAII 自然获得同等语义（拷贝即 +1，
> 析构即 -1，零 in-flight 风险）；Go/Java/Python 缺少随值拷贝流动的引用计数，
> 改用手写引用计数 + 作用域退出钩子（Go `defer`、Java try-with-resources、
> Python `with`）。对外契约一致：「退休 = 释放引用，最后一个引用关闭，绝不泄漏」。
> 代价是借用必须在取得后立即 `defer Release`，且不得保存到字段或传出作用域。
>
> 数据型 vs 句柄型：C++ 将资源值显式建模为 `ResourceValue`，内部是
> 「数据 `Variant`」XOR「句柄 `shared_ptr<void>`」二选一——句柄刻意排除在
> `Variant` 之外，保证 `Variant` 始终是可 `dump_json` / `parse_json` 的纯 JSON。
> 数据型资源走 `snapshot()` 整体导出（与 Go/Java 的快照导出对齐），句柄型资源
> 走 `ResourceProvider::borrow(name)` 返回 `shared_ptr<void>`（数据型 / 缺失 /
> 未加载一律返回 `nullptr`，调用方降级）。算子拿到后 `static_pointer_cast` 到
> 具体类型（如 `RedisConnResource`）。退休时 `Manager::stop()` 把句柄型
> `ResourceValue` 重置，最后一个借用者析构其 `shared_ptr` 时连接池才真正拆除，
> 配合 `engine_mu_` 锁序保证无 in-flight 借用——这正是 Go 双层引用计数、Java
> 快照引用计数在 C++ 里的等价落地。

失败回滚：

- 若新 Manager 的 `Start()` 或 `ValidateResourceDeps` 失败，新 Manager 被 Stop，旧 Engine 和旧 Manager 保持不变
- 日志记录失败原因，服务继续使用旧配置

限制：通过 `Config.Resources` 传入的手动 `Register()` 资源在重载后不会保留。声明式资源应通过 `RegisterResource` + JSON 配置管理。
