English | [简体中文](README.md)

# Pineapple

High-performance DAG pipeline engine. **Declare in Python, execute in Go/Java/C++ (three engines), decouple via JSON.**

Operators declare their input/output fields; the engine automatically infers dependencies, builds the DAG, and schedules parallel execution — you focus on business logic, Pineapple makes it fast.

Suitable for any scenario requiring **multi-step data processing pipelines**: search/recommendation/ad ranking, feature engineering, real-time data processing, rule engines, ML pre/post-processing, etc.

> **⚠️ Pre-1.0**: APIs and behavioral semantics may change incompatibly between versions. Pin to a specific version for production use.

## Architecture

```
Python DSL (Apple)  ──compile──>  JSON Config
                                      │
                          ┌───────────┼───────────┐
                          v           v           v
                   Pine-Go (Go)  Pine-Java     Pine-C++
                   Build DAG      Build DAG     Build DAG
                   parallel exec  parallel      per-node parallel
```

| Component | Language | Role |
|-----------|----------|------|
| **Apple** | Python | Declarative DSL, compiles to JSON config |
| **Pine-Go** | Go | Primary execution engine: parse config, build DAG, parallel scheduling |
| **Pine-Java** | Java | Second execution engine, behavior-consistent with Pine-Go |
| **Pine-C++** | C++23 | Third execution engine (benchmark runtime), full parity + performance ceiling |

**Engineering teams** develop high-performance operators in Go/Java/C++; **product teams** compose logic with the Python DSL. The two sides are fully decoupled via JSON config.

> The former Pine-Python runtime engine was removed after v0.9.7. The Python code in this repository is the Apple DSL declaration layer (a compiler), not a runtime.

## Key Features

