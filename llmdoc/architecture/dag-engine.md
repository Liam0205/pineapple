# DAG Engine Architecture

This document describes Pineapple's deepest execution model: how JSON becomes an immutable engine, how the DAG is inferred, how operators are scheduled, and which invariants preserve correctness.

## Scope

Use this doc when a task touches:

- `pine.go`
- `internal/config/`
- `internal/dag/`
- `internal/runtime/`
- `internal/dataframe/`
- `internal/types/`

It is the core runtime retrieval path.

## Engine lifecycle

`pine.go` builds an `Engine` once and reuses it across requests. The engine itself is immutable after construction and safe for concurrent `Execute()` calls.

### Four-step compile pipeline

`pine.NewEngine()` follows a fixed compile pipeline:

1. **Parse JSON config** via `internal/config.Load`.
   - Reads the root config.
   - Splits reserved engine keys from business params.
   - Validates minimum config structure.

2. **Expand operator sequence** via `internal/config.ExpandOperatorSequence`.
   - Resolves `pipeline_group["main"]`.
   - Follows `pipeline_map` ordering.
   - Produces the flat operator sequence used by both validation and DAG construction.

3. **Build operator instances** via `internal/registry.BuildOperator`.
   - Looks up the registered schema.
   - Filters reserved keys out of params.
   - Applies defaults and required-param checks.
   - Calls `factory()` and then `Init(params)`.
   - Applies engine-owned wiring for `MetadataAware` and `DebugAware` operators.

4. **Build the DAG** via `internal/dag.Build`.
   - Infers barrier edges.
   - Infers data hazard edges.
   - Adds explicit `sources` edges.
   - Runs topological validation.

The output is an immutable `runtime.Plan` containing the graph, compiled operators, and flow contract.

## Per-request execution lifecycle

`Engine.Execute()` uses the precompiled plan but creates fresh request state:

1. Validate the incoming request against the flow contract.
2. Create a request-local `internal/dataframe.Frame` from request common fields and items.
3. Run the scheduler in `internal/runtime.Run`.
4. Project the final frame to the declared result fields.

This split is important:

- compile-time work belongs in engine construction
- mutable state belongs in the request-local frame
- operator instances are shared across requests and must tolerate concurrent execution

## DAG construction model

`internal/dag/dag.go` infers dependencies from execution semantics rather than requiring the user to specify all edges manually.

### Graph model

The graph stores operators in DSL sequence order. Each node tracks predecessor and successor indexes. Name-to-index lookup allows explicit source references and merge edge construction.

Declaration order matters because the hazard tracker walks operators in sequence and derives causality from that order.

## Three-phase build algorithm

### Phase 1: barrier edges

Barrier operators are:

- Filter
- Merge
- Reorder

For every barrier operator, Pineapple adds:

- an edge from every earlier operator to the barrier
- an edge from the barrier to every later operator

This makes a barrier a total ordering fence.

Why this exists:

- filters can remove rows, changing the item set seen by all later operators
- merges combine multi-source results and must observe all earlier contributions
- reorders mutate item ordering globally and must settle before later item consumers proceed

Barrier semantics are intentionally stronger than ordinary field hazards.

### Phase 2: data hazard edges

The hazard pass runs twice:

- once for common fields
- once for item fields

Each pass uses a per-field tracker with three pieces of state:

- `lastMutWriter` — the most recent mutating writer
- `additiveWriters` — additive writers that do not conflict with each other
- `activeReaders` — readers that may force WAR edges

The pass walks operators in DSL order and handles reads before writes.

#### Read processing

When an operator reads a field, Pineapple adds RAW dependencies from:

- the latest mutating writer of that field
- all additive writers of that field

Then it may register the operator as an active reader.

Exception: Observe operators get RAW edges but do not become active readers. That prevents a logging or observation op from blocking downstream writers through WAR edges.

#### Write processing

When an operator mutates a field, Pineapple adds:

- WAW dependency from the last mutating writer
- WAW dependencies from all additive writers
- WAR dependencies from all active readers

Then it updates tracker state so this operator becomes the new mutating writer and clears reader/additive state as needed.

