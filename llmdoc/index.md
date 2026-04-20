# Pineapple llmdoc Index

This index is the global map for Pineapple's durable documentation. It lists the stable docs by category and points to the right retrieval path for each concept.

## must/

- `llmdoc/must/conventions.md` — Cross-codebase conventions for operator naming, JSON as the Python/Go contract, blank-import registration, version synchronization, codegen freshness, and test expectations.

## overview/

- `llmdoc/overview/project-overview.md` — What Pineapple is, where its system boundaries are, and the high-level design decisions behind the Python DSL plus Go runtime split.

## architecture/

- `llmdoc/architecture/dag-engine.md` — Core engine architecture: config compile pipeline, DAG inference rules, scheduler model, DataFrame semantics, operator type constraints, and row-dependency behavior.
- `llmdoc/architecture/apple-compiler.md` — Python DSL architecture: Flow declaration API, compile pipeline, validation rules, control-flow lowering, and resource declaration handling.

## guides/

- No stable workflow guides yet.

## reference/

- `llmdoc/reference/operator-contract.md` — Operator authoring reference: interface, schema registration contract, optional metadata/debug hooks, type/output restrictions, reserved JSON keys, and naming conventions.

## memory/

- No recorder-owned memory docs created during init.
