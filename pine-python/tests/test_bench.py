"""Benchmark tests for pine-python.

Uses pytest-benchmark to measure engine execution performance across
different pipeline configurations and sizes.
"""
from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest
from pine.engine import Engine

FIXTURES_ROOT = Path(__file__).parent.parent.parent / "fixtures"


@pytest.fixture(autouse=True, scope="module")
def register_operators():
    from pine.operators import ensure_registered
    ensure_registered()


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _load_fixture(name: str) -> dict[str, Any]:
    """Load a pipeline fixture by name."""
    path = FIXTURES_ROOT / "pipelines" / name
    return json.loads(path.read_text())


def _make_engine_from_fixture(name: str) -> Engine:
    """Create an engine from a pipeline fixture file."""
    data = _load_fixture(name)
    config = data.get("config", data)
    config_bytes = json.dumps(config).encode()
    return Engine.create(config_bytes)


def _make_engine_from_config(config: dict[str, Any]) -> Engine:
    """Create an engine from a raw config dict."""
    config_bytes = json.dumps(config).encode()
    return Engine.create(config_bytes)


def _generate_items(n: int, fields: dict[str, Any] | None = None) -> list[dict[str, Any]]:
    """Generate n items with the given fields pattern."""
    if fields is None:
        fields = {"item_id": "", "item_ctr": 0.0, "item_dur": 0.0}
    items = []
    for i in range(n):
        item = {}
        for k, default in fields.items():
            if isinstance(default, str):
                item[k] = f"{k}_{i}"
            elif isinstance(default, float):
                item[k] = float(i) * 0.1 + 0.01
            elif isinstance(default, int):
                item[k] = i
            else:
                item[k] = default
        items.append(item)
    return items


# ---------------------------------------------------------------------------
# Benchmark: Small Pipeline (recall -> sort -> truncate, 100 items)
# ---------------------------------------------------------------------------


_SMALL_PIPELINE_CONFIG = {
    "_PINEAPPLE_VERSION": "0.6.6",
    "pipeline_config": {
        "operators": {
            "recall_items": {
                "type_name": "recall_static",
                "recall": True,
                "items": _generate_items(100, {"item_id": "", "item_ctr": 0.0, "item_dur": 0.0}),
                "$metadata": {
                    "common_input": [],
                    "common_output": [],
                    "item_input": [],
                    "item_output": ["item_id", "item_ctr", "item_dur"],
                },
            },
            "sort_by_ctr": {
                "type_name": "reorder_sort",
                "order": "desc",
                "$metadata": {
                    "common_input": [],
                    "common_output": [],
                    "item_input": ["item_ctr"],
                    "item_output": [],
                },
            },
            "truncate_top": {
                "type_name": "filter_truncate",
                "top_n": 50,
                "$metadata": {
                    "common_input": [],
                    "common_output": [],
                    "item_input": [],
                    "item_output": [],
                },
            },
        },
        "pipeline_map": {},
    },
    "pipeline_group": {
        "main": {"pipeline": ["recall_items", "sort_by_ctr", "truncate_top"]},
    },
}


@pytest.mark.benchmark
def test_bench_small_pipeline(benchmark):
    """Benchmark: recall -> sort -> truncate with 100 items."""
    engine = _make_engine_from_config(_SMALL_PIPELINE_CONFIG)
    common: dict[str, Any] = {}
    items: list[dict[str, Any]] = []  # recall provides items

    benchmark(engine.execute, common, items)


# ---------------------------------------------------------------------------
# Benchmark: Large Pipeline (recall -> sort -> truncate, 1000 items)
# ---------------------------------------------------------------------------


