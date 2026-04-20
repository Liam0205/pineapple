# Apple Compiler Architecture

This document explains how the Apple DSL records flows and compiles them into the JSON contract consumed by the Go engine.

## Scope

Use this doc when a task touches:

- `apple/flow.py`
- `apple/compiler.py`
- `apple/validator.py`
- `apple/control.py`
- `apple/resource.py`
- `apple/base.py`
- `apple_generated/`

## Role in the system

Apple is the declaration side of Pineapple. It does not execute pipelines. Its job is to:

- provide a Python API for flow declaration
- record operator calls as structured `OpCall` values
- validate declaration correctness before runtime
- lower control flow into plain operators plus skip fields
- emit JSON matching `internal/config/types.go`

The compiler's output is the durable boundary between Python and Go.

## Declaration API

### Flow and SubFlow

`apple/flow.py` defines two main user-facing builders:

- `Flow` — top-level declaration with input/output contract and resources
- `SubFlow` — reusable operator fragments with no independent contract

Both inherit `_FlowBase`, which owns the operator list and control-flow bookkeeping.

### Two dispatch paths

Apple supports two ways to declare operators.

#### Dynamic dispatch

`_FlowBase.__getattr__` turns unknown attribute access into operator recording, so code like `flow.transform_copy(...)` or even `flow.some_future_op(...)` is accepted.

Characteristics:

- no static typing requirement
- operator name is taken directly from the called attribute
- metadata kwargs and business params are separated at runtime

This is the baseline API and explains why the wheel does not need `apple_generated/` to function.

#### Typed dispatch

`apple_generated/operators.py` contains generated helper classes that inherit `apple.base.BaseOp`.

Characteristics:

- generated from Go operator schemas
- typed `__call__` signatures for params and metadata kwargs
- ultimately call `BaseOp._apply()` to append an `OpCall`

These are development-time conveniences for typed authoring, not a distinct execution path.

### `OpCall` as the compiler IR

`apple/base.py` defines `OpCall`, the intermediate representation recorded by both dispatch styles. It stores:

- `type_name`
- business params
- metadata fields (`common_input`, `common_output`, `item_input`, `item_output`)
- defaults
- control-flow fields like `skip` and `for_branch_control`
- merge ancestry (`sources`)
- `row_dependency`
- `debug`
- `code_info`
- optional explicit `name`

Compilation operates over ordered `OpCall` values.

## Control flow lowering

The Go engine has no native if/else construct. Apple lowers control flow entirely in the compiler.

### User-facing API

`_FlowBase` provides:

- `if_(condition)`
- `elseif_(condition)`
- `else_()`
- `end_if_()`

These mutate an internal control-block stack and emit control operators.

### Lowering strategy

`apple/control.py` converts each branch into a `transform_by_lua` operator created by `make_control_op`.

Each branch writes a compiler-generated common field such as:

- `_if_1`
- `_elif_1`
- `_else_1`

Branch operators declared inside the block receive:

- `skip=<that control field>`
- an added `common_input` dependency on the same field

Runtime meaning:

- control operator returns `false` when the branch should execute
- control operator returns `true` when downstream branch operators should skip

So the scheduler's skip convention is `true = skip`, `false = run`.

### Condition field extraction

For control operators, `extract_fields()` heuristically scans the Lua condition string for referenced field names. Those fields are added to the control operator's `common_input` set, along with prior branch-control fields for `elseif` and `else` logic.

## Compile pipeline

`apple/compiler.py` performs a fixed sequence. The pipeline is important because later steps assume earlier steps have already stabilized ordering and naming.

### Step 1: flatten sub-flows

All declared `SubFlow` operator lists are concatenated first, followed by the main flow's own operators.

The compiler also records each sub-flow's `[start, end)` slice so it can rebuild `pipeline_map` entries later.

### Step 2: generate unique operator names

Every operator needs a stable JSON key.

Naming rules:

- explicit `name=` is used as-is
- explicit names must be globally unique
- auto names use `{type_name}_{MD5[:6].upper()}`
- if an auto-name collision occurs, the compiler appends `_N`

This creates the ordered named sequence used by all later phases.

### Step 3: run the four validation passes

Validation is fail-fast and runs in a specific order.

1. `validate_no_underscore_output`
2. `validate_field_coverage`
3. `validate_write_without_read`
4. `detect_dead_code`

The ordering matters because each later rule assumes the operator sequence and field sets are already sensible enough for the next analysis.

### Step 4: build the operators dict

The compiler emits one JSON object per named operator with:

- `type_name`
- `$metadata`
- optional `$code_info`
- `recall`
- `sources`
- `skip`
- `for_branch_control`
- `row_dependency`
- `item_defaults`
- `common_defaults`
- `debug`
- business params

This is the object the Go config loader later parses into `internal/config.OperatorConfig`.

### Step 5: build `pipeline_map`

Each sub-flow becomes a named pipeline containing the operator names assigned to that fragment. The main flow's own direct operators are grouped into an internal `_main_*` pipeline entry.

### Step 6: build `pipeline_group`

Apple currently emits a single group named `main` whose pipeline list preserves the flattened order of pipeline-map entries.

### Step 7: build `flow_contract`

The top-level contract copies the `Flow` declaration's:

