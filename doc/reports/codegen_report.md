# Code Generation Report

## Overview

The `pineapple-codegen` tool bridges the Go operator registry and the Apple Python DSL. It scans all registered `OperatorSchema` instances and generates typed Python classes, ensuring Pine and Apple share a single source of truth for operator definitions.

## Architecture

```
go run ./cmd/pineapple-codegen -output apple_generated
         │
         ├── imports _ "github.com/Liam0205/pineapple/operators" (triggers init())
         ├── calls registry.All() to get sorted schemas
         ├── renders operators.py (one class per operator)
         └── renders __init__.py (re-exports all classes)
```

### Files

| File | Purpose |
|------|---------|
| `cmd/pineapple-codegen/main.go` | CLI entry point, thin wrapper |
| `pkg/codegen/codegen.go` | `Run()` orchestration, doc generation |
| `pkg/codegen/template.go` | Go templates, type mapping, CamelCase conversion |
| `pkg/codegen/docparse.go` | Parse operator doc comments from Go source |
| `pkg/codegen/codegen_test.go` | Unit + integration tests |
| `internal/registry/export.go` | `registry.All()` — exports all registered schemas sorted by name |

### Generated Output

| File | Content |
|------|---------|
| `apple_generated/operators.py` | Python classes for all 8 registered operators |
| `apple_generated/__init__.py` | Re-exports all operator classes |

## Type Mapping

| Go ParamSpec.Type | Python type hint | Python default |
|-------------------|-----------------|----------------|
| `string` | `str` | `""` |
| `int64` | `int` | `0` |
| `float64` | `float` | `0.0` |
| `bool` | `bool` | `False` |
| `any` / other | `Any` | `None` |

Default values from ParamSpec are rendered as proper Python literals (strings quoted, bools capitalized).

## Generated Class Structure

Each operator class:
- Inherits from `BaseOp` (to be implemented in `apple/base.py`)
- Has `_name` (operator type name) and `_params_schema` class attributes
- Provides a typed `__call__` method with:
  - Business parameters as keyword args (required params use `...` as sentinel)
  - Standard metadata kwargs: `common_input`, `common_output`, `item_input`, `item_output`, `item_defaults`
- Delegates to `self._apply(...)` for flow composition

## Generated Operators

| Class | Operator | Params |
|-------|----------|--------|
| `TransformDispatchOp` | transform_dispatch | common_field, item_field |
| `TransformNormalizeOp` | transform_normalize | field, method, output_field |
| `FilterConditionOp` | filter_condition | field, value |
| `FilterTruncateOp` | filter_truncate | top_n |
| `TransformByLuaOp` | transform_by_lua | lua_script, function_for_item, function_for_common |
| `MergeDedupOp` | merge_dedup | dedup_by, strategy |
| `RecallStaticOp` | recall_static | items |
| `ReorderSortOp` | reorder_sort | field, order |

## Testing

| Test | Scope |
|------|-------|
| `TestToCamelCase` | snake_case → CamelCase conversion |
| `TestPythonType` | Go type → Python type hint mapping |
| `TestPythonDefault` | Go type → Python default value |
| `TestPythonLiteral` | Go value → Python literal (strings, bools, numbers, nil) |
| `TestSortedParams` | Deterministic param ordering |
| `TestTemplateRendering` | Full template rendering with assertions on key elements |
| `TestRegistryAllIntegration` | `registry.All()` returns sorted, non-empty schemas |
| `TestRunIntegration` | End-to-end: generates files in temp dir, validates content |

**Validation:**
- `go test ./... -count=1`: all pass
- `go vet ./...`: clean
- `python3 -c "import ast; ast.parse(open('operators.py').read())"`: syntax valid
