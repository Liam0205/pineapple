# 12. Distribution & Third-Party Extensions

## Distribution

| Layer | Form | Description |
|-------|------|-------------|
| Go engine | Go module (`go get github.com/Liam0205/pineapple`) | Engine, operator interfaces, built-in operators |
| Java engine | Maven artifact `page.liam:pine-java` | Engine, operator interfaces, built-in operators |
| Server library | `pkg/server` package (Go) | Reusable HTTP service framework |
| Codegen library | `pkg/codegen` package (Go) / `page.liam.pine.Codegen` (Java) | Reusable Python binding generator |
| Python DSL | pip package `pineapple-apple` (core, excludes `generated/`) | Compiler, Flow abstraction, validator |

`apple_generated/` is generated per operator set and differs per deployment — it is not included in the pip package. Third parties install `pineapple-apple` via pip for core DSL capabilities, then run their own codegen wrapper to generate Python bindings that include custom operators.

## Installation Steps for Third Parties

### Go

```bash
go get github.com/Liam0205/pineapple
```

### Java

```xml
<!-- pom.xml -->
<dependency>
    <groupId>page.liam</groupId>
    <artifactId>pine-java</artifactId>
    <version>${pineapple.version}</version>
</dependency>
```

### Python

```bash
# Install core DSL package
pip install pineapple-apple

# Build and run custom codegen (generates bindings for built-in + custom operators)
# Go backend
go build -o my-codegen ./cmd/my-codegen
./my-codegen -output apple_generated -doc-dir doc/operators -operators-dir operators

# Or Java backend
mvn exec:java -Dexec.mainClass="com.example.MyCodegen" -Dexec.args="--export apple_generated"
```

The third-party `apple_generated/` contains complete bindings for both built-in and custom operators, kept locally in the project without relying on pip distribution.

## Third-Party Extension Pattern

Third parties add custom operators **without modifying pineapple source**.

### Go Project Structure

```
my-project/
├── go.mod                    # require github.com/Liam0205/pineapple
├── operators/
│   ├── my_scorer/
│   │   └── scorer.go         # init() { pine.Register(schema, factory) }
│   └── all.go                # import _ "my-project/operators/my_scorer"
├── cmd/
│   ├── my-server/
│   │   └── main.go           # Thin wrapper: blank import operators + server.Run()
│   └── my-codegen/
│       └── main.go           # Thin wrapper: blank import operators + codegen.Run()
├── apple/
│   └── generated/            # Codegen output (built-in + custom operator bindings)
└── pipelines/
    └── my_pipeline.py
```

### Java Project Structure

```
my-project/
├── pom.xml                   # dependency: page.liam:pine-java
├── src/main/java/com/example/
│   ├── operators/
│   │   └── MyScorer.java     # @AutoRegister or static { Registry.register(...) }
│   ├── MyServer.java         # Thin wrapper: Engine.create() + HTTP serving
│   └── MyCodegen.java        # Thin wrapper: Codegen produces Python bindings
├── apple/
│   └── generated/            # Codegen output
└── pipelines/
    └── my_pipeline.py
```

### Custom Operator (Go)

```go
// my-project/operators/my_scorer/scorer.go
package my_scorer

import (
    "context"
    pine "github.com/Liam0205/pineapple"
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
    // Business logic
    return nil
}
```

### Custom Operator (Java)

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
        // Business logic
    }
}
```

### Server Wrapper

```go
// my-project/cmd/my-server/main.go
package main

import (
    "flag"
    "log"

    _ "github.com/Liam0205/pineapple/operators" // Built-in operators
    _ "my-project/operators"                      // Custom operators
    "github.com/Liam0205/pineapple/pkg/server"
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

`server.Config.Middlewares` accepts `[]func(http.Handler) http.Handler`, wrapping from outer to inner in slice order. Use it for cross-cutting concerns like access logging or authentication:

```go
server.Run(server.Config{
    ConfigPath: *configPath,
    Addr:       *addr,
    Middlewares: []func(http.Handler) http.Handler{
        accessLogMiddleware,
    },
})
```

### Codegen Wrapper

```go
// my-project/cmd/my-codegen/main.go
package main

import (
    "flag"
    "fmt"
    "os"

    _ "github.com/Liam0205/pineapple/operators"
    _ "my-project/operators"
    "github.com/Liam0205/pineapple/pkg/codegen"
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

## How It Works

### Go

Go's `init()` + blank import mechanism enables fully decoupled operator registration:

1. Third-party operator packages call `pine.Register()` in their `init()`, writing to the global registry
2. Server / codegen wrappers trigger all `init()` functions via blank imports
3. `pkg/server` and `pkg/codegen` read operators from the registry without knowing their origin

### Java

Java achieves the same decoupling via static initializers or `ServiceLoader`:

1. Third-party operator classes call `Registry.register()` in their `static {}` blocks, writing to the global registry
2. Server / codegen entry points trigger registration via classpath scanning or explicit class loading
3. `Engine.create()` reads operators from the registry without knowing their origin

### Common Principle

Third-party operators in both languages have zero pollution on pineapple source and enjoy exactly the same capabilities as built-in operators (DAG scheduling, tracing, hot reload, etc.). Go/Java engines are verified for behavioral consistency via CI cross-validation — third parties only need to implement operators in one language of their choice.
