# Operator Development Contract

This reference is for authors adding or modifying Pineapple operators.

## Authoritative files

Use these files as the source of truth:

- `internal/types/operator.go`
- `internal/types/operator_io.go`
- `internal/registry/registry.go`
- `operator.go`
- `operator_io.go`
- `registry.go`
- representative implementations under `operators/`

## Operator lifecycle

An operator instance goes through two phases.

1. `Init(params map[string]any) error`
   - called once during engine construction
   - receives only business params
   - reserved engine keys have already been stripped
   - defaults have already been applied

2. `Execute(ctx context.Context, input *OperatorInput, output *OperatorOutput) error`
   - called once per request execution
   - reads from the provided input snapshot
   - records writes in the provided output collector
   - may run concurrently across requests on the same operator instance

That concurrency model means any mutable state kept on the operator struct must either be immutable after `Init()` or explicitly synchronized.

## Required interface

The public operator interface is exposed through `operator.go` and defined in `internal/types/operator.go`:

- `Init(params map[string]any) error`
- `Execute(ctx, input, output) error`

Use `OperatorInput` and `OperatorOutput` from `operator_io.go` rather than reaching into runtime internals.

## Registration contract

Operators are registered through `pine.Register(schema, factory)` from `registry.go`, usually inside an `init()` function in the operator's source file.

### Required schema fields

`OperatorSchema` must provide:

- `Name` — stable operator type name such as `transform_copy`
- `Type` — one of the six operator types
- `Description` — non-empty human-readable description
- `Params` — map of business params keyed by param name
- `Factory` is not part of the schema; it is supplied separately by the registration call

Each `ParamSpec` should provide:

- `Type` — documentary/codegen type token
- `Required` — whether callers must provide it
- `Default` — optional default value
- `Description` — non-empty description

### Registration failure behavior

`internal/registry.Register` is intentionally strict and panics on invalid definitions, including:

- empty operator name
- invalid operator type
- empty operator description
- any param with empty description
- duplicate registration

Treat schema registration as startup-time validation. Missing metadata is considered a programmer error, not a recoverable runtime condition.

## Reserved JSON/config keys

These keys are engine-owned and filtered out before `Init(params)` receives its map:

- `type_name`
- `$metadata`
- `$code_info`
- `skip`
- `recall`
- `sources`
- `debug`
- `row_dependency`
- `common_defaults`
- `item_defaults`
- `for_branch_control`

Do not define business params that rely on these names.

## Optional interfaces

### `MetadataAware`

If an operator implements the metadata-aware interface from `internal/types/operator.go`, the engine will inject field metadata after `Init()`.

Typical pattern:

- embed `MetadataHolder`
- read `CommonInput`, `CommonOutput`, `ItemInput`, `ItemOutput` inside `Execute()`

This is the standard way an operator learns which fields it should read and write.

### `DebugAware`

If an operator implements the debug-aware interface, the engine injects per-operator debug settings after `Init()`.

Typical pattern:

- embed `DebugHolder`
- consult the debug info if the operator needs specialized debug behavior beyond standard runtime tracing

Most operators only need runtime trace capture, but Lua is an example of an operator that embeds both metadata and debug holders.

## Input/output API contract

### Read from `OperatorInput`

Use the read-only accessors:

- `Common(field)`
- `Item(index, field)`
- `ItemCount()`
- `CommonKeys()`
- `ItemKeys(index)`

Do not assume the full frame or arbitrary undeclared fields are present; input is projected from declared metadata.

### Write to `OperatorOutput`

Use only the output methods permitted by your operator type:

- `SetCommon`
- `SetItem`
- `AddItem`
- `RemoveItem`
- `SetItemOrder`
- `SetWarning`

`SetWarning` is orthogonal to operator type and is used for non-fatal warnings.

## Operator type table

`internal/types/operator.go` defines six operator types. Runtime validation checks the output methods used by each execution.

| Type | Intended role | Allowed output methods |
|---|---|---|
| Recall | produce new rows/items | `AddItem` |
| Transform | mutate common or item field values | `SetCommon`, `SetItem` |
| Filter | remove rows/items | `RemoveItem` |
| Merge | combine/deduplicate row sets | `SetItem`, `RemoveItem` |
| Reorder | change item order | `SetItemOrder` |
| Observe | read-only side effects | none |

Additional semantics to remember:

- Filter, Merge, and Reorder are barrier types in DAG construction.
- Recall acts as an additive writer on item fields.
- Observe operators do not create blocking WAR-reader behavior in the DAG.

## Naming conventions

Operator names should encode their category with a stable prefix:

- `recall_*`
- `transform_*`
- `filter_*`
- `merge_*`
- `reorder_*`
- `observe_*`

Reasons:

- readers can infer semantics quickly
- the Apple DSL infers recall behavior from the `recall_` prefix
- generated docs and typed helpers group by these stable families

Do not use ambiguous names that hide the operator type.

## Recommended implementation pattern

Built-in operators generally follow this structure in `operators/`:

1. package-level doc comments describing operator name, type, params, and metadata contract
2. `init()` function calling `pine.Register(...)`
3. struct embedding `pine.MetadataHolder` when metadata is needed
4. optional `pine.DebugHolder` when debug info is needed
5. `Init()` for param parsing and one-time setup
6. `Execute()` for request-time logic

Representative examples:

- recall: `operators/recall/static.go`
- transform: `operators/transform/copy.go`
- filter: `operators/filter/condition.go`
- merge: `operators/merge/dedup.go`
- reorder: `operators/reorder/sort.go`
- observe: `operators/observe/log.go`
- debug-aware transform: `operators/lua/lua.go`

## Metadata contract comments and generated docs

Go doc comments in operator source files are parsed by `pkg/codegen/docparse.go` to generate Markdown docs in `doc/operators/`.

Important boundary:

- schema registration is authoritative for name, type, params, and descriptions
- comment parsing supplements generated docs with metadata-contract sections

Keep comments consistent with actual metadata usage, but fix runtime truth in the schema and code first.

## Codegen impact

Operator schema changes affect generated artifacts:

- `apple_generated/operators.py`
- `apple_generated/__init__.py`
- `doc/operators/`

Any change to schema shape, param types/defaults, or registry contents should be followed by codegen regeneration. CI checks freshness with a generated-diff gate.

## Metadata contract expectations

Metadata fields describe the operator's declared field contract, not incidental implementation details.

Use them to express:

- which common fields are read
- which item fields are read
- which common fields are written
- which item fields are written

These declarations are consumed by multiple systems:

- Apple validator (`apple/validator.py`)
- runtime input projection (`internal/dataframe/dataframe.go`)
- DAG dependency inference (`internal/dag/dag.go`)
- generated operator docs (`pkg/codegen/`)

Incorrect metadata can therefore create both compile-time and runtime misbehavior.

## Common pitfalls

- Forgetting a param description causes registration panic.
- Using a reserved key as a business param means it will never reach `Init()`.
- Writing through the wrong output method for the operator type will fail runtime validation.
- Storing request-local mutable state on the operator struct can break concurrent execution.
- Changing schema without regenerating `apple_generated/` and `doc/operators/` will fail CI.

## Retrieval pointers

- Interface and type constraints: `internal/types/operator.go`
- IO helpers: `internal/types/operator_io.go`
- Registry validation and reserved keys: `internal/registry/registry.go`
- Public wrappers: `operator.go`, `operator_io.go`, `registry.go`
- Built-in examples: `operators/`
- Codegen consumer path: `pkg/codegen/codegen.go`, `pkg/codegen/template.go`, `pkg/codegen/docparse.go`
