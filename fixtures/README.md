# Operator Test Fixtures

JSON fixture files for cross-language operator validation.

## Schema

Each file is named `{operator_name}.json` and contains:

```json
{
  "operator": "string — registered operator type_name",
  "cases": [
    {
      "name": "string — human-readable test case name",
      "params": { "key": "value" },
      "metadata": {
        "common_input": ["field1"],
        "item_input": ["field2"],
        "common_output": ["field3"],
        "item_output": ["field4"]
      },
      "input": {
        "common": { "field1": "value" },
        "items": [ { "field2": "value" } ]
      },
      "expected": {
        "common": { "field3": "value" },
        "items": [ { "field4": "value" } ],
        "added_items": [ { "key": "value" } ],
        "removed_indices": [0, 2],
        "warnings": ["optional warning substring match"]
      }
    }
  ]
}
```

## Expected output semantics

- `expected.common`: fields that must appear in common writes (subset match)
- `expected.items`: for Transform/Recall — the full expected items list after execution
- `expected.added_items`: for Recall — items produced by the operator
- `expected.removed_indices`: for Filter — indices of items removed
- `expected.warnings`: if present, each string must be a substring of a warning message
- Omitted fields in `expected` are not checked

## File naming

`fixtures/{operator_name}.json` — one file per operator, e.g. `fixtures/filter_condition.json`
