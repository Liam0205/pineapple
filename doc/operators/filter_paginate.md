# filter_paginate

**Type**: Filter

Keeps only items in the [page*size, page*size+size) range, removes the rest.

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | `[<page_field>, <size_field>]` |
| CommonOutput | `[]` |
| ItemInput | `[]` |
| ItemOutput | `[]` |

## DSL Usage

```python
flow.filter_paginate(
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
```
