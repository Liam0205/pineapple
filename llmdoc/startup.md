# Startup Reading Order

Read these docs on every Pineapple task:

1. `llmdoc/must/conventions.md` — Cross-cutting conventions: JSON boundary, registration pattern, naming, version sync, codegen freshness, and test expectations.
   - Escalate if the task touches release/versioning, generated files, operator naming, or repo-wide contribution patterns.

2. `llmdoc/overview/project-overview.md` — Project identity, system boundaries, and why Pineapple is split across Python declaration and Go execution.
   - Escalate if the task involves entrypoints, packaging, public surface area, or where a change belongs.

3. `llmdoc/architecture/dag-engine.md` — Core execution model: engine compile, DAG inference, scheduler, DataFrame semantics, operator type rules, and row dependency.
   - Escalate if the task touches execution order, hazards, barriers, runtime bugs, operator semantics, or performance/concurrency.

4. `llmdoc/architecture/apple-compiler.md` — Python DSL declaration, compile pipeline, validation, control-flow lowering, and resource declarations.
   - Escalate if the task touches Flow APIs, JSON generation, validation errors, control flow, or DSL/runtime mismatches.

5. `llmdoc/reference/operator-contract.md` — Stable lookup reference for authoring and registering operators.
   - Escalate if the task adds or modifies operators, schemas, metadata contracts, or codegen-facing definitions.
