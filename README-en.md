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
│   └── pipelines/          #   Pipeline-level fixtures
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

1. **Schema parity** — Operator schemas exported by both codegen tools (names, param types, required flags) must match
2. **DAG parity** — Same config input must produce identical DAG output (DOT format) from both engines
3. **Execution parity** — Same config + request must yield identical results (after JSON normalization) from both engines

## Documentation

| Category | Link |
|----------|------|
| Design docs | [`design_doc/`](design_doc/) — Architecture, data model, operator registration, observability, etc. |
| Operator reference | [`doc/operators/`](doc/operators/README.md) — Detailed docs for all built-in operators |
| Pipeline authoring | [`doc/guide_pipeline.md`](doc/guide_pipeline.md) — Apple DSL usage guide |
| Operator development | [`doc/guide_operator.md`](doc/guide_operator.md) — Go operator development guide |
| Third-party extensions | [`design_doc/12_distribution.md`](design_doc/12_distribution.md) — Add custom operators without modifying source |
| API reference | [`doc/api.md`](doc/api.md) — HTTP endpoint documentation |

## License

[MIT](LICENSE)
