English | [简体中文](README.md)

# Pineapple

High-performance DAG pipeline engine. **Declare in Python, execute in Go/Java, decouple via JSON.**

Operators declare their input/output fields; the engine automatically infers dependencies, builds the DAG, and schedules parallel execution — you focus on business logic, Pineapple makes it fast.

Suitable for any scenario requiring **multi-step data processing pipelines**: search/recommendation/ad ranking, feature engineering, real-time data processing, rule engines, ML pre/post-processing, etc.

> **⚠️ Pre-1.0**: APIs and behavioral semantics may change incompatibly between versions. Pin to a specific version for production use.

## Architecture

```
Python DSL (Apple)  ──compile──>  JSON Config
                                      │
                          ┌───────────┴───────────┐
                          v                       v
                   Pine-Go (Go)            Pine-Java (Java)
                   Build DAG, execute      Build DAG, execute
```

| Component | Language | Role |
|-----------|----------|------|
| **Apple** | Python | Declarative DSL, compiles to JSON config |
| **Pine-Go** | Go | Primary execution engine: parse config, build DAG, parallel scheduling |
| **Pine-Java** | Java | Second execution engine, behavior-consistent with Pine-Go |

**Engineering teams** develop high-performance operators in Go/Java; **product teams** compose logic with the Python DSL. The two sides are fully decoupled via JSON config.

## Key Features

- **Implicit graph construction** — Operators declare input/output fields; engine infers DAG dependencies with transitive reduction
- **Lock-free parallelism** — Independent operators in the DAG execute in parallel automatically
- **Compile-time validation** — Dead code, missing fields, write-after-write detected before deployment
- **Embedded Lua** — Built-in Lua operators for lightweight custom computation, only ~1.3x slower than native Go
- **Hot config reload** — Service automatically reloads engine config without downtime
- **Dynamic resources** — Background-refreshed in-memory resource manager with lock-free reads
- **White-box observability** — Operator-level traces, `/stats` endpoint, pluggable Prometheus interface
- **Row/Column storage** — DataFrame supports both storage modes
- **Dual-engine consistency** — Go/Java engines verified via CI cross-validation for schema, DAG, and execution parity

## Migrating from Older Versions (Breaking Change)

> Starting from v0.7, the Go engine has moved from the repository root into the `pine-go/` subdirectory. The Go module path has changed accordingly.

### What Changed

| Item | Before | After |
|------|--------|-------|
| Module path | `github.com/Liam0205/pineapple` | `github.com/Liam0205/pineapple/pine-go` |
| Import | `github.com/Liam0205/pineapple/internal/...` | `github.com/Liam0205/pineapple/pine-go/internal/...` |
| Import | `github.com/Liam0205/pineapple/pkg/...` | `github.com/Liam0205/pineapple/pine-go/pkg/...` |
| Import | `github.com/Liam0205/pineapple/operators` | `github.com/Liam0205/pineapple/pine-go/operators` |
| Binary | `go build ./cmd/pineapple-server` | `go build ./pine-go/cmd/pineapple-server` |

### Migration Steps

```bash
# 1. Bulk-replace import paths
find . -name '*.go' -exec sed -i \
  's|github.com/Liam0205/pineapple/|github.com/Liam0205/pineapple/pine-go/|g' {} +

# 2. Fix double-nesting if you referenced the module itself
find . -name '*.go' -exec sed -i \
  's|github.com/Liam0205/pineapple/pine-go/pine-go/|github.com/Liam0205/pineapple/pine-go/|g' {} +

# 3. Update go.mod
go get github.com/Liam0205/pineapple/pine-go@latest
go mod tidy
```

If your project uses Pineapple through public APIs (`pine.NewEngine`, `pine.BuildOperator`, etc.), the above steps complete the migration. No semantic changes to internal APIs.

## Quick Start

### Prerequisites

- Go 1.22+ (Pine-Go)
- Java 11+ (Pine-Java)
- Python 3.10+ (Apple DSL)

### 1. Write a Pipeline

