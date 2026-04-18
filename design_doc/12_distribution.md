# 12. 发布与第三方扩展

## 发布形式

| 层 | 发布形式 | 说明 |
|----|---------|------|
| Go 引擎 | Go module (`go get github.com/Liam0205/pineapple`) | 引擎、算子接口、内置算子 |
| Server 库 | `pkg/server` 包 | 可复用的 HTTP 服务框架 |
| Codegen 库 | `pkg/codegen` 包 | 可复用的 Python binding 生成器 |
| Python DSL | pip package（`apple` 核心包，不含 `generated/`） | 编译器、Flow 抽象、验证器 |

`apple/generated/` 是按算子集合生成的产物，每个部署不同，不打入 pip 包。

## 第三方扩展模式

第三方在**不修改 pineapple 源码**的前提下添加自定义算子，架构如下：

```
my-project/
├── go.mod                    # require github.com/Liam0205/pineapple
├── operators/
│   ├── my_scorer/
│   │   └── scorer.go         # init() { pine.Register(schema, factory) }
│   └── all.go                # import _ "my-project/operators/my_scorer"
├── cmd/
│   ├── my-server/
│   │   └── main.go           # 薄 wrapper: blank import 算子 + server.Run()
│   └── my-codegen/
│       └── main.go           # 薄 wrapper: blank import 算子 + codegen.Run()
├── apple/
│   └── generated/            # codegen 产出（含内置 + 自定义算子的 binding）
└── pipelines/
    └── my_pipeline.py
```

### 自定义算子

```go
// my-project/operators/my_scorer/scorer.go
package my_scorer

import pine "github.com/Liam0205/pineapple"

func init() {
    pine.Register(pine.OperatorSchema{
        Name:     "my_scorer",
        Category: "Scoring",
        Params: map[string]pine.ParamSpec{
            "model_name": {Type: "string", Required: true},
        },
    }, func() pine.Operator { return &MyScorer{} })
}

type MyScorer struct{ modelName string }

func (s *MyScorer) Init(params map[string]any) error {
    s.modelName = params["model_name"].(string)
    return nil
}

func (s *MyScorer) Execute(ctx context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
    // 业务逻辑
    return nil
}
```

### Server wrapper

```go
// my-project/cmd/my-server/main.go
package main

import (
    "flag"
    "log"

    _ "github.com/Liam0205/pineapple/operators" // 内置算子
    _ "my-project/operators"                      // 自定义算子
    "github.com/Liam0205/pineapple/pkg/server"
)

func main() {
    cfg := server.Config{
        ConfigPath: flag.String("config", "", "Pipeline JSON config"),
        Addr:       flag.String("addr", ":8080", "Listen address"),
    }
    flag.Parse()
    if err := server.Run(cfg); err != nil {
        log.Fatal(err)
    }
}
```

### Codegen wrapper

```go
// my-project/cmd/my-codegen/main.go
package main

import (
    "flag"
    "log"

    _ "github.com/Liam0205/pineapple/operators"
    _ "my-project/operators"
    "github.com/Liam0205/pineapple/pkg/codegen"
)

func main() {
    cfg := codegen.Config{
        OutputDir: flag.String("output", "apple/generated", "Python output dir"),
        DocDir:    flag.String("doc-dir", "", "Doc output dir"),
        OpsDir:    flag.String("operators-dir", "operators", "Go operators source"),
    }
    flag.Parse()
    if err := codegen.Run(cfg); err != nil {
        log.Fatal(err)
    }
}
```

## 原理

Go 的 `init()` + blank import 机制使得算子注册完全解耦：

1. 第三方算子包的 `init()` 调用 `pine.Register()` 写入全局 registry
2. Server / codegen wrapper 通过 blank import 触发所有 `init()`
3. `pkg/server` 和 `pkg/codegen` 从 registry 读取算子，无需知道算子的来源

第三方算子对 pineapple 源码零污染，且与内置算子享有完全相同的能力（DAG 调度、trace、hot reload 等）。
