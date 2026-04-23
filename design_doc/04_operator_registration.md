# 算子注册机制

## 注册方式

采用 Go `init()` 自注册。算子包被 import 时自动注册到全局 registry，无需手动修改注册代码。

```go
// operators/filter/condition.go
package filter

import pine "github.com/Liam0205/pineapple"

func init() {
    pine.Register(pine.OperatorSchema{
        Name:        "filter_condition",
        Type:        "Filter",
        Description: "Remove items matching a condition.",
        Params: map[string]pine.ParamSpec{
            "field": {Type: "string", Required: true, Description: "Field to check."},
            "value": {Type: "any", Required: true, Description: "Value to match for removal."},
        },
    }, func() pine.Operator {
        return &ConditionOp{}
    })
}

// FilterOperator 实现 pine.Operator 接口
type FilterOperator struct {
    condition string   // Init 后只读
    threshold float64  // Init 后只读
}

func (f *FilterOperator) Init(params map[string]any) error {
    f.condition = params["condition"].(string)
    if v, ok := params["threshold"]; ok {
        f.threshold = v.(float64)
    }
    return nil
}

func (f *FilterOperator) Execute(ctx context.Context, input *pine.OperatorInput, output *pine.OperatorOutput) error {
    // 业务逻辑...
    return nil
}
```

### 设计原则

- JSON 配置生成后，Pine 无需修改任何 Go 代码即可运行。
- 新增算子只需编写 Go 实现 + import 该包，自动注册生效。

## 注册信息

### OperatorSchema

每个算子注册时提供：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| Name | string | Yes | 算子唯一标识，蛇形命名 |
| Type | string | Yes | 算子类型，必须为合法枚举值（见下） |
| Description | string | Yes | 一句话功能描述 |
| Params | map[string]ParamSpec | - | 算子接受的配置参数 schema |

#### Type 枚举值

| 值 | 含义 | 允许的 Output 方法 | DSL 前缀 |
|----|------|-------------------|----------|
| `"Recall"` | 召回 | `AddItem` | `recall_` |
| `"Transform"` | 特征变换 | `SetCommon`, `SetItem` | `transform_` |
| `"Filter"` | 过滤 | `RemoveItem` | `filter_` |
| `"Merge"` | 合并 | `RemoveItem`, `SetItem` | `merge_` |
| `"Reorder"` | 排序 | `SetItemOrder` | `reorder_` |
| `"Observe"` | 观察 | 无 | `observe_` |

`Type` 缺失或不在枚举范围内 → 注册时 panic。

算子名称 (Name) **必须**以其类型对应的 DSL 前缀开头。此约束在注册时校验，不符合则 panic。

详见 [05 算子类型体系](05_operator_types.md)。

### ParamSpec

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| Type | string | Yes | 参数类型 (`"string"`, `"int64"`, `"float64"`, `"bool"`, `"any"`) |
| Required | bool | Yes | 是否必填 |
| Default | any | No | 非必填时的默认值 |
| Description | string | Yes | 参数描述 |

## 算子工厂与生命周期

注册时提供的是**工厂函数** `func() Operator`，用于创建算子实例。

### 生命周期

1. **配置加载时**：引擎通过工厂函数为 JSON 中的每个算子定义创建一个实例，调用 `Init(params)` 注入业务参数。
2. **运行时**：同一个实例被所有并发请求共享，`Execute` 可被多个 goroutine 并发调用。
3. **配置重新加载时**：引擎创建新实例并切换，旧实例在无引用后由 GC 回收。

### 无状态可重入约定

算子在 `Init` 后必须是无状态可重入的：

- `Init` 后只持有只读配置和线程安全资源（如连接池）。
- `Execute` 不可持有或修改请求级状态。
- 确定了参数后，算子的行为完全由输入数据决定。

此约定使得同一个算子实例可安全地被并发复用，无需每次 DAG 执行都创建新实例。

## Pine ↔ Apple 联动：构建时代码生成

Go 侧的算子 schema 是**唯一的事实来源**。Apple 侧的 Python 代码通过构建流程自动生成，确保两侧始终一致。

### 流程

```
                        构建流程 (go generate)
                               │
        ┌──────────────────────┼──────────────────────┐
        ▼                      ▼                      ▼
  扫描所有已注册的       读取 Apple 模板文件       输出生成的
  OperatorSchema          (Python 模板)          Apple Python 包
        │                      │                      │
        └──────────┬───────────┘                      │
                   ▼                                  │
            用 schema 数据                             │
            填充模板内容  ────────────────────────────▶│
```

### 输入

1. **Go 算子 schema**: 构建工具扫描所有通过 `pine.Register()` 注册的 OperatorSchema。
2. **Apple 模板**: Python 侧提供模板文件，定义 DSL 的基础结构（BaseOp、Flow 等）。

### 输出

完整可用的 Apple Python 包，包含：

- 每个算子对应的 Python class（带类型提示、参数校验）
- DSL 基础设施（Flow、装饰器等来自模板）

### 生成的 Python 代码示例

```python
# auto-generated from pine operator schema — DO NOT EDIT
from apple.base import BaseOp

class FilterConditionOp(BaseOp):
    """Operator: filter_condition"""
    _name = "filter_condition"
    _params_schema = {
        "value": {"type": "any", "required": True},
    }

    def __call__(
        self,
        *,
        value: Any = ...,
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "FilterConditionOp":
        ...
```

### 好处

- **单一事实来源**: 算子定义只在 Go 侧维护，无手工同步负担。
- **自动一致**: 构建流程保证 Pine 和 Apple 对算子的认知始终同步。
- **IDE 友好**: 算法同学拿到的 Apple 包有完整的类型提示和补全。
- **双向校验**: Apple 侧在 DSL 解析时校验参数，Pine 侧在加载 JSON 时校验参数，两侧使用相同的 schema 定义。

## 启动校验

Pine 加载 JSON 配置时，基于注册的 schema 进行校验：

1. **算子名是否存在**: JSON 中引用了未注册的算子 → 报错。
2. **必填参数是否齐全**: schema 中 Required=true 的参数在 JSON 中缺失 → 报错。
3. **参数类型是否匹配**: JSON 中参数值的类型与 schema 不符 → 报错。
4. **DAG 合法性**: 输入输出依赖关系校验（见 02_flow_abstraction.md）。
