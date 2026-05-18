# JSON 大整数精度丢失问题

## 现状

Go `encoding/json` 将 JSON number 反序列化到 `interface{}` 时，一律使用 `float64`。
float64 只有 53 位有效尾数，超过 2^53 (9007199254740992) 的整数会**静默丢失精度**。

当前 Pine-Go 所有 JSON 解码路径均未启用 `UseNumber`：

- `internal/config/load.go` — `json.Unmarshal` 到 `map[string]any`
- `pkg/server/server.go` — `json.NewDecoder(r.Body).Decode(&req)`
- `cmd/pineapple-run/main.go` — `json.Unmarshal` 到 Request

## 影响

雪花 ID (snowflake) 通常为 19 位十进制整数，远超 2^53。如果调用方在 request JSON 中以
number 形式传入大整数 ID，Go 引擎会静默截断末位，导致：

1. 数据正确性问题：输出的 ID 与输入不一致
2. Go/Java 一致性问题：Java (Jackson) 默认用 Long 精确保存，两端结果不同

## 修复方案：UseNumber

```go
dec := json.NewDecoder(reader)
dec.UseNumber()  // JSON number → json.Number (string 底层，保留原文)
```

### 需要改动的位置

1. **config 加载** (`internal/config/load.go`)
   - `json.Unmarshal` → `json.NewDecoder` + `UseNumber()`
   - 算子参数中 number 类型的默认值会变成 `json.Number`

2. **server 请求解码** (`pkg/server/server.go:309`)
   - 已经是 `json.NewDecoder`，只需加 `.UseNumber()`

3. **CLI 工具** (`cmd/pineapple-run`, `cmd/pineapple-dag`)
   - 同样切换到 Decoder + UseNumber

4. **引擎内数值消费点** — 需要统一辅助函数：
   - `toFloat64(val any) float64` — 处理 `float64` / `json.Number` / `int` 等
   - `toInt64(val any) int64` — 同上
   - 涉及模块：
     - `operators/` — filter 比较、sort 取值、truncate count
     - Lua 传参 (`luaValue(any)`)
     - GoFormat 格式化
     - DataFrame 写入

### 与 Java 一致性的影响

UseNumber **改善**一致性：

| 场景 | 当前 Go | UseNumber Go | Java |
|------|---------|-------------|------|
| 透传字段 (ID) | float64 丢精度 | json.Number 精确 | Long 精确 |
| 计算结果 | float64 | float64 | Double |
| 序列化 `83` | `83` | `83` | `83` |
| 序列化 `83.0` | `83` | `83.0` | `83.0` |

唯一需要注意的是序列化行为：`json.Number("83")` marshal 为 `83`，
`json.Number("83.0")` marshal 为 `83.0`——这和 Java 的 Integer/Double 自然行为一致。

## 替代方案（可并行）

- **约定 ID 用 string 传递** — 零代码改动，但依赖调用方遵守
- **Apple DSL lint** — 对 `recall_static` 等有字面值的算子，检查 `*_id` 字段是否为 string
- **Server 入口校验** — 对 request 中 > 2^53 的 number 发出 warning

## 优先级

建议 UseNumber 作为引擎层的根本修复，配合文档约定 ID 用 string。
Apple DSL lint 作为锦上添花的辅助手段。
