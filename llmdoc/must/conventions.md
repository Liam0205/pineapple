# Key Conventions

These conventions recur across most Pineapple work and should be treated as stable defaults.

## JSON config is the contract between Python and Go

The Apple DSL in `apple/` declares flows, but the Go engine only consumes JSON shaped like `internal/config/types.go`. Treat that JSON as the decoupling boundary for:

- Python DSL compilation
- Go engine loading
- test fixtures in `testdata/`
- generated artifacts and cross-language integration tests

A change that crosses the Python/Go boundary should usually preserve or intentionally evolve this JSON contract rather than add a runtime bridge.

## Operator names encode operator type

Built-in operator names use a type-prefixed convention:

- `recall_*`
- `transform_*`
- `filter_*`
- `merge_*`
- `reorder_*`
- `observe_*`

This naming matters in several places:

- humans infer runtime semantics from the prefix
- the Apple DSL infers `recall=true` from the `recall_` prefix in `apple/flow.py`
- generated docs and helper classes preserve these stable names

Do not introduce operator names that hide their type category.

## Registration is side-effect based

Operators and resources register themselves with `init()` functions and public wrappers:

- operators call `pine.Register(...)`
- resources call `pine.RegisterResource(...)`

Blank imports are the normal aggregation mechanism. `operators/all.go` exists so entrypoints such as `cmd/pineapple-server/main.go` and `cmd/pineapple-codegen/main.go` can register the full built-in operator set by importing `operators` for side effects.

When a binary or test depends on built-in operators, check the blank imports first.

## Version sync spans three file groups

Pineapple versions are intentionally synchronized across:

- `version.go`
- `apple/_version.py`
- JSON fixtures carrying `_PINEAPPLE_VERSION`, including `pipeline.json` and files in `testdata/`

`scripts/bump-version.sh` is the established path for keeping them aligned. A version bump is incomplete if only one language constant changes.

## Generated code must stay fresh

Generated artifacts are checked into the repo and must match current schemas. The key generated outputs are:

- `apple_generated/`
- `doc/operators/`

CI enforces freshness through `.github/workflows/ci.yml`, which runs the codegen binary and fails on `git diff --exit-code`. If a change touches operator schemas, codegen templates, or resource schemas, regenerate artifacts before considering the work complete.

## Go schemas are the source of truth for operators

Operator contracts originate in Go registration under `operators/` plus `internal/types/operator.go` and `internal/registry/registry.go`. The Python DSL and generated helpers consume these contracts but do not supersede them.

In practice this means:

- schema fixes belong in Go registration first
- generated Python classes should be treated as derived output
- Markdown operator docs are also derived output

## Test changes should follow the existing test shape

Pineapple's durable test pattern has four layers:

1. unit tests for runtime/config/registry/resource subsystems
2. unit tests for each built-in operator package
3. engine and integration tests in Go using real or test-only operators
4. Python DSL tests, including cross-language JSON-to-Go execution

Prefer extending the nearest existing layer instead of creating a one-off test style.

## Concurrency assumptions are deliberate

The engine is built once and reused concurrently. Operators are initialized once, then executed for many requests. Any operator implementation should assume `Execute` may run concurrently across requests and should not rely on request-local mutable state stored on the operator struct unless it is synchronized or immutable.

## Codegen is a build-time bridge, not a runtime bridge

`cmd/pineapple-codegen/main.go` reads the Go registry and emits helper code and docs. It does not create a runtime integration path. Preserve the current architecture where:

- Python declares
- JSON carries the contract
- Go executes