- `common_input`
- `item_input`
- `common_output`
- `item_output`

This contract is later enforced at engine request/result boundaries.

### Step 8: validate resource references

After core operator validation, `_validate_resource_refs` scans business params for `resource_name` and checks that each name matches a declared `flow.resource(...)` entry.

### Step 9: assemble root metadata

The compiler adds:

- `_PINEAPPLE_VERSION` from `apple/_version.py`
- `_PINEAPPLE_CREATE_TIME` as UTC ISO timestamp
- optional `resource_config`

### Step 10: serialize to JSON

`compile_to_json()` is a thin `json.dumps(..., indent=2)` wrapper over the result dict.

## Validation rules

The compiler's validation logic is declaration-oriented rather than runtime-oriented.

### 1. No underscore-prefixed user outputs

`validate_no_underscore_output` reserves `_`-prefixed outputs for engine/compiler internals.

Applies to:

- flow-level declared outputs
- per-operator declared outputs

Exemption:

- compiler-generated control operators marked `for_branch_control=True`

### 2. Field coverage

`validate_field_coverage` walks the named operator sequence in order.

State:

- `available_common`, seeded from the flow's common input contract
- `available_item`, seeded from the flow's item input contract

For each operator:

- all declared inputs must already be available
- then the operator's declared outputs are added to the available sets

Internal `_`-prefixed input fields are ignored so compiler-generated control fields do not trigger false missing-input errors.

### 3. Write without read

`validate_write_without_read` detects overwriting a field already produced upstream without reading it first.

Purpose:

- catch suspicious accidental overwrites in declaration order
- force authors to make dependencies explicit through input metadata

Control-flow exemption:

- operators with `skip` set are exempt
- their outputs also do not count as globally written for downstream checks

This is what allows mutually exclusive if/elseif/else branches to write the same field.

### 4. Dead code detection

`detect_dead_code` flags operators that produce outputs no downstream consumer ever reads and that the flow output contract never exposes.

Exemptions:

- recall ops
- control ops
- observe-style ops with no outputs

The compiler raises `ValidationError` if any dead operators are found.

## Key invariant: validation order must align with execution order

Validation correctness depends on the operator order used by the compiler matching the order assumptions used by the runtime.

Why this matters:

- field coverage assumes earlier declarations are the producers for later consumers
- write-without-read assumes earlier writes are the causally prior ones
- dead-code detection reasons about downstream consumption in the declared sequence

This only works because the runtime's DAG construction also uses the flattened declaration order as the base sequence for hazard tracking and tie-breaking. If compile-time order and runtime order diverged, validation could approve flows that execute differently than analyzed.

## Metadata and defaults semantics

Apple emits metadata that the Go engine consumes directly.

### `$metadata`

Per-operator metadata carries:

- `common_input`
- `common_output`
- `item_input`
- `item_output`

These fields are not documentation only. They drive:

- compiler validation
- runtime input projection
- DAG hazard inference
- generated operator docs

### Defaults

Apple can attach:

- `common_defaults`
- `item_defaults`

These become part of the config and are applied by the Go DataFrame when building an operator input snapshot.

### Debug

`debug=True` is emitted per operator and later tells the runtime to capture input/output snapshots in traces.

### Row dependency

`row_dependency=True` is declaration metadata that later tells the Go DAG builder to read the `_row_set_` sentinel during item-level dependency inference.

## Resource declaration model

`apple/resource.py` defines the declaration side of resources.

### Resource objects

- `BaseResource` is the generated resource-class base.
- `ResourceDecl` is the serialized declaration form.

A `Flow` records resources through `flow.resource(name, instance)`.

### Emitted shape

Resources are emitted under `resource_config` with:

- resource name
- resource type
- refresh interval
- params

The Go server-side resource manager later loads these definitions and injects live values into request context.

## Relationship to Go code generation

Apple's typed helper classes are generated from Go, not the other way around.

Flow of authority:

1. Go operator schema registration under `operators/`
2. Go codegen in `pkg/codegen/`
3. generated Python helper classes in `apple_generated/`
4. Apple compilation emits JSON
5. Go runtime consumes the JSON

The compiler therefore sits between generated declaration helpers and runtime config consumption.

## Important invariants to preserve

1. **Apple emits JSON, not executable runtime objects.** Keep the boundary file-based/schema-based.
2. **Validation uses flattened declaration order.** That order must stay aligned with runtime ordering assumptions.
3. **Control flow is fully lowered before runtime.** The engine should continue treating it as ordinary operators plus skip fields.
4. **Underscore-prefixed fields are reserved.** User outputs should not collide with compiler/runtime internals.
5. **Resource references are validated after operator sequence construction.** A `resource_name` param without a declaration is a compile error.
6. **Dynamic dispatch remains viable even without generated helpers.** `apple_generated/` is convenience, not the language core.

## Retrieval pointers

- Flow API and control stack behavior: `apple/flow.py`
- Compiler orchestration: `apple/compiler.py`
- Validation logic: `apple/validator.py`
- Control flow lowering helpers: `apple/control.py`
- Compiler IR and typed helper base: `apple/base.py`
- Resource declaration types: `apple/resource.py`
- Generated typed operators: `apple_generated/operators.py`
