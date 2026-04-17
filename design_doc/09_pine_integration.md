# Pine 集成模型

## 定位：纯计算库

Pine 是一个纯计算库（library），不绑定任何网络协议或服务框架。外层按部署场景套壳：

```
┌─────────────────────────┐
│  HTTP / RPC / Runner    │  ← 按需选择
├─────────────────────────┤
│     Pine (library)      │  ← 纯计算，无网络/协议依赖
└─────────────────────────┘
```

典型的壳子：

| 壳子 | 场景 |
|------|------|
| REST API HTTP 服务器 | 对外提供 HTTP 服务 |
| RPC 服务（gRPC 等） | 内部微服务调用 |
| Offline Runner | 批量/离线计算 |

Pine 不关心自己被谁调用、以什么协议调用。

## 核心 API

```go
// 加载 JSON 配置，构建 DAG，初始化所有算子实例。
engine, err := pine.NewEngine(jsonConfig []byte)

// 执行一次请求。
result, err := engine.Execute(ctx context.Context, req *pine.Request)
```

### Request

```go
type Request struct {
    // Common 特征，如 user_id、user_age 等。必填。
    Common map[string]any

    // Item 列表。可选——有召回算子的 flow 不需要外部传入 item。
    Items []map[string]any
}
```

### Result

```go
type Result struct {
    // 处理后的 common 输出字段。
    Common map[string]any

    // 处理后的 item 列表（已排序、过滤）。
    Items []map[string]any
}
```

### 设计原则

- **无状态**：`Engine` 在 `NewEngine` 后不可变。不提供 `Reload` 方法。
- **配置重载由外层负责**：壳子创建新 `Engine`，通过原子替换（`atomic.Pointer` 或类似机制）切换，旧 `Engine` 在无引用后由 GC 回收。
- **并发安全**：同一个 `Engine` 可被多个 goroutine 并发调用 `Execute`。
- **生命周期简单**：`NewEngine` → 反复 `Execute` → 不再引用即回收。无需显式 `Close`。

## 请求数据流

```
壳子接收外部请求
    │
    ▼
构造 pine.Request（填充 common 特征 + 可选 item 列表）
    │
    ▼
engine.Execute(ctx, req)
    │
    ├── 引擎将 Request 填充到 DataFrame
    ├── 按 DAG 拓扑并行执行算子
    ├── 召回算子从外部服务获取 item（如有）
    └── 返回 pine.Result
    │
    ▼
壳子将 Result 序列化为响应返回
```

## 配置重载流程

```
壳子监听配置变更（文件 watch / 配置中心推送）
    │
    ▼
newEngine, err := pine.NewEngine(newJsonConfig)
    │  ← 加载失败则保留旧 Engine，记录错误
    ▼
atomic.StorePointer(&currentEngine, newEngine)
    │  ← 后续请求使用新 Engine
    ▼
旧 Engine 无引用后由 GC 回收
    （旧 Engine 中的算子实例、Lua state pool 等随之释放）
```

Pine 不参与配置变更的监听和切换——这是壳子的责任。Pine 只保证：给我合法的 JSON，我返回可用的 Engine。
