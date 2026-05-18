# 算子开发指南（工程视角）

## Operator 接口

每个算子实现两个方法：

```go
type Operator interface {
    Init(params map[string]any) error
    Execute(ctx context.Context, input *OperatorInput, output *OperatorOutput) error
}
```

- `Init`：接收业务参数，做一次性初始化
- `Execute`：每次请求调用，从 `input` 读数据、向 `output` 写数据

## 注册算子

在 `init()` 中调用 `pine.Register()`，所有元信息字段**必填**：

```go
package myop

import (
    "context"
    pine "github.com/Liam0205/pineapple"
)

func init() {
    pine.Register(pine.OperatorSchema{
        Name:        "transform_my_custom",
        Type:        pine.OpTypeTransform,
        Description: "Computes a custom feature for each item.",
        Params: map[string]pine.ParamSpec{
            "field":  {Type: "string", Required: true, Description: "Input field name."},
            "factor": {Type: "float64", Required: false, Default: 1.0, Description: "Scaling factor."},
        },
    }, func() pine.Operator {
        return &MyCustomOp{}
    })
}
```

### Schema 字段说明

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Name` | string | Yes | 算子唯一标识，蛇形命名，前缀体现类型 |
| `Type` | OperatorType | Yes | Recall / Transform / Filter / Merge / Reorder / Observe |
| `Description` | string | Yes | 一句话功能描述 |
| `Params[k].Type` | string | Yes | `"string"` / `"int64"` / `"float64"` / `"bool"` / `"any"` |
| `Params[k].Required` | bool | Yes | 是否必填 |
| `Params[k].Default` | any | No | 可选参数的默认值 |
| `Params[k].Description` | string | Yes | 参数描述 |

> 缺少 `Type`、`Description` 或任一参数的 `Description` 将导致启动 panic。

## 代码生成

```bash
scripts/codegen.sh                    # 默认 Go 后端
scripts/codegen.sh --backend java     # Java 后端
```

生成产出：
- `apple_generated/operators.py` — 带类型提示的 Python 算子类
- `apple_generated/__init__.py` — 算子导出列表
- `apple_generated/resources.py` — Python 资源类（若有注册资源）
- `doc/operators/<name>.md` — 每个算子的文档
- `doc/operators/README.md` — 按分类索引

## 动态资源注册

```go
func init() {
    pine.RegisterResource(pine.ResourceSchema{
        Name:            "feature_index",
        Description:     "User feature lookup table.",
        DefaultInterval: 600,
        Params: map[string]pine.ParamSpec{
            "dsn": {Type: "string", Required: true, Description: "Database DSN."},
        },
    }, func(params map[string]any) (resource.Fetcher, error) {
        return newFeatureIndexFetcher(params["dsn"].(string)), nil
    })
}
```

算子中读取资源：

```go
func (o *MyOp) Execute(ctx context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
    rp := resource.FromContext(ctx)
    if rp == nil {
        return nil
    }
    idx, _ := rp.Get("my_index")
    table := idx.(*FeatureTable)
    // ...
    return nil
}
```

详见 [动态资源管理设计文档](../design_doc/11_resource_manager.md)。

## 第三方扩展

第三方项目可以在不修改 pineapple 源码的前提下添加自定义算子和资源。核心思路：写自己的算子/资源包，通过 blank import 注册到全局 registry，然后用 `pkg/server` 和 `pkg/codegen` 构建自己的服务和 Python 绑定。

详见 [发布与第三方扩展设计文档](../design_doc/12_distribution.md)。

## 测试

```bash
scripts/go-test.sh          # Go 全量测试
scripts/java-test.sh        # Java 全量测试
scripts/go-fuzz.sh          # Go fuzz 测试
scripts/java-fuzz.sh        # Java fuzz 测试
scripts/cross-validate.sh   # Go/Java 交叉验证
```