#### Additive versus mutating writers

This distinction is central to Pineapple's parallelism.

- **Mutating writers** overwrite or structurally change a field and therefore conflict with other accesses.
- **Additive writers** contribute independent data that downstream readers must see, but they do not conflict with each other.

On item fields, Recall operators are treated as additive writers. That means:

- recall ops writing the same logical item fields do not create WAW/WAR conflicts with each other
- downstream readers depend on all relevant recalls
- a later mutating writer still depends on every additive recall writer

This is why multiple recall stages can run in parallel before a merge or transform consumes their results.

### Phase 3: explicit merge sources

Operators with `sources` add hard edges from each named source operator. This supplements the inferred hazard graph with user-declared merge ancestry.

This matters most for merge operators that must wait for specific upstream producers even when field-level metadata alone would be insufficiently explicit.

### Final validation

After all edges are added, `TopologicalSort` validates that the graph is acyclic. Cycles indicate an impossible ordering implied by barriers, hazards, or explicit source edges.

## Row dependency model

Some operators depend on the item collection as a whole rather than on a specific item field. Pineapple models that without a separate graph mechanism.

### `_row_set_` sentinel

During the item-field hazard pass, the engine injects a synthetic sentinel field named `_row_set_`.

Rules:

- Recall operators act as additive writers of `_row_set_`.
- Barrier operators reset the `_row_set_` tracker.
- Operators with `RowDependency=true` are treated as readers of `_row_set_`.

This captures collection-level causality such as:

- an operator like `transform_size` needing the complete recalled row set before computing item count
- waiting on row-producing recalls without inventing fake business field names

The sentinel is internal only. User flows should not treat it as a real field.

## Scheduler architecture

`internal/runtime/scheduler.go` executes the compiled graph.

### Scheduling model

The scheduler uses:

- one goroutine per operator
- one done channel per operator
- predecessor waiting via channel close/broadcast
- a single shared mutex guarding frame access

Each goroutine:

1. Waits for all predecessor done channels or context cancellation.
2. Checks its skip condition, if any.
3. Builds an input snapshot from the shared DataFrame under lock.
4. Runs `Execute` outside the lock.
5. Validates output against the operator type contract.
6. Applies output back to the frame under lock.
7. Records traces and stats.
8. Closes its done channel so dependents can proceed.

### Why a single mutex is sufficient

Operators execute concurrently, but the shared frame is mutated serially under one mutex. This design keeps the correctness boundary simple:

- reading a snapshot and writing results are synchronized
- operator business logic runs without holding the lock
- the DAG provides the semantic order; the mutex provides memory safety for shared state

There is no per-field or per-row locking. Pineapple prefers a simple scheduling core over a more fragmented locking scheme.

### Skip handling

Control flow is compiled into ordinary operators plus a `skip` common-field reference. At runtime the scheduler reads that field under lock before execution:

- `true` means skip execution
- `false` means execute normally

A skipped operator still participates in the graph and trace stream, but its business logic is not run.

### Error handling

Each operator goroutine is wrapped with panic recovery.

Failure behavior:

- the first fatal error wins
- `sync.Once` records it and calls `cancel()` on the shared context
- waiting goroutines unblock on cancellation and stop early
- panic paths are wrapped as `PanicError`
- operator-returned failures become `ExecutionError` or propagate through the engine's typed error model

Warnings are separate from fatal errors. Operators can emit warnings through `OperatorOutput.SetWarning`, and execution continues.

### Debug traces

When an operator config has `debug=true`, the scheduler captures:

- input snapshot
- output snapshot
- timing data
- skipped status

These populate `internal/types/trace.go` records and are returned in the final result.

## DataFrame invariants

`internal/dataframe/dataframe.go` is the request-local mutable store.

### Structure

The frame holds:

- `common map[string]any`
- `items []map[string]any`

`New` shallow-copies request input so later mutations do not alias caller-owned maps.

### Input projection

`BuildInput` projects the frame to the operator's declared metadata contract:

- only declared fields are exposed
- defaults from `common_defaults` and `item_defaults` are applied for nil values

