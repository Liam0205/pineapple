# pineapple-pine

Python runtime engine for the [Pineapple](https://github.com/Liam0205/pineapple) pipeline framework.

## Installation

```bash
pip install pineapple-pine
```

## Quick Start

```python
import json
from pine.engine import Engine

config = json.dumps({
    "operators": [
        {"type_name": "recall_static", "name": "recall", "$metadata": {
            "common_input": [], "item_input": [], "common_output": [],
            "item_output": ["id", "score"]
        }, "items": [{"id": "a", "score": 1.0}]}
    ],
    "pipeline_groups": {"main": ["recall"]}
}).encode()

engine = Engine.create(config)
result = engine.execute(common={}, items=[])
print(result.common, result.items)
```

## Features

- Full DAG scheduling with data-hazard analysis
- ConsumesRowSet / MutatesRowSet / AdditiveWritesRowSet marker interfaces
- Framework-level field accessor (Strict / Default semantics)
- Lua operator support via `lupa`
- HTTP server with hot-reload
- DAG visualization (DOT / Mermaid)
- Cross-engine parity with pine-go and pine-java

## Requirements

- Python >= 3.11
- `lupa` (Lua runtime)
- `redis` (optional, for redis_get/redis_set operators)
- `httpx` (for remote_pineapple operator)

## License

Apache-2.0
