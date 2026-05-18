# Operator Development Guide (Engineering Perspective)

## Operator Interface

Each operator implements two methods:

```go
type Operator interface {
    Init(params map[string]any) error
    Execute(ctx context.Context, input *OperatorInput, output *OperatorOutput) error
}
```

- `Init`: Receives business parameters, performs one-time initialization
- `Execute`: Called per request, reads from `input` and writes to `output`

## Registering an Operator

Call `pine.Register()` in `init()`. All metadata fields are **required**:

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

### Schema Field Reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `Name` | string | Yes | Unique operator identifier, snake_case with type prefix |
| `Type` | OperatorType | Yes | Recall / Transform / Filter / Merge / Reorder / Observe |
| `Description` | string | Yes | One-line description |
| `Params[k].Type` | string | Yes | `"string"` / `"int64"` / `"float64"` / `"bool"` / `"any"` |
| `Params[k].Required` | bool | Yes | Whether the parameter is required |
| `Params[k].Default` | any | No | Default value for optional parameters |
| `Params[k].Description` | string | Yes | Parameter description |

> Missing `Type`, `Description`, or any parameter's `Description` will cause a startup panic.

## Code Generation

```bash
scripts/codegen.sh                    # Default: Go backend
scripts/codegen.sh --backend java     # Java backend
```

Generated output:
- `apple_generated/operators.py` — Type-hinted Python operator classes
- `apple_generated/__init__.py` — Operator export list
- `apple_generated/resources.py` — Python resource classes (if resources are registered)
- `doc/operators/<name>.md` — Per-operator documentation
- `doc/operators/README.md` — Categorized index

## Dynamic Resource Registration

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

Reading resources in an operator:

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

See [Resource Manager Design Doc](../design_doc/11_resource_manager.md) for details.

## Third-Party Extensions

Third-party projects can add custom operators without modifying pineapple source. The approach: write your own operator/resource packages, register them via blank import, then use `pkg/server` and `pkg/codegen` to build your own server and Python bindings.

See [Distribution & Extensions Design Doc](../design_doc/12_distribution.md) for details.

## Testing

```bash
scripts/go-test.sh          # All Go tests
scripts/java-test.sh        # All Java tests
scripts/go-fuzz.sh          # Go fuzz testing
scripts/java-fuzz.sh        # Java fuzz testing
scripts/cross-validate.sh   # Go/Java cross-validation
```