_LARGE_PIPELINE_CONFIG = {
    "_PINEAPPLE_VERSION": "0.6.6",
    "pipeline_config": {
        "operators": {
            "recall_items": {
                "type_name": "recall_static",
                "recall": True,
                "items": _generate_items(1000, {"item_id": "", "item_ctr": 0.0, "item_dur": 0.0}),
                "$metadata": {
                    "common_input": [],
                    "common_output": [],
                    "item_input": [],
                    "item_output": ["item_id", "item_ctr", "item_dur"],
                },
            },
            "sort_by_ctr": {
                "type_name": "reorder_sort",
                "order": "desc",
                "$metadata": {
                    "common_input": [],
                    "common_output": [],
                    "item_input": ["item_ctr"],
                    "item_output": [],
                },
            },
            "truncate_top": {
                "type_name": "filter_truncate",
                "top_n": 500,
                "$metadata": {
                    "common_input": [],
                    "common_output": [],
                    "item_input": [],
                    "item_output": [],
                },
            },
        },
        "pipeline_map": {},
    },
    "pipeline_group": {
        "main": {"pipeline": ["recall_items", "sort_by_ctr", "truncate_top"]},
    },
}


@pytest.mark.benchmark
def test_bench_large_pipeline(benchmark):
    """Benchmark: recall -> sort -> truncate with 1000 items."""
    engine = _make_engine_from_config(_LARGE_PIPELINE_CONFIG)
    common: dict[str, Any] = {}
    items: list[dict[str, Any]] = []

    benchmark(engine.execute, common, items)


# ---------------------------------------------------------------------------
# Benchmark: Data Parallel (transform_copy with data_parallel=4, 200 items)
# ---------------------------------------------------------------------------


_DATA_PARALLEL_CONFIG = {
    "_PINEAPPLE_VERSION": "0.6.6",
    "pipeline_config": {
        "operators": {
            "copy_tag": {
                "type_name": "transform_copy",
                "direction": "common_to_item",
                "data_parallel": 4,
                "$metadata": {
                    "common_input": ["tag"],
                    "common_output": [],
                    "item_input": [],
                    "item_output": ["tag"],
                },
            },
        },
        "pipeline_map": {},
    },
    "pipeline_group": {
        "main": {"pipeline": ["copy_tag"]},
    },
}


@pytest.mark.benchmark
def test_bench_data_parallel(benchmark):
    """Benchmark: transform_copy with data_parallel=4 and 200 items."""
    engine = _make_engine_from_config(_DATA_PARALLEL_CONFIG)
    common = {"tag": "benchmark_tag"}
    items = [{"item_id": f"item_{i}"} for i in range(200)]

    benchmark(engine.execute, common, items)


# ---------------------------------------------------------------------------
# Benchmark: Lua Pipeline (transform_by_lua on 100 items)
# ---------------------------------------------------------------------------


_LUA_PIPELINE_CONFIG = {
    "_PINEAPPLE_VERSION": "0.6.6",
    "pipeline_config": {
        "operators": {
            "recall_items": {
                "type_name": "recall_static",
                "recall": True,
                "items": _generate_items(100, {"item_id": "", "item_score": 0.0}),
                "$metadata": {
                    "common_input": [],
                    "common_output": [],
                    "item_input": [],
                    "item_output": ["item_id", "item_score"],
                },
            },
            "lua_transform": {
                "type_name": "transform_by_lua",
                "lua_script": "function compute()\n  return item_score * 2 + 1\nend",
                "function_for_item": "compute",
                "function_for_common": "",
                "$metadata": {
                    "common_input": [],
                    "common_output": [],
                    "item_input": ["item_score"],
                    "item_output": ["item_result"],
                },
            },
        },
        "pipeline_map": {},
    },
    "pipeline_group": {
        "main": {"pipeline": ["recall_items", "lua_transform"]},
    },
}


@pytest.mark.benchmark
def test_bench_lua_pipeline(benchmark):
    """Benchmark: Lua transform on 100 items."""
    engine = _make_engine_from_config(_LUA_PIPELINE_CONFIG)
    common: dict[str, Any] = {}
    items: list[dict[str, Any]] = []

    benchmark(engine.execute, common, items)
