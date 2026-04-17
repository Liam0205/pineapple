# Apple Python DSL Report

## Overview

Apple is the Python DSL layer for declaring Pineapple pipelines. It provides a declarative API for composing operators into flows, compiles to JSON config, and validates field dependencies at declaration time.

## Architecture

```
apple/
├── __init__.py         # Package init, re-exports Flow, SubFlow
├── base.py             # BaseOp class, OpCall dataclass
├── flow.py             # Flow, SubFlow — pipeline declaration + operator chaining
├── control.py          # if_/elseif_/else_/end_if_ → Lua control operators
├── compiler.py         # Flow → JSON compilation
├── validator.py        # Field coverage, write-without-read, dead code detection
├── generated/          # Auto-generated operator classes (from pineapple-codegen)
│   ├── __init__.py
│   └── operators.py
└── tests/
    ├── test_flow.py      # Flow/SubFlow composition tests
    ├── test_compiler.py  # JSON output, naming, control flow lowering
    ├── test_validator.py # Validation error detection
    └── test_e2e.py       # DSL → JSON → Pine engine execution
```

## Core API

### Flow Declaration

```python
flow = Flow(
    name="example",
    common_input=["user_age"],
    item_output=["item_score"],
)
```

### Operator Chaining

```python
flow.recall_static(
    item_output=["item_id", "item_price"],
    items=[...],
).filter_condition(
    item_input=["item_status"],
    field="item_status", value="offline",
).reorder_sort(
    item_input=["item_score"],
    field="item_score", order="desc",
)
```

### Control Flow

```python
flow.if_("user_age > 18") \
    .reorder_sort(...) \
.elseif_("user_age > 12") \
    .lua(...) \
.else_() \
    .lua(...) \
.end_if_()
```

Control flow compiles to Lua control operators + `skip` fields per the design doc.

### SubFlow Composition

```python
recall_stage = SubFlow(name="recall").recall_static(...)
rank_stage = SubFlow(name="ranking").reorder_sort(...)
flow = Flow(name="full", sub_flows=[recall_stage, rank_stage])
```

### Compilation

```python
json_str = flow.compile()        # JSON string
cfg_dict = flow.compile_dict()   # dict
```

## Compilation Steps

1. Flatten sub_flows into single operator sequence
2. Auto-generate unique operator names: `{type_name}_{hash6}`
3. Validate field coverage (all inputs provided by flow contract or upstream)
4. Validate write-without-read (overwriting operator output without reading)
5. Dead-code detection (when output contract declared)
6. Lower control flow to Lua operators + skip fields
7. Emit JSON with `pipeline_config`, `pipeline_group`, `flow_contract`

## Validation

### DSL-Side Checks

| Check | Behavior |
|-------|----------|
| Field coverage | Every `common_input`/`item_input` must be from flow contract or upstream `*_output` |
| Write-without-read | Writing an operator-produced field without reading it → error |
| Dead code | If output contract declared, operators whose output is not consumed → error |
| Control flow syntax | Mismatched if_/elseif_/else_/end_if_ → error |

### Control Flow Lowering

Each `if_`/`elseif_`/`else_` branch compiles to:
- A Lua control operator that outputs `_if_N` / `_elif_N` / `_else_N`
- Business operators in the branch get `skip: "_if_N"` in their config
- Chain dependency: `elseif` reads prior control fields, `else` reads all prior fields
- Nested if blocks are fully supported

## Testing

### Python Tests (27 total)

| Suite | Tests | Scope |
|-------|-------|-------|
| `test_flow.py` | 6 | Flow/SubFlow composition, chaining, JSON output |
| `test_compiler.py` | 7 | Control flow lowering, operator naming, pipeline map |
| `test_validator.py` | 7 | Field coverage, write-without-read, dead code |
| `test_e2e.py` | 4 | JSON structure, control flow JSON, subflow JSON, Go engine execution |

### End-to-End Path

```
Python DSL → compile() → JSON file → go test → Pine engine → validate results
```

The `test_go_engine_executes_dsl_json` test:
1. Declares a pipeline: recall → Lua discount → sort
2. Compiles to `testdata/e2e_apple_dsl.json`
3. Runs `go test ./integration/ -run TestAppleDSLe2e`
4. Validates: young user gets 20% discount, items sorted desc by price

### Go Integration Test

`integration/apple_e2e_test.go` — loads the Apple-generated JSON, executes with young and adult users, validates prices and sort order.

## Files Added/Modified

| File | Change |
|------|--------|
| `apple/__init__.py` | New — package init |
| `apple/base.py` | New — BaseOp, OpCall |
| `apple/flow.py` | New — Flow, SubFlow, operator chaining, control flow |
| `apple/control.py` | New — control flow lowering |
| `apple/compiler.py` | New — Flow → JSON compilation |
| `apple/validator.py` | New — field coverage, write-without-read, dead code |
| `apple/tests/test_flow.py` | New — 6 tests |
| `apple/tests/test_compiler.py` | New — 7 tests |
| `apple/tests/test_validator.py` | New — 7 tests |
| `apple/tests/test_e2e.py` | New — 4 tests including Go engine e2e |
| `integration/apple_e2e_test.go` | New — Go test for Apple-generated JSON |
| `testdata/e2e_apple_dsl.json` | New — Apple-generated test config |