This means operator behavior depends on its metadata contract, not on unrestricted access to the full frame.

### Apply order invariant

`ApplyOutput` always applies an operator's output in this order:

1. common writes
2. item field writes
3. item removals
4. item reorder
5. item additions

This order is load-bearing. It ensures structural item mutations happen after ordinary field writes but before recall additions are appended.

Consequences:

- a transform can safely write fields before a later filter removes rows
- reorder always applies to the surviving current rows, not to rows that will be added afterward
- recall additions always arrive after the current row set has been filtered/reordered for that operator's turn

Any change to this order would change runtime semantics and must be treated as an architecture change.

### Result projection

`ToResult` projects the final frame through the flow contract's declared output fields. Empty output lists mean "return everything currently present" for that dimension.

## Operator type constraints

`internal/types/operator.go` defines six operator types and validates which `OperatorOutput` methods they may use.

### Type table

| Operator type | Runtime role | Allowed output methods | Barrier |
|---|---|---|---|
| Recall | Produce new items | `AddItem` only | No |
| Transform | Mutate field values | `SetCommon`, `SetItem` | No |
| Filter | Remove rows | `RemoveItem` only | Yes |
| Merge | Combine or deduplicate row sets | `SetItem`, `RemoveItem` | Yes |
| Reorder | Change row order | `SetItemOrder` only | Yes |
| Observe | Read-only side effects | none | No |

These restrictions are checked after `Execute()` returns. They are a runtime enforcement of the operator taxonomy.

### Why the taxonomy matters to DAG inference

The DAG builder relies on operator type, not just metadata fields, to infer semantics:

- barriers create total fences
- recalls are additive item writers
- observe ops do not create active-reader WAR pressure
- row-dependent transforms become `_row_set_` readers when configured

Changing type semantics can therefore affect both validation and scheduling.

## Config and metadata semantics that feed runtime behavior

The runtime depends on several config-owned fields from `internal/config/types.go`:

- `$metadata` — declared common/item inputs and outputs
- `skip` — control-flow guard field
- `recall` — declaration hint preserved from DSL/codegen conventions
- `sources` — explicit upstream source references
- `debug` — trace capture toggle
- `row_dependency` — enables `_row_set_` reads
- `common_defaults` / `item_defaults` — input defaulting during snapshot build
- `for_branch_control` — marks compiler-generated control operators

Although these originate from the DSL or hand-written JSON, their semantics are enforced in the Go runtime.

## Resource and server integration

Resources and HTTP serving sit beside the engine rather than inside the DAG core.

- `pkg/resource/` manages named resources with background refresh and atomic reads.
- `pkg/server/server.go` loads the engine, starts resources, injects them into request context, and serves `/health`, `/execute`, and `/stats`.

This separation matters: DAG execution depends only on the request context and compiled plan, not on server-specific logic.

## Important invariants to preserve

1. **Validation and execution assume the same operator order base.** The flattened DSL/config sequence is the canonical order for hazard inference.
2. **Barrier operators are total fences.** Do not weaken them casually; many execution-order guarantees rely on this.
3. **Recall writes are additive on item fields.** Parallel recall behavior depends on this distinction.
4. **Observe operators are non-blocking readers.** They should not create WAR edges.
5. **`_row_set_` is internal sentinel state.** It models row-set causality without becoming user-visible data.
6. **DataFrame apply order is fixed.** Common writes, item writes, removals, reorder, additions.
7. **Operator instances are shared.** `Init()` happens once; `Execute()` must be concurrency-safe.
8. **The scheduler serializes frame access with one mutex.** Parallelism happens in operator execution, not unsynchronized frame mutation.

## Retrieval pointers

- Engine compile and request lifecycle: `pine.go`
- Config parsing and sequence expansion: `internal/config/load.go`, `internal/config/types.go`
- DAG inference: `internal/dag/dag.go`
- Scheduler and trace capture: `internal/runtime/scheduler.go`
- Stats: `internal/runtime/stats.go`
- Frame behavior: `internal/dataframe/dataframe.go`
- Operator taxonomy and output validation: `internal/types/operator.go`
- Shared request/result/trace/error types: `internal/types/`
