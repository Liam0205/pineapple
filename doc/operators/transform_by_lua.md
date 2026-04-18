# transform_by_lua

**Type**: Transform

Executes a Lua script for per-item or per-common computation.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| function_for_common | string | No | `""` | Function name to call once for all items. |
| function_for_item | string | No | `""` | Function name to call per item. |
| lua_script | string | Yes | - | Lua source code defining the function to call. |

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | `[<common fields read as scalar globals>]` |
| CommonOutput | `[<return values from function_for_common>]` |
| ItemInput | `[<item fields — scalars in item mode, lists in common mode>]` |
| ItemOutput | `[<return values from function_for_item>]` |

## DSL Usage

```python
flow.transform_by_lua(
    function_for_common=...,
    function_for_item=...,
    lua_script=...,
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
