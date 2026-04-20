# Project Overview

Pineapple is a high-performance pipeline engine for request-time data processing. Pipelines are declared in Python with the Apple DSL, compiled into a JSON configuration, and executed by a Go runtime that builds and runs a dependency-aware DAG.

## What Pineapple is

Pineapple has two primary halves:

- A Python declaration layer in `apple/` that lets users describe flows with operator chaining, sub-flows, control flow, and resource declarations.
- A Go execution layer rooted at `pine.go` and `internal/` that loads the JSON config, constructs operators, infers dependencies, and executes the pipeline concurrently.

The two halves are decoupled by JSON. The Python side does not call Go directly at runtime, and the Go side does not know about Python objects. The contract is the emitted config shape consumed by `pine.NewEngine()`.

## Why it is split this way

This split serves three goals:

- Python gives a concise authoring experience for pipeline declaration, validation, and composition.
- Go provides a concurrency-friendly runtime for request execution, DAG scheduling, and long-lived service deployment.
- JSON creates a stable boundary that supports code generation, testing, and cross-language evolution without a runtime bridge.

That design also explains the presence of `cmd/pineapple-codegen/main.go`: Go operator schemas are the source of truth for typed helpers and generated operator docs, but execution still happens through JSON configs rather than FFI or embedded interpreters.

## Core execution model

A Pineapple flow is a sequence of named operators with declared metadata about the fields they read and write. At engine construction time, Pineapple:

1. Parses the JSON config from `internal/config/`.
2. Expands the declared flow sequence from `pipeline_group` and `pipeline_map`.
3. Builds operator instances through the registry in `internal/registry/`.
4. Builds a DAG in `internal/dag/` from barriers, data hazards, and explicit merge sources.

At request time, the engine creates a request-local `internal/dataframe.Frame`, runs operators according to the DAG in `internal/runtime/`, and projects the final result through the flow contract.

## Main concepts

### Operators

Operators are the unit of business logic. They implement the public `pine.Operator` interface and are registered through schemas that declare:

- a stable type name such as `transform_copy`
- one of six operator kinds: Recall, Transform, Filter, Merge, Reorder, Observe
- parameter specs used for validation and code generation

Built-in operators live under `operators/` and self-register via `init()` plus `pine.Register(...)`. Blank-importing `operators/all.go` registers the full built-in set.

### Apple DSL

The Apple DSL in `apple/flow.py` records operator calls, lowers control flow into ordinary operators plus skip fields, validates field usage, and emits the JSON config consumed by Go. It supports both dynamic dispatch (`flow.some_op(...)`) and generated typed helpers in `apple_generated/`, though the runtime contract remains JSON.

### DAG runtime

The Go engine does not execute operators in plain declaration order. It infers dependencies from field reads and writes. This lets independent operators run in parallel while preserving ordering where hazards or barrier operators require it.

### Resources

Resources are separate from operators and are managed by `pkg/resource/`. They are declared in flow output JSON, loaded by the server-side resource manager, refreshed in the background, and injected into request context for operators that need them.

## Entry points and packaging boundaries

### Go entry points

- `cmd/pineapple-server/main.go` — Runs the HTTP server in `pkg/server/` with `/health`, `/execute`, and `/stats` endpoints.
- `cmd/pineapple-codegen/main.go` — Reads registered operator schemas and generates Python helpers and optional docs.

### Python package

`pyproject.toml` packages `apple/` as `pineapple-apple`. The checked-in `apple_generated/` package is development-time generated output and is not included in the distributed wheel. This is intentional because dynamic dispatch in `apple/flow.py` is sufficient for runtime declaration; generated classes mainly improve typed authoring inside the repo.

## Key design decisions

### JSON is the decoupling contract

The most important boundary in Pineapple is the JSON config schema rooted in `internal/config/types.go`. It decouples:

- Python declaration from Go execution
- Go operator schemas from generated Python helpers
- test fixtures from either language implementation

This is why cross-language tests use files like `testdata/e2e_apple_dsl.json` instead of a direct bridge.

### Go operator schemas are the source of truth

Operator schemas registered in Go drive:

- runtime validation in `internal/registry/registry.go`
- generated Python operator classes in `pkg/codegen/`
- generated operator docs in `doc/operators/`

The Python DSL consumes those contracts but does not redefine them authoritatively.

### Engine instances are immutable after construction

`Engine` in `pine.go` is compiled once and then shared across requests. Mutable execution state lives in the per-request DataFrame and runtime traces/stats. This keeps the public engine safe for concurrent `Execute()` calls.

### Validation happens on both sides of the boundary

- The Python DSL validates declaration-level correctness such as field coverage, dead code, and control-flow lowering assumptions.
- The Go runtime validates config shape, operator registration, operator params, request inputs, and runtime output-method restrictions.

The two layers are complementary rather than redundant.

## Quality and release shape

Pineapple's quality strategy has four visible layers:

- subsystem unit tests in `internal/` and `pkg/`
- per-operator unit tests in `operators/`
- engine/integration tests in `engine_test.go` and `integration/`
- Python DSL tests in `apple/tests/`, including a cross-language test that emits JSON and invokes Go integration tests

CI in `.github/workflows/ci.yml` runs Go tests, Python tests, and a codegen freshness check. Release in `.github/workflows/release.yml` adds wheel build and PyPI publish on `v*` tags.

## Project boundaries

Pineapple does not currently include:

- a direct Python-to-Go runtime bridge
- a multi-module Go workspace
- built-in linting or coverage gates in CI
- automatic resource hot reload in the server

Those absences matter because many changes should preserve the existing JSON-mediated, registry-driven architecture instead of introducing tighter coupling.