- **Implicit graph construction** — Operators declare input/output fields; engine infers DAG dependencies with transitive reduction
- **Lock-free parallelism** — Independent operators in the DAG execute in parallel automatically
- **Compile-time validation** — Dead code, missing fields, write-after-write detected before deployment
- **Embedded Lua** — Built-in Lua operators for lightweight custom computation. pine-go defaults to [wangshu](https://github.com/Liam0205/wangshu) (pure-Go Lua 5.1 VM, NaN-boxing + arena GC); switch back to gopher-lua via `-tags=lua_gopher`. pine-java uses LuaJC (bytecode compilation), pine-cpp uses LuaJIT. End-to-end overhead ~1.2-2x; isolated operator-level overhead varies by runtime and compute complexity (C++/LuaJIT ~3-5x, Java ~2-9x, Go ~6-17x) — write native operators for compute-heavy hot paths
- **Hot config reload** — Service automatically reloads engine config without downtime
- **Dynamic resources** — Two-channel resource manager: **data-typed** (e.g. static dict / real-time feature store, snapshot-exported lock-free reads) + **handle-typed** (e.g. `redis_connection`, borrow lease + RAII teardown); background-refreshed
- **Redis cascade-safety** — The `redis_connection` resource exposes 5 cascade params (`{dial,read,write,pool}_timeout_ms` + `pool_size`); per-command metrics `pine_redis_command_*` with 4-state status (ok / timeout / pool_timeout / error), fail-on-error silent-degradation contract
- **White-box observability** — Operator-level traces; the `/stats` composite response includes `/stats.http` (request-level 4-state metrics) + `/stats.resources` (resource pool / probe / per-command 4-state categories); pluggable Prometheus interface
- **Row/Column storage** — DataFrame supports both storage modes
- **Tri-engine consistency** — Go/Java/C++ engines verified byte-exactly via CI cross-validation (19 sections + tri-engine differential fuzz + daily ASan/TSan sanitized fuzz)
- **Pine-C++ benchmark runtime** — Complete third runtime with operator parity, HTTP server (hot reload / graceful shutdown), ColumnFrame/RowFrame dual physical layouts, lazy OperatorInput projection, LuaJIT integration, metrics/resource parity

## Quick Start

### Prerequisites

- Go 1.26+ (Pine-Go)
- Java 25+ (Pine-Java)
- Python 3.11+ (Apple DSL)

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
├── pine-cpp/               # C++ execution engine (Pine-C++)
│   ├── include/pine/       #   Public headers
│   ├── src/                #   config/dag/dataframe/runtime/server/lua/redis/http/resource
│   ├── operators/          #   Built-in operators (parity with Go/Java) + bench stubs (PINE_BUILD_BENCH_STUBS)
│   ├── cmd/                #   pineapple-run / pineapple-render-dag / pineapple-server / pineapple-codegen / pineapple-cause-chain-probe
│   └── tests/              #   doctest unit suite
├── fixtures/               # Shared test fixtures (used by all three engines)
│   ├── operators/          #   Operator-level unit fixtures
│   ├── pipelines/          #   Pipeline-level end-to-end fixtures
│   ├── errors/             #   Error path fixtures
│   ├── error_chain/        #   ExecutionError cause-chain fixtures
│   ├── server_byte_exact/  #   Byte-exact server response fixtures
│   └── benchmarks/         #   Benchmark configs/requests (incl. calibrated production proxies)
├── scripts/                # Developer scripts
├── design_doc/             # Design documents
└── doc/                    # Generated operator docs & reports
```

## Development

### Top-level Make Targets

Cross-language fmt / lint / test / bench / codegen / version management is unified behind the top-level `Makefile` (with `pine-go/Makefile` for Go-specific work). CI and local dev share the same command sequence.

| Make target | Purpose |
|---|---|
| `make fmt` | Format all four languages (gofmt / google-java-format / clang-format / ruff) |
| `make lint` | Lint all four languages (incl. checkstyle `failOnViolation=true`, `-Werror`) |
| `make test` | Full test suite across runtimes |
| `make bench` | Default `pine_bench` tag |
| `make bench-cross-runtime` | Cross-engine fixture-driven benchmark (cgroup-isolated) |
| `make bench-lua-backends` | wangshu vs gopher-lua, same-host serial + benchstat |
| `make differential-fuzz` | Tri-engine differential fuzz |
| `make cross-validate` | Tri-engine consistency verification |
| `make codegen` | Generate `apple_generated/` + `doc/operators/` from pine-go Registry |
| `make codegen-check` | CI: codegen + `git diff --exit-code` to enforce artifact freshness |
| `make check-pr-ci` | Watch CI status of the current branch's PR (pre-push hook calls this) |

### Scripts

`scripts/` holds the actual implementations behind the Make targets and can be invoked standalone:

| Script | Purpose |
|--------|---------|
| `scripts/go-test.sh` | Run all Go tests |
| `scripts/java-test.sh` | Run all Java tests |
| `scripts/test-all.sh` | Run Go + Apple (Python) + Java tests |
| `scripts/lint.sh` | Lint Go + Java + Python |
| `scripts/go-bench.sh` | Go benchmarks |
| `scripts/java-bench.sh` | Java benchmarks |
| `scripts/bench-cross-runtime.sh` | Cross-engine HTTP server benchmark (fixture-driven, cgroup-isolated) |
| `scripts/bench-lua-backends.sh` | wangshu vs gopher-lua backend comparison (benchstat delta) |
| `scripts/go-fuzz.sh` | Go fuzz testing |
| `scripts/java-fuzz.sh` | Java fuzz testing |
| `scripts/differential-fuzz.sh` | Tri-engine differential fuzzing (random pipelines, output diff) |
| `scripts/cross-validate.sh` | Tri-engine cross-validation (schema + DAG + execution + errors + server + metrics, 19 sections) |
| `scripts/cpp-sanitizer-smoke.sh` | C++ ASan/UBSan smoke |
| `scripts/cpp-tsan-smoke.sh` | C++ ThreadSanitizer high-fanout stress |
| `scripts/codegen.sh` | Code generation (`--backend go\|java`) |
| `scripts/render-dag.sh` | DAG visualization (`--backend go\|java`) |
| `scripts/apple-compile.sh` | Compile Apple DSL to JSON |
| `scripts/run-pipeline.sh` | One-shot pipeline execution |
| `scripts/bump-version.sh` | Synchronize version across all components (incl. pine-cpp `kVersion`) |
| `scripts/check-pr-ci.sh` | Watch CI status of the current branch's PR (pre-push hook invokes this) |

### Local Git Hooks

`.githooks/` ships with the repository; activate via `git config core.hooksPath .githooks` once after clone:

- **`pre-commit`** — staged-only format gate (gofmt / clang-format / ruff); does not touch unstaged work
- **`pre-push`** — project-level lint (four-language fail-on-violation) + self-wrapped post-push CI watcher (auto-runs `check-pr-ci.sh` after the actual push) + auto `--set-upstream` relay (first-push of a new branch does not need a manual `-u`)

### CI Pipeline

CI runs automatically on every push/PR:

- **Lint** — Go (golangci-lint), Java (checkstyle, failOnViolation=true), Python (ruff), C++ (clang-format -Werror)
- **Test** — Full Go/Java/Apple/C++ test suites with coverage
- **Sanitizer** — C++ ASan/UBSan smoke + ThreadSanitizer stress
- **Fuzz** — Go/Java fuzz + tri-engine differential fuzzing
- **Daily sanitized fuzz** — Daily (12:00 UTC+8) ASan/TSan differential fuzz, 3000+2000 rounds, dedicated to race / memory-bug deep diagnostics (independent of the per-push fast lane)
- **Benchmark** — Go/Java performance benchmarks
- **Cross-validation** — Tri-engine schema/DAG/execution/error/server/metrics parity
- **Codegen check** — Ensures generated code is in sync with source

### Cross-Validation

`scripts/cross-validate.sh` verifies consistency across the three engines, currently 19 sections (see `scripts/cross-validate/` for the authoritative list):

1. **Schema parity** — Operator schemas and apple_generated artifacts exported by all three codegen tools must match byte-for-byte
2. **DAG parity** — Same config input must produce identical DAG output (DOT + Mermaid, including collapse) from all engines
3. **Execution parity** — Same config + request must yield identical results (after JSON normalization)
4. **Column-store parity** — Repeats execution verification in column-store mode
5. **Error parity** — Invalid configs/requests must produce the same error classification and messages
6. **Server parity** — HTTP endpoints must return matching status codes, body structure, and Content-Type
7. **Cancellation parity** — Timeout, runtime error, and client-disconnect cancellation behavior must match
8. **Concurrent parity** — Behavior and counters under concurrent requests must match
9. **Raw-byte parity** — Un-normalized raw byte output comparison (key ordering)
10. **Hot-reload parity** — Config and `resource_config` hot-reload behavior must match
11. **Redis integration** — Redis operators behave identically against a live redis
12. **Extensibility parity** — Downstream extension patterns (middleware, unregistered paths) must match
13. **Metrics parity** — `/stats` structure and values must match (incl. lua_pool counters, data_parallel invariants)
14. **Byte-exact execute** — Server `/execute` responses must match byte-for-byte
15. **Error cause chain** — ExecutionError cause chains unwrap identically
16. **Resource metrics** — `/stats.resources` subtree matches in shape across no-traffic/under-load/unreachable states
17. **Templated params** — `{{field}}` template parameter resolution must match
18. **SubFlow contract stderr** — Apple compile-time SubFlow contract error wording stays stable
19. **Bench-stub parity** — `reorder_topn_boost` matches byte-for-byte under bench builds

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

## Benchmark

Cross-engine performance comparison (HTTP server mode, `scripts/bench-cross-runtime.sh`, 10000 requests × 16 concurrency, server cgroup-isolated to 2C/4G, re-measured 2026-06-25 / v0.10.9). `realistic_*_calibrated*` fixtures are production-proxy benchmarks calibrated against real traffic; the rest are synthetic stress tests.

### Throughput (QPS)

| Fixture | Go | Java | C++ |
|---|---|---|---|
| small_010 (10 items) | 36298 | 6318 | 20756 |
| small_050 (50 items) | 27270 | 5336 | 17227 |
| small_100 (100 items) | 19658 | 4607 | 13812 |
| medium_0100 (100 items) | 12514 | 3589 | 8542 |
| medium_0500 (500 items) | 3026 | 1965 | 2941 |
| medium_1000 (1000 items) | 1513 | 1295 | 1656 |
| large_0100 (100 items) | 7243 | 3064 | 5120 |
| large_0500 (500 items) | 1684 | 1508 | 1773 |
| large_1000 (1000 items) | 825 | 966 | 951 |
| large_5000 (5000 items) | 155 | 213 | 175 |
| realistic_for_you | 483 | 303 | 349 |
| realistic_for_you_latency | 250 | 141 | 212 |
| **realistic_for_you_calibrated (production proxy)** | **121** | **127** | **237** |
| **realistic_for_you_calibrated_2c4g** | **121** | **124** | **224** |
| **realistic_for_you_calibrated_itemlua** | **127** | **126** | **233** |

### P50 Latency (ms)

| Fixture | Go | Java | C++ |
|---|---|---|---|
| small_010 | 0.4 | 1.5 | 0.6 |
| medium_0500 | 4.9 | 6.8 | 5.3 |
| large_1000 | 18.2 | 14.3 | 15.3 |
| large_5000 | 94.3 | 68.6 | 83.4 |
| **realistic_for_you_calibrated** | **122.3** | **117.7** | **60.8** |
| **realistic_for_you_calibrated_itemlua** | **117.1** | **119.5** | **61.5** |

Highlights:

- **C++ leads by ~1.9x on production-calibrated workloads** (calibrated QPS 237 vs 121/127; P50 60ms vs 117/122ms) — this is what the "benchmark runtime" positioning means
- Go has the highest throughput on synthetic small/medium fixtures (lowest lightweight-request overhead); Java's JIT hot-loop optimization wins at large row counts (large_1000+)
- itemlua (3000 Lua calls/request, boundary-dominated shape) is statistically flat against calibrated across all three engines — confirms the "per-item boundary dominates + end-to-end dilution" calibration fact (see `llmdoc/memory/decisions/perf-evolution-roadmap.md`)
- Numbers evolve with versions. Reproduce with `make bench-cross-runtime` or `scripts/bench-cross-runtime.sh --requests 10000 --concurrency 16`; reports land in `bench-results/`

## Documentation

| Category | Link |
|----------|------|
| Design docs | [`design_doc/`](design_doc/) — Architecture, data model, operator registration, observability, etc. |
| Operator reference | [`doc/operators/`](doc/operators/README.md) — Detailed docs for all built-in operators |
| Pipeline authoring | [`doc/guide_pipeline-en.md`](doc/guide_pipeline-en.md) — Apple DSL usage guide |
| Operator development | [`doc/guide_operator-en.md`](doc/guide_operator-en.md) — Go operator development guide |
| Third-party extensions | [`design_doc/12_distribution-en.md`](design_doc/12_distribution-en.md) — Add custom operators without modifying source |
| API reference | [`doc/api-en.md`](doc/api-en.md) — HTTP endpoint documentation |
| LLM retrieval docs | [`llmdoc/`](llmdoc/) — Stable knowledge map for AI collaboration (architecture / decisions / reflections / index) |

## License

[Apache-2.0](LICENSE)