```python
from apple.flow import Flow

flow = Flow(
    name="demo",
    common_input=["user_age"],
    item_output=["item_id", "item_final_price"],
)

flow.recall_static(
    item_output=["item_id", "item_price"],
    items=[
        {"item_id": "a", "item_price": 100.0},
        {"item_id": "b", "item_price": 200.0},
    ],
)

flow.transform_by_lua(
    common_input=["user_age"],
    item_input=["item_price"],
    item_output=["item_final_price"],
    lua_script="""
function discount()
  if user_age < 18 then return item_price * 0.8
  else return item_price end
end
""",
    function_for_item="discount",
)

flow.reorder_sort(
    item_input=["item_final_price"],
    field="item_final_price",
    order="desc",
)

with open("pipeline.json", "w") as f:
    f.write(flow.compile())
```

### 2. Start the Server

```bash
go run ./pine-go/cmd/pineapple-server -config pipeline.json -addr :8080
```

### 3. Send a Request

```bash
curl -s -X POST http://localhost:8080/execute \
  -H "Content-Type: application/json" \
  -d '{"common": {"user_age": 16}, "items": []}' | python3 -m json.tool
```

After modifying the Python script, recompile and the service hot-reloads automatically — no restart needed.

## Project Structure

```
pineapple/
├── apple/                  # Python DSL (Apple)
│   ├── flow.py             #   Flow/SubFlow declarations
│   ├── compiler.py         #   Compiler: DSL → JSON
│   ├── validator.py        #   Static validator
│   └── tests/              #   Python tests
├── apple_generated/        # Auto-generated Python bindings (via codegen)
├── pine-go/                # Go execution engine (Pine-Go)
│   ├── cmd/                #   CLI tools
│   │   ├── pineapple-server/   # HTTP server
│   │   ├── pineapple-codegen/  # Code & doc generation
│   │   ├── pineapple-dag/      # DAG rendering
│   │   └── pineapple-run/      # One-shot execution
│   ├── internal/           #   Internal packages (config/dag/dataframe/runtime)
│   ├── operators/          #   Built-in operators
│   ├── pkg/                #   Reusable libraries (server/codegen/metrics/resource)
│   ├── integration/        #   Integration tests
│   └── benchmarks/         #   Performance benchmarks
├── pine-java/              # Java execution engine (Pine-Java)
│   ├── src/main/java/      #   Engine implementation + CLI tools
│   └── src/test/java/      #   Tests + benchmarks + fuzz
├── fixtures/               # Shared test fixtures (used by both Go and Java)
│   ├── operators/          #   Operator-level unit fixtures
│   ├── pipelines/          #   Pipeline-level end-to-end fixtures
│   └── errors/             #   Error path fixtures
├── scripts/                # Developer scripts
├── design_doc/             # Design documents
└── doc/                    # Generated operator docs & reports
```

## Development

### Scripts

| Script | Purpose |
|--------|---------|
| `scripts/go-test.sh` | Run all Go tests |
| `scripts/java-test.sh` | Run all Java tests |
| `scripts/test-all.sh` | Run Go + Java + Python tests |
| `scripts/lint.sh` | Lint Go + Java + Python |
| `scripts/go-bench.sh` | Go benchmarks |
| `scripts/java-bench.sh` | Java benchmarks |
| `scripts/go-fuzz.sh` | Go fuzz testing |
| `scripts/java-fuzz.sh` | Java fuzz testing |
| `scripts/cross-validate.sh` | Go/Java cross-validation (schema + DAG + execution) |
| `scripts/codegen.sh` | Code generation (supports `--backend go\|java`) |
| `scripts/render-dag.sh` | DAG visualization (supports `--backend go\|java`) |
| `scripts/apple-compile.sh` | Compile Apple DSL to JSON |
| `scripts/run-pipeline.sh` | One-shot pipeline execution |
| `scripts/bump-version.sh` | Synchronize version across all components |

### CI Pipeline

CI runs automatically on every push/PR:

- **Lint** — Go (golangci-lint), Java (checkstyle), Python (ruff)
- **Test** — Full Go/Java/Python test suites with coverage
- **Fuzz** — Go/Java fuzz testing
- **Benchmark** — Go/Java performance benchmarks
- **Cross-validation** — Go/Java schema parity + DAG parity + execution result consistency
- **Codegen check** — Ensures generated code is in sync with source

