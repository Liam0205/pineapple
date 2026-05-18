# 12. 发布与第三方扩展

## 发布形式

| 层 | 发布形式 | 说明 |
|----|---------|------|
| Go 引擎 | Go module (`go get github.com/Liam0205/pineapple/pine-go`) | 引擎、算子接口、内置算子 |
| Java 引擎 | Maven artifact `page.liam:pine-java` | 引擎、算子接口、内置算子 |
| Server 库 | `pkg/server` 包（Go） | 可复用的 HTTP 服务框架 |
| Codegen 库 | `pkg/codegen` 包（Go）/ `page.liam.pine.Codegen`（Java） | 可复用的 Python binding 生成器 |
| Python DSL | pip package `pineapple-apple`（核心包，不含 `generated/`） | 编译器、Flow 抽象、验证器 |

`apple_generated/` 是按算子集合生成的产物，每个部署不同，不打入 pip 包。第三方通过 `pip install pineapple-apple` 获得核心 DSL 能力，然后运行自己的 codegen wrapper 生成包含自定义算子的 Python 绑定。

## 第三方安装步骤

### Go 侧

```bash
go get github.com/Liam0205/pineapple/pine-go
```

### Java 侧

```xml
<!-- pom.xml -->
<dependency>
    <groupId>page.liam</groupId>
    <artifactId>pine-java</artifactId>
    <version>${pineapple.version}</version>
</dependency>
```

### Python 侧

```bash
# 安装核心 DSL 包
pip install pineapple-apple

# 构建并运行自定义 codegen（生成含内置 + 自定义算子的 Python 绑定）
# Go 后端
go build -o my-codegen ./cmd/my-codegen
./my-codegen -output apple_generated -doc-dir doc/operators -operators-dir operators

# 或 Java 后端
mvn exec:java -Dexec.mainClass="com.example.MyCodegen" -Dexec.args="--export apple_generated"
```

第三方的 `apple_generated/` 包含内置算子和自定义算子的完整 binding，放在项目本地，不依赖 pip 包分发。

## 第三方扩展模式

第三方在**不修改 pineapple 源码**的前提下添加自定义算子。

### Go 项目结构

```
my-project/
├── go.mod                    # require github.com/Liam0205/pineapple/pine-go
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

### Java 项目结构

```
my-project/
├── pom.xml                   # dependency: page.liam:pine-java
├── src/main/java/com/example/
│   ├── operators/
│   │   └── MyScorer.java     # @AutoRegister 或 static { Registry.register(...) }
│   ├── MyServer.java         # 薄 wrapper: Engine.create() + HTTP serving
│   └── MyCodegen.java        # 薄 wrapper: Codegen 产出 Python bindings
├── apple/
│   └── generated/            # codegen 产出
└── pipelines/
    └── my_pipeline.py
```

### 自定义算子

```go
// my-project/operators/my_scorer/scorer.go
package my_scorer

import (
    "context"
    pine "github.com/Liam0205/pineapple/pine-go"
)

func init() {
    pine.Register(pine.OperatorSchema{
        Name:        "transform_my_scorer",
        Type:        pine.OpTypeTransform,
        Description: "Scores items using a custom model.",
        Params: map[string]pine.ParamSpec{
            "model_name": {Type: "string", Required: true, Description: "Name of the scoring model."},
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

### 自定义算子（Java）

```java
// my-project/src/main/java/com/example/operators/MyScorer.java
package com.example.operators;

import page.liam.pine.*;

public class MyScorer implements Operator {
    static {
        Registry.register(OperatorSchema.builder()
            .name("transform_my_scorer")
            .type(OpType.TRANSFORM)
            .description("Scores items using a custom model.")
            .param("model_name", ParamSpec.required("string", "Name of the scoring model."))
            .build(),
            MyScorer::new);
    }

    private String modelName;

    @Override
    public void init(Map<String, Object> params) {
        this.modelName = (String) params.get("model_name");
    }

    @Override
    public void execute(Context ctx, OperatorInput in, OperatorOutput out) {
        // 业务逻辑
    }
}
```

### Server wrapper

```go
// my-project/cmd/my-server/main.go
package main

import (
    "flag"
    "log"

    _ "github.com/Liam0205/pineapple/pine-go/operators" // 内置算子
    _ "my-project/operators"                      // 自定义算子
    "github.com/Liam0205/pineapple/pine-go/pkg/server"
)

func main() {
    configPath := flag.String("config", "", "Pipeline JSON config")
    addr := flag.String("addr", ":8080", "Listen address")
    flag.Parse()
    if err := server.Run(server.Config{ConfigPath: *configPath, Addr: *addr}); err != nil {
        log.Fatal(err)
    }
}
```

`server.Config.Middlewares` 接受 `[]func(http.Handler) http.Handler`，按切片顺序从外到内包装。用于注入访问日志、认证等横切逻辑：

```go
server.Run(server.Config{
    ConfigPath: *configPath,
    Addr:       *addr,
    Middlewares: []func(http.Handler) http.Handler{
        accessLogMiddleware,
    },
})
```

### Codegen wrapper

```go
// my-project/cmd/my-codegen/main.go
package main

import (
    "flag"
    "fmt"
    "os"

    _ "github.com/Liam0205/pineapple/pine-go/operators"
    _ "my-project/operators"
    "github.com/Liam0205/pineapple/pine-go/pkg/codegen"
)

func main() {
    output := flag.String("output", "apple_generated", "Python output dir")
    docDir := flag.String("doc-dir", "", "Doc output dir")
    opsDir := flag.String("operators-dir", "operators", "Go operators source")
    flag.Parse()
    if err := codegen.Run(codegen.Config{OutputDir: *output, DocDir: *docDir, OpsDir: *opsDir}); err != nil {
        fmt.Fprintf(os.Stderr, "codegen: %v\n", err)
        os.Exit(1)
    }
}
```

## 原理

### Go

Go 的 `init()` + blank import 机制使得算子注册完全解耦：

1. 第三方算子包的 `init()` 调用 `pine.Register()` 写入全局 registry
2. Server / codegen wrapper 通过 blank import 触发所有 `init()`
3. `pkg/server` 和 `pkg/codegen` 从 registry 读取算子，无需知道算子的来源

### Java

Java 通过 static initializer 或 `ServiceLoader` 实现同样的解耦：

1. 第三方算子类的 `static {}` 块调用 `Registry.register()` 写入全局 registry
2. Server / codegen 入口通过 classpath 扫描或显式类加载触发注册
3. `Engine.create()` 从 registry 读取算子，无需知道算子的来源

### 共同点

两种语言的第三方算子对 pineapple 源码零污染，且与内置算子享有完全相同的能力（DAG 调度、trace、hot reload 等）。Go/Java 引擎通过 CI 交叉验证保证行为一致，第三方只需选择其中一种语言实现算子即可。
