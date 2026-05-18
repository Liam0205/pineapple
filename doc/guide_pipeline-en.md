# Pipeline Authoring Guide (Product/Algorithm Perspective)

## Basic Usage

```python
from apple.flow import Flow

flow = Flow(
    name="my_pipeline",
    common_input=["user_id", "user_age"],   # Request-level context fields
    item_output=["item_id", "item_score"],  # Final output fields
)
```

## Chaining Operators

All operator methods return the Flow itself, enabling method chaining:

```python
flow.recall_static(
    item_output=["item_id", "item_score"],
    items=[...],
)

flow.filter_condition(
    item_input=["item_status"],
    field="item_status",
    value="offline",
)

flow.transform_normalize(
    item_input=["item_score"],
    item_output=["item_score_norm"],
    field="item_score",
)

flow.filter_truncate(top_n=50)

flow.reorder_sort(
    item_input=["item_score_norm"],
    field="item_score_norm",
    order="desc",
)
```

## Conditional Branching

Field references in conditions use `{{field_name}}` template syntax:

```python
flow.if_("{{is_new_user}}") \
    .transform_dispatch(
        common_input=["default_score"],
        item_output=["item_score"],
        common_field="default_score",
        item_field="item_score",
    ) \
.else_() \
    .transform_by_lua(
        common_input=["user_id"],
        item_input=["item_id"],
        item_output=["item_score"],
        lua_script="...",
        function_for_item="score",
    ) \
.end_if_()
```

## SubFlow Composition and Nesting

SubFlows support arbitrary nesting depth. Operators and child SubFlows can be freely interleaved within a SubFlow:

```python
from apple.flow import Flow, SubFlow

candidates = SubFlow(name="candidates")
candidates.recall_static(item_output=["item_id", "item_score"], items=[...])

recall = SubFlow(name="recall")
recall.add_subflow(candidates)
recall.merge_all(item_input=["item_id"], item_output=["item_id"])

process = SubFlow(name="process")
process.transform_normalize(item_input=["item_score"], item_output=["norm_score"], field="item_score")

flow = Flow(
    name="main",
    common_input=["user_id"],
    item_output=["item_id", "norm_score"],
    sub_flows=[recall, process],
)
```

After compilation, SubFlow paths use `/` as a separator to represent hierarchy (e.g., `recall/candidates`).

## SubFlows Inside Branches

SubFlows can be nested inside conditional branches. The compiler automatically propagates outer branch control fields to all operators within the SubFlow:

```python
ranking = SubFlow(name="ranking")
ranking.reorder_sort(item_input=["item_score"], field="item_score", order="desc")

flow.if_("{{enabled}}") \
    .add_subflow(ranking) \
.else_() \
    .transform_dispatch(...) \
.end_if_()
```

## Resource Declarations

When operators depend on external data, declare resources in the pipeline:

```python
from apple_generated.resources import FeatureIndexResource

flow.resource("my_index", FeatureIndexResource(dsn="host:3306/db"))

flow.recall_feature_index(
    resource_name="my_index",
    item_output=["item_id", "score"],
)
```

The compiler validates that all `resource_name` references have matching resource declarations.

## Metadata Declarations

Each operator call must declare the fields it reads and writes:

| Parameter | Meaning |
|-----------|---------|
| `common_input` | Request-level fields to read |
| `common_output` | Request-level fields to write |
| `item_input` | Item-level fields to read |
| `item_output` | Item-level fields to write |
| `item_defaults` | Default values for item-level fields |
| `common_defaults` | Default values for request-level fields |
| `sources` | Data sources for merge operators |
| `debug` | Enable debug snapshots for this operator |
| `data_parallel` | Data-parallel shard count (Transform only, requires empty common_output) |

## Compilation and Validation

```python
json_str = flow.compile()       # Compile to JSON string
config = flow.compile_dict()    # Compile to dict
```

The compiler automatically performs the following validations:

- **Field coverage** — Fields read by operators must be produced upstream
- **Dead code detection** — Operators whose output fields are never consumed downstream are flagged
- **Write-after-write** — Detects the same field being written multiple times
- **Control flow integrity** — Every `if_` must have a matching `end_if_`
- **Empty branch detection** — Each branch in a control block must have at least one operator or SubFlow
- **Data-parallel constraints** — `data_parallel > 1` requires Transform type and empty `common_output`
- **Parameter-metadata consistency** — Reports errors when business parameters conflict with metadata declarations
- **Error localization** — Validation errors include the operator's SubFlow path and source location