### Cross-Validation

`scripts/cross-validate.sh` verifies consistency between the Go and Java engines:

1. **Schema parity** — Operator schemas exported by both codegen tools (names, param types, required flags, defaults) must match
2. **DAG parity** — Same config input must produce identical DAG output (DOT + Mermaid, including collapse) from both engines
3. **Execution parity** — Same config + request must yield identical results (after JSON normalization) from both engines
4. **Column-store parity** — Repeats execution verification in column-store mode
5. **Error parity** — Invalid configs/requests must produce the same error classification and messages
6. **Server parity** — HTTP endpoints must return matching status codes, body structure, and Content-Type
7. **Cancellation parity** — Timeout and runtime error cancellation behavior must match

### Building Cross-Validation for Downstream Projects

If you implement custom operators in both Go and Java and need to guarantee cross-language consistency, you can reuse Pineapple's parity verification framework.

#### Design Principles

1. **Fixture-driven** — All verification is based on shared JSON fixture files, not per-language hardcoded expectations
2. **Unified CLI interface** — Each engine provides the same CLI tools (`-config`, `-request`), outputting JSON results
3. **JSON normalization** — Use `sort_keys` + numeric type unification to eliminate platform differences (Go map ordering, float64/Double representation)
4. **Incrementally extensible** — A new engine backend only needs to implement the CLI interface to join the validation

#### Fixture Formats

**Operator-level fixture** (single operator behavior verification):

```json
{
  "operator": "your_operator_name",
  "cases": [
    {
      "name": "descriptive test name",
      "params": { "param1": "value" },
      "metadata": {
        "common_input": [], "common_output": [],
        "item_input": ["field"], "item_output": ["result"]
      },
      "input": { "common": {}, "items": [{"field": 1}] },
      "expected": { "items": [{"result": 2}] }
    }
  ]
}
```

**Pipeline-level fixture** (end-to-end execution verification):

```json
{
  "name": "fixture description",
  "config": { "pipeline_config": {...}, "pipeline_group": {...}, "flow_contract": {...} },
  "cases": [
    {
      "name": "case description",
      "request": { "common": {...}, "items": [...] },
      "expected": { "common": {...}, "items": [...] }
    }
  ]
}
```

**Error path fixture**:

```json
{
  "name": "error description",
  "config": { ... },
  "expected_error": { "type": "ConfigError", "message_contains": "keyword" }
}
```

#### JSON Normalization Strategy

When comparing outputs from both engines, you must eliminate these inherent platform differences:

```python
def normalize_json(text):
    """Go map order is non-deterministic; numeric types differ."""
    import json
    obj = json.loads(text)
    def unify(v):
        if isinstance(v, int): return float(v)
        if isinstance(v, list): return [unify(x) for x in v]
        if isinstance(v, dict): return {k: unify(x) for k, x in v.items()}
        return v
    return json.dumps(unify(obj), sort_keys=True)
```

#### Integration Steps for Downstream

1. Implement operators on both sides with consistent param names and `$metadata` declarations
2. Create fixture files in a shared directory
3. Write a validation script: invoke both CLIs, normalize outputs, compare byte-for-byte
4. Add to CI: failures block merges

See `scripts/cross-validate.sh` for a complete production implementation.

## Documentation

| Category | Link |
|----------|------|
| Design docs | [`design_doc/`](design_doc/) — Architecture, data model, operator registration, observability, etc. |
| Operator reference | [`doc/operators/`](doc/operators/README.md) — Detailed docs for all built-in operators |
| Pipeline authoring | [`doc/guide_pipeline-en.md`](doc/guide_pipeline-en.md) — Apple DSL usage guide |
| Operator development | [`doc/guide_operator-en.md`](doc/guide_operator-en.md) — Go operator development guide |
| Third-party extensions | [`design_doc/12_distribution-en.md`](design_doc/12_distribution-en.md) — Add custom operators without modifying source |
| API reference | [`doc/api-en.md`](doc/api-en.md) — HTTP endpoint documentation |

## License

[MIT](LICENSE)
