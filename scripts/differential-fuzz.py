"""Differential fuzzer: generates random configs+requests, runs all engines, reports divergences.

Supported engines: Go, Java, Python (extensible via --engines flag).
Randomization dimensions:
  - Pipeline topology (1-8 ops, 10 operator types, random combinations)
  - Operator parameters (type-specific random values)
  - Data shape (item count 1-50, field count 2-6)
  - Data values (edge floats, unicode, large strings, null, booleans)
  - data_parallel (1-4 for ConcurrentSafe operators)
  - storage_mode (row / column)
  - SubFlow / nested pipeline_map
  - skip / for_branch_control conditional execution
  - Edge numerics (near-zero, large, negative zero boundary)

Usage:
    python3 scripts/differential-fuzz.py [--rounds N] [--seed S] [--engines go,python,java]
"""
from __future__ import annotations

import argparse
import json
import os
import random
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Any


REPO_ROOT = Path(__file__).resolve().parent.parent
PYTHON_DIR = REPO_ROOT / "pine-python"
JAVA_DIR = REPO_ROOT / "pine-java"

# ---------------------------------------------------------------------------
# Operator registry (safe for fuzzing — no external deps, deterministic)
# ---------------------------------------------------------------------------

CONCURRENT_SAFE_OPS = {"transform_copy", "transform_dispatch", "transform_size", "transform_by_lua"}

FIELD_POOL = [
    "user_id", "user_age", "score", "price", "tag", "label", "count",
    "weight", "ratio", "flag", "name", "value", "amount", "level",
]

ITEM_FIELD_POOL = [
    "item_id", "item_score", "item_price", "item_weight", "item_tag",
    "item_count", "item_ratio", "item_level", "item_name", "item_value",
]

# Edge-case scalars for stress testing
EDGE_SCALARS: list[Any] = [
    0, -0.0, 1, -1,
    0.1, 0.2, 0.3,  # classic float imprecision
    1e-10, 1e10, 1e100, -1e100,
    1e-308, 1.7976931348623157e+308,  # near float64 limits
    42, -42, 2147483647, -2147483648,  # int32 bounds
    0.1 + 0.2,  # 0.30000000000000004
    True, False, None,
    "", "a", "hello world",
    "x" * 200,  # long string
    # Unicode edge cases
    "", "\n\t\r", "日本語テスト", "emoji: 🎉🔥",
    "zero_width", "rtl: ‏text",
    "<script>alert(1)</script>",  # XSS-like
    'key"with"quotes', "back\\slash",
    "café", "naïve", "über",
]

LUA_ITEM_FUNCTIONS = [
    ("function compute()\n  return item_score * 2\nend", ["item_score"], ["item_result"]),
    ("function compute()\n  return item_score + 1\nend", ["item_score"], ["item_result"]),
    ("function compute()\n  if item_score > 50 then return 1 else return 0 end\nend", ["item_score"], ["item_result"]),
    ("function compute()\n  return item_price * item_weight\nend", ["item_price", "item_weight"], ["item_result"]),
    ("function compute()\n  return item_score * 0.1 + 0.2\nend", ["item_score"], ["item_result"]),
    ("function compute()\n  if item_score == 0 then return -1 else return item_score end\nend", ["item_score"], ["item_result"]),
    ("function compute()\n  return item_count + item_level\nend", ["item_count", "item_level"], ["item_result"]),
    # Float precision edge cases
    ("function compute()\n  return item_score * 0.1 * 0.1\nend", ["item_score"], ["item_result"]),
    ("function compute()\n  return (item_score + 0.1) + 0.2\nend", ["item_score"], ["item_result"]),
    ("function compute()\n  return item_score / 3.0\nend", ["item_score"], ["item_result"]),
    ("function compute()\n  return item_score * 1e-10 + 1e-10\nend", ["item_score"], ["item_result"]),
    ("function compute()\n  return item_score * item_score * item_score\nend", ["item_score"], ["item_result"]),
    ("function compute()\n  return (item_score - 0.3) * 1000000\nend", ["item_score"], ["item_result"]),
]

LUA_COMMON_FUNCTIONS = [
    ("function process()\n  return value * 2\nend", ["value"], ["doubled"]),
    ("function process()\n  return score + 10\nend", ["score"], ["boosted"]),
    ("function process()\n  if user_age > 18 then return 1 else return 0 end\nend", ["user_age"], ["is_adult"]),
    ("function process()\n  return value * 0.1 + 0.2\nend", ["value"], ["computed"]),
    ("function process()\n  if value == 0 then return -999 else return value * value end\nend", ["value"], ["computed"]),
    # Float precision edge cases
    ("function process()\n  return value / 7.0\nend", ["value"], ["computed"]),
    ("function process()\n  return value * 1e-15 + 1e-15\nend", ["value"], ["computed"]),
    ("function process()\n  return (value + 0.1 + 0.2) - 0.3\nend", ["value"], ["computed"]),
]


# ---------------------------------------------------------------------------
# Value generators
# ---------------------------------------------------------------------------


def random_scalar(rng: random.Random, edge_weight: float = 0.2) -> Any:
    if rng.random() < edge_weight:
        return rng.choice(EDGE_SCALARS)
    choice = rng.randint(0, 4)
    if choice == 0:
        return rng.randint(-1000, 1000)
    elif choice == 1:
        return round(rng.uniform(-1000, 1000), 6)
    elif choice == 2:
        return rng.choice(["hello", "world", "test", "foo", "bar", "", "café", "🎉"])
    elif choice == 3:
        return rng.choice([True, False])
    else:
        return None


def random_numeric(rng: random.Random, edge_weight: float = 0.3) -> float | int:
    if rng.random() < edge_weight:
        return rng.choice([0, -0.0, 0.1, 0.2, 0.3, 1e-10, 1e10, 42, -42, 0.1 + 0.2,
                           1e100, -1e100, 2147483647, -2147483648, 1e-308])
    if rng.random() < 0.5:
        return rng.randint(-100, 100)
    return round(rng.uniform(-100, 100), 4)


def random_items(rng: random.Random, fields: list[str], count: int, edge_weight: float = 0.15) -> list[dict[str, Any]]:
    items = []
    for i in range(count):
        item: dict[str, Any] = {}
        for f in fields:
            if "id" in f:
                if rng.random() < 0.1:
                    item[f] = rng.choice(["id_🎉", "id_café", f"id_{i}_special"])
                else:
                    item[f] = f"id_{i}"
            elif "score" in f or "price" in f or "weight" in f or "ratio" in f:
                item[f] = random_numeric(rng, edge_weight)
            elif "count" in f or "level" in f:
                item[f] = rng.randint(0, 100)
            elif "tag" in f or "name" in f:
                if rng.random() < edge_weight:
                    item[f] = rng.choice(["", "café", "🎉", "a\"b", "x\ny", None])
                else:
                    item[f] = rng.choice(["a", "b", "c", "d"])
            else:
                item[f] = random_scalar(rng, edge_weight)
        items.append(item)
    return items


# ---------------------------------------------------------------------------
# Operator generation
# ---------------------------------------------------------------------------


def gen_operator(rng: random.Random, name: str,
                 prev_item_outputs: list[str], prev_common_outputs: list[str],
                 allow_data_parallel: bool = True) -> tuple[dict, list[str], list[str]]:
    """Generate a random operator config. Returns (config, new_item_outputs, new_common_outputs)."""
    op_types = [
        "filter_truncate", "filter_condition", "recall_static", "reorder_sort",
        "transform_by_lua", "transform_copy", "transform_dispatch",
        "transform_size", "transform_normalize", "observe_log",
    ]
    op_type = rng.choice(op_types)

    item_in: list[str] = []
    item_out: list[str] = []
    common_in: list[str] = []
    common_out: list[str] = []

    config: dict[str, Any] = {"type_name": op_type}

    # Randomly add data_parallel for ConcurrentSafe ops
    if allow_data_parallel and op_type in CONCURRENT_SAFE_OPS and rng.random() < 0.3:
        dp = rng.choice([2, 3, 4])
        config["data_parallel"] = dp

    if op_type == "filter_truncate":
        config["top_n"] = rng.randint(1, 30)
        item_in = prev_item_outputs[:2] if prev_item_outputs else []

    elif op_type == "filter_condition":
        if prev_item_outputs:
            item_in = [prev_item_outputs[0]]
            config["value"] = rng.choice([None, 0, "", False])
        else:
            common_in = prev_common_outputs[:1] if prev_common_outputs else ["value"]
            config["value"] = rng.choice([None, 0, "", False])

    elif op_type == "recall_static":
        n_items = rng.randint(2, 30)
        out_fields = rng.sample(ITEM_FIELD_POOL, min(rng.randint(2, 5), len(ITEM_FIELD_POOL)))
        items = random_items(rng, out_fields, n_items)
        # Assign globally unique _fuzz_distinctive_score (offset by large random to avoid collision)
        base = rng.uniform(1e6, 1e9)
        for idx, item in enumerate(items):
            item["_fuzz_distinctive_score"] = base + idx * 1000.0 + rng.uniform(0.1, 999.9)
        config["items"] = items
        config["recall"] = True
        item_out = out_fields + ["_fuzz_distinctive_score"]
        # data_parallel not valid for recall
        config.pop("data_parallel", None)

    elif op_type == "reorder_sort":
        config["order"] = rng.choice(["asc", "desc"])
        if prev_item_outputs:
            sort_field = rng.choice(prev_item_outputs)
            item_in = [sort_field]
        else:
            item_in = ["item_score"]
        # data_parallel not valid for reorder
        config.pop("data_parallel", None)

    elif op_type == "transform_by_lua":
        use_item = rng.choice([True, False]) if prev_item_outputs else False
        if use_item:
            lua_fn, lua_in, lua_out = rng.choice(LUA_ITEM_FUNCTIONS)
            available = [f for f in lua_in if f in prev_item_outputs]
            if not available:
                lua_fn = "function compute()\n  return 42\nend"
                lua_in = []
                lua_out = ["item_result"]
            else:
                lua_in = available
            config["lua_script"] = lua_fn
            config["function_for_item"] = "compute"
            config["function_for_common"] = ""
            item_in = lua_in
            item_out = lua_out
        else:
            lua_fn, lua_in, lua_out = rng.choice(LUA_COMMON_FUNCTIONS)
            available = [f for f in lua_in if f in prev_common_outputs]
            if not available:
                lua_fn = "function process()\n  return 42\nend"
                lua_in = []
                lua_out = ["computed"]
            else:
                lua_in = available
            config["lua_script"] = lua_fn
            config["function_for_common"] = "process"
            config["function_for_item"] = ""
            common_in = lua_in
            common_out = lua_out
            # data_parallel requires empty common_output
            if common_out and "data_parallel" in config:
                del config["data_parallel"]

    elif op_type == "transform_copy":
        direction = rng.choice(["common_to_item", "item_to_common"])
        config["direction"] = direction
        if direction == "common_to_item":
            field = rng.choice(prev_common_outputs) if prev_common_outputs else "tag"
            common_in = [field]
            item_out = [field]
        else:
            field = rng.choice(prev_item_outputs) if prev_item_outputs else "item_score"
            item_in = [field]
            common_out = [field]
            # data_parallel requires empty common_output
            if "data_parallel" in config:
                del config["data_parallel"]

    elif op_type == "transform_dispatch":
        if prev_common_outputs:
            field = rng.choice(prev_common_outputs)
            common_in = [field]
            item_out = ["item_" + field if not field.startswith("item_") else field]
        else:
            common_in = ["tag"]
            item_out = ["item_tag"]

    elif op_type == "transform_size":
        common_out = ["size"]
        # data_parallel requires empty common_output
        if "data_parallel" in config:
            del config["data_parallel"]

    elif op_type == "transform_normalize":
        config["method"] = "min_max"
        if prev_item_outputs:
            numeric_fields = [f for f in prev_item_outputs
                              if "score" in f or "price" in f or "weight" in f or "count" in f or "level" in f]
            norm_field = rng.choice(numeric_fields) if numeric_fields else prev_item_outputs[0]
            item_in = [norm_field]
        else:
            item_in = ["item_score"]
        # data_parallel not valid for non-transform types handled above
        config.pop("data_parallel", None)

    elif op_type == "observe_log":
        common_in = prev_common_outputs[:2] if prev_common_outputs else []
        item_in = prev_item_outputs[:2] if prev_item_outputs else []
        config.pop("data_parallel", None)

    config["$metadata"] = {
        "common_input": common_in,
        "common_output": common_out,
        "item_input": item_in,
        "item_output": item_out,
    }

    new_item_out = list(set(prev_item_outputs + item_out))
    new_common_out = list(set(prev_common_outputs + common_out))
    return config, new_item_out, new_common_out


# ---------------------------------------------------------------------------
# Pipeline generation (with subflow / skip / storage_mode)
# ---------------------------------------------------------------------------


def gen_pipeline(rng: random.Random) -> tuple[dict, dict, list[dict], bool]:
    """Generate a random pipeline config + matching request.
    Returns (config, common, items, strict_order).
    strict_order=True when the pipeline ends with a sort on a unique key.
    """
    n_ops = rng.randint(1, 8)

    # Start with recall_static — always include _fuzz_distinctive_score with unique values
    n_recall_items = rng.randint(3, 50)
    recall_fields = rng.sample(ITEM_FIELD_POOL, min(rng.randint(2, 5), len(ITEM_FIELD_POOL)))
    recall_items = random_items(rng, recall_fields, n_recall_items)
    for idx, item in enumerate(recall_items):
        item["_fuzz_distinctive_score"] = idx * 1000.0 + rng.uniform(0.1, 999.9)
    recall_fields_with_score = recall_fields + ["_fuzz_distinctive_score"]

    operators: dict[str, Any] = {}
    pipeline: list[str] = []

    operators["recall"] = {
        "type_name": "recall_static",
        "recall": True,
        "items": recall_items,
        "$metadata": {
            "common_input": [],
            "common_output": [],
            "item_input": [],
            "item_output": recall_fields_with_score,
        },
    }
    pipeline.append("recall")

    item_outputs = list(recall_fields_with_score)

    # Generate common inputs with edge values
    common_fields = rng.sample(FIELD_POOL, rng.randint(1, 5))
    common: dict[str, Any] = {}
    for f in common_fields:
        if "age" in f or "count" in f or "level" in f:
            common[f] = rng.choice([0, 1, 17, 18, 60, 61, 80, -1, 2147483647])
        elif "score" in f or "price" in f or "weight" in f or "ratio" in f or "amount" in f or "value" in f:
            common[f] = random_numeric(rng, edge_weight=0.4)
        elif "flag" in f:
            common[f] = rng.choice([True, False])
        else:
            if rng.random() < 0.2:
                common[f] = rng.choice(["", "café", "🎉", "a\"b", None, "x" * 100])
            else:
                common[f] = rng.choice(["hello", "test", "abc"])
    common_outputs = list(common_fields)

    # Optionally add skip/branch_control
    use_skip = rng.random() < 0.25
    skip_field = ""
    if use_skip and common_outputs:
        skip_field = "_skip_branch"
        # Insert a Lua op that sets the skip flag
        skip_lua = (
            "function check()\n"
            f"  if {common_outputs[0]} then return true else return false end\n"
            "end"
        )
        operators["set_skip"] = {
            "type_name": "transform_by_lua",
            "lua_script": skip_lua,
            "function_for_common": "check",
            "function_for_item": "",
            "$metadata": {
                "common_input": [common_outputs[0]],
                "common_output": [skip_field],
                "item_input": [],
                "item_output": [],
            },
        }
        pipeline.append("set_skip")
        common_outputs.append(skip_field)

    for i in range(n_ops):
        op_name = f"op_{i}"
        op_config, item_outputs, common_outputs = gen_operator(
            rng, op_name, item_outputs, common_outputs
        )

        # Randomly attach skip to some ops
        if use_skip and skip_field and rng.random() < 0.3:
            op_config["skip"] = skip_field
            op_config["for_branch_control"] = True
            if skip_field not in op_config["$metadata"]["common_input"]:
                op_config["$metadata"]["common_input"].append(skip_field)

        operators[op_name] = op_config
        pipeline.append(op_name)

    # ~40% chance: append a trailing sort on _fuzz_distinctive_score (enables strict order check)
    strict_order = False
    if rng.random() < 0.4 and "_fuzz_distinctive_score" in item_outputs:
        sort_name = "_final_sort"
        operators[sort_name] = {
            "type_name": "reorder_sort",
            "order": rng.choice(["asc", "desc"]),
            "$metadata": {
                "common_input": [],
                "common_output": [],
                "item_input": ["_fuzz_distinctive_score"],
                "item_output": [],
            },
        }
        pipeline.append(sort_name)
        strict_order = True

    # Decide pipeline structure: flat, subflow, nested, or deep-nested
    pipeline_map: dict[str, Any] = {}
    group_pipeline: list[str]
    structure = rng.choice(["flat", "flat", "subflow", "nested", "deep_nested"])

    if structure == "subflow" and len(pipeline) >= 4:
        split = rng.randint(2, len(pipeline) - 1)
        sub_name = "stage_a"
        pipeline_map[sub_name] = {"pipeline": pipeline[:split]}
        sub_name_b = "stage_b"
        pipeline_map[sub_name_b] = {"pipeline": pipeline[split:]}
        group_pipeline = [sub_name, sub_name_b]
    elif structure == "nested" and len(pipeline) >= 5:
        split1 = rng.randint(2, len(pipeline) // 2)
        split2 = rng.randint(split1 + 1, len(pipeline) - 1)
        pipeline_map["inner"] = {"pipeline": pipeline[:split1]}
        pipeline_map["outer"] = {"pipeline": ["inner"] + pipeline[split1:split2]}
        remaining = pipeline[split2:]
        if remaining:
            pipeline_map["tail"] = {"pipeline": remaining}
            group_pipeline = ["outer", "tail"]
        else:
            group_pipeline = ["outer"]
    elif structure == "deep_nested" and len(pipeline) >= 7:
        # 3-4 layers of nesting
        n = len(pipeline)
        cuts = sorted(rng.sample(range(2, n), min(3, n - 2)))
        segments = []
        prev = 0
        for c in cuts:
            segments.append(pipeline[prev:c])
            prev = c
        segments.append(pipeline[prev:])

        # Build from innermost out
        layer_names = [f"layer_{i}" for i in range(len(segments))]
        pipeline_map[layer_names[0]] = {"pipeline": segments[0]}
        for i in range(1, len(segments) - 1):
            pipeline_map[layer_names[i]] = {"pipeline": [layer_names[i - 1]] + segments[i]}
        # Outermost layer includes the last segment
        if len(segments) > 1:
            last_layer = layer_names[len(segments) - 2]
            pipeline_map["top"] = {"pipeline": [last_layer] + segments[-1]}
            group_pipeline = ["top"]
        else:
            group_pipeline = [layer_names[0]]
    else:
        group_pipeline = pipeline

    # Randomly inject storage_mode
    storage_mode = rng.choice(["row", "row", "row", "column"])  # 25% column

    config: dict[str, Any] = {
        "_PINEAPPLE_VERSION": "0.6.6",
        "pipeline_config": {
            "operators": operators,
            "pipeline_map": pipeline_map,
        },
        "pipeline_group": {"main": {"pipeline": group_pipeline}},
    }

    if storage_mode == "column":
        config["storage_mode"] = "column"

    items: list[dict] = []  # recall_static provides items
    return config, common, items, strict_order


# ---------------------------------------------------------------------------
# Error-path pipeline generation
# ---------------------------------------------------------------------------


def gen_error_pipeline(rng: random.Random) -> tuple[dict, dict, list[dict], bool, str]:
    """Generate a pipeline designed to trigger error/edge-case paths.
    Returns (config, common, items, strict_order=False, error_scenario).
    """
    scenario = rng.choice([
        "nil_sort_field",
        "missing_sort_field",
        "string_sort_field",
        "lua_missing_var",
        "zero_items_filter",
        "zero_items_sort",
    ])

    operators: dict[str, Any] = {}
    pipeline: list[str] = []

    if scenario == "nil_sort_field":
        # Some items have sort field as nil
        n = rng.randint(3, 10)
        items_data = []
        for i in range(n):
            item: dict[str, Any] = {"item_id": f"id_{i}"}
            if rng.random() < 0.4:
                item["sort_val"] = None
            else:
                item["sort_val"] = rng.uniform(-100, 100)
            items_data.append(item)

        operators["recall"] = {
            "type_name": "recall_static",
            "recall": True,
            "items": items_data,
            "$metadata": {"common_input": [], "common_output": [],
                          "item_input": [], "item_output": ["item_id", "sort_val"]},
        }
        operators["sort_nil"] = {
            "type_name": "reorder_sort",
            "order": "asc",
            "$metadata": {"common_input": [], "common_output": [],
                          "item_input": ["sort_val"], "item_output": []},
        }
        pipeline = ["recall", "sort_nil"]

    elif scenario == "missing_sort_field":
        # Items don't have the field referenced by sort
        n = rng.randint(3, 10)
        items_data = [{"item_id": f"id_{i}", "item_score": rng.uniform(0, 100)} for i in range(n)]

        operators["recall"] = {
            "type_name": "recall_static",
            "recall": True,
            "items": items_data,
            "$metadata": {"common_input": [], "common_output": [],
                          "item_input": [], "item_output": ["item_id", "item_score"]},
        }
        operators["sort_missing"] = {
            "type_name": "reorder_sort",
            "order": "desc",
            "$metadata": {"common_input": [], "common_output": [],
                          "item_input": ["nonexistent_field"], "item_output": []},
        }
        pipeline = ["recall", "sort_missing"]

    elif scenario == "string_sort_field":
        # Sort on a string field
        n = rng.randint(3, 10)
        items_data = [{"item_id": f"id_{i}", "item_tag": rng.choice(["a", "b", "c", "z", ""])}
                      for i in range(n)]

        operators["recall"] = {
            "type_name": "recall_static",
            "recall": True,
            "items": items_data,
            "$metadata": {"common_input": [], "common_output": [],
                          "item_input": [], "item_output": ["item_id", "item_tag"]},
        }
        operators["sort_string"] = {
            "type_name": "reorder_sort",
            "order": "asc",
            "$metadata": {"common_input": [], "common_output": [],
                          "item_input": ["item_tag"], "item_output": []},
        }
        pipeline = ["recall", "sort_string"]

    elif scenario == "lua_missing_var":
        # Lua references a variable that items don't have
        n = rng.randint(3, 10)
        items_data = [{"item_id": f"id_{i}", "item_score": rng.uniform(0, 100)} for i in range(n)]

        operators["recall"] = {
            "type_name": "recall_static",
            "recall": True,
            "items": items_data,
            "$metadata": {"common_input": [], "common_output": [],
                          "item_input": [], "item_output": ["item_id", "item_score"]},
        }
        operators["lua_bad"] = {
            "type_name": "transform_by_lua",
            "lua_script": "function compute()\n  return missing_field * 2\nend",
            "function_for_item": "compute",
            "function_for_common": "",
            "$metadata": {"common_input": [], "common_output": [],
                          "item_input": ["missing_field"], "item_output": ["item_result"]},
        }
        pipeline = ["recall", "lua_bad"]

    elif scenario == "zero_items_filter":
        # Start with 0 items, apply filter
        operators["recall"] = {
            "type_name": "recall_static",
            "recall": True,
            "items": [],
            "$metadata": {"common_input": [], "common_output": [],
                          "item_input": [], "item_output": ["item_id"]},
        }
        operators["filter_empty"] = {
            "type_name": "filter_truncate",
            "top_n": 5,
            "$metadata": {"common_input": [], "common_output": [],
                          "item_input": [], "item_output": []},
        }
        pipeline = ["recall", "filter_empty"]

    elif scenario == "zero_items_sort":
        # Start with 0 items, apply sort
        operators["recall"] = {
            "type_name": "recall_static",
            "recall": True,
            "items": [],
            "$metadata": {"common_input": [], "common_output": [],
                          "item_input": [], "item_output": ["item_id", "item_score"]},
        }
        operators["sort_empty"] = {
            "type_name": "reorder_sort",
            "order": "asc",
            "$metadata": {"common_input": [], "common_output": [],
                          "item_input": ["item_score"], "item_output": []},
        }
        pipeline = ["recall", "sort_empty"]

    config: dict[str, Any] = {
        "_PINEAPPLE_VERSION": "0.6.6",
        "pipeline_config": {
            "operators": operators,
            "pipeline_map": {},
        },
        "pipeline_group": {"main": {"pipeline": pipeline}},
    }

    common: dict[str, Any] = {"value": rng.randint(1, 100)}
    return config, common, [], False, scenario


# ---------------------------------------------------------------------------
# Engine runners
# ---------------------------------------------------------------------------


def _resolve_java_cp() -> str:
    """Resolve Java classpath."""
    target = JAVA_DIR / "target" / "classes"
    if not target.exists():
        raise FileNotFoundError(f"Java not built: {target}")
    result = subprocess.run(
        ["mvn", "dependency:build-classpath", "-B", "-q", "-Dmdep.outputFile=/dev/stdout"],
        capture_output=True, text=True, cwd=str(JAVA_DIR), timeout=60,
    )
    deps = result.stdout.strip().split("\n")[-1]
    return f"{target}:{deps}"


class Engine:
    name: str

    def run(self, config_file: str, request_file: str) -> tuple[int, str, str]:
        raise NotImplementedError


class GoEngine(Engine):
    name = "go"

    def __init__(self, binary: str):
        self.binary = binary

    def run(self, config_file: str, request_file: str) -> tuple[int, str, str]:
        result = subprocess.run(
            [self.binary, "-config", config_file, "-request", request_file],
            capture_output=True, text=True, timeout=10,
        )
        return result.returncode, result.stdout, result.stderr


class PythonEngine(Engine):
    name = "python"

    def run(self, config_file: str, request_file: str) -> tuple[int, str, str]:
        result = subprocess.run(
            [sys.executable, "-m", "pine.cli.run", "-config", config_file, "-request", request_file],
            capture_output=True, text=True, timeout=10,
            cwd=str(PYTHON_DIR),
        )
        return result.returncode, result.stdout, result.stderr


class JavaEngine(Engine):
    name = "java"

    def __init__(self, classpath: str):
        self.classpath = classpath

    def run(self, config_file: str, request_file: str) -> tuple[int, str, str]:
        result = subprocess.run(
            ["java", "-cp", self.classpath, "page.liam.pine.RunCli",
             "-config", config_file, "-request", request_file],
            capture_output=True, text=True, timeout=15,
        )
        return result.returncode, result.stdout, result.stderr


# ---------------------------------------------------------------------------
# Comparison
# ---------------------------------------------------------------------------


def _normalize_value(v: Any) -> Any:
    if isinstance(v, float):
        if v == 0.0:
            return 0.0
        if abs(v) < 1e-15:
            return 0.0
        if abs(v) > 1e-6:
            return round(v, 10)
        return v
    if isinstance(v, dict):
        return {k: _normalize_value(val) for k, val in v.items()}
    if isinstance(v, list):
        return [_normalize_value(item) for item in v]
    return v


def _sort_items(items: list) -> list:
    """Sort items by stable key for order-independent comparison."""
    def sort_key(item):
        if isinstance(item, dict):
            return json.dumps(item, sort_keys=True, ensure_ascii=False)
        return str(item)
    return sorted(items, key=sort_key)


def normalize_json(data: str, sort_items: bool = False) -> str:
    """Parse and re-serialize JSON for comparison."""
    try:
        obj = json.loads(data)
    except json.JSONDecodeError:
        return data.strip()

    obj = _normalize_value(obj)
    if sort_items and isinstance(obj, dict) and "items" in obj:
        obj["items"] = _sort_items(obj["items"])

    return json.dumps(obj, sort_keys=True, ensure_ascii=False)


def save_divergence(save_dir: Path, round_num: int, config: dict, request: dict,
                    outputs: dict[str, tuple[int, str]], pair: tuple[str, str],
                    kind: str = "divergence"):
    d = save_dir / f"{kind}_{round_num:06d}"
    d.mkdir(parents=True, exist_ok=True)
    (d / "config.json").write_text(json.dumps(config, indent=2, ensure_ascii=False))
    (d / "request.json").write_text(json.dumps(request, indent=2, ensure_ascii=False))
    for name, (rc, out) in outputs.items():
        (d / f"{name}_output.json").write_text(out)
    (d / "info.txt").write_text(
        f"divergent_pair: {pair[0]} vs {pair[1]}\n"
        + "\n".join(f"{name}_rc={rc}" for name, (rc, _) in outputs.items())
    )
    return d


def check_stability(engines: list[Engine], config_file: str, request_file: str,
                    runs: int) -> tuple[bool, str]:
    """Run each engine multiple times, return (stable, detail) — detects non-determinism in values/count."""
    for engine in engines:
        outputs = []
        for _ in range(runs):
            rc, out, _ = engine.run(config_file, request_file)
            outputs.append(normalize_json(out, sort_items=True))
        if len(set(outputs)) > 1:
            return False, f"{engine.name}: {len(set(outputs))} distinct outputs in {runs} runs"
    return True, ""


def shrink_pipeline(config: dict, request: dict, engines: list[Engine],
                    tmpdir: str) -> dict:
    """Try removing operators one by one to find minimal diverging pipeline."""
    pc = config.get("pipeline_config", {})
    operators = pc.get("operators", {})
    pm = pc.get("pipeline_map", {})

    if pm:
        return config

    group = config.get("pipeline_group", {}).get("main", {}).get("pipeline", [])
    if len(group) <= 2:
        return config

    config_file = os.path.join(tmpdir, "shrink_config.json")
    request_file = os.path.join(tmpdir, "shrink_request.json")
    with open(request_file, "w") as f:
        json.dump(request, f, ensure_ascii=False)

    best = config
    improved = True

    while improved:
        improved = False
        group = best.get("pipeline_group", {}).get("main", {}).get("pipeline", [])

        for idx in range(len(group) - 1, 0, -1):
            candidate_pipeline = group[:idx] + group[idx + 1:]
            if len(candidate_pipeline) < 1:
                continue

            op_name = group[idx]
            candidate_ops = {k: v for k, v in best["pipeline_config"]["operators"].items()
                            if k in candidate_pipeline}

            candidate = {
                **best,
                "pipeline_config": {**best["pipeline_config"], "operators": candidate_ops},
                "pipeline_group": {"main": {"pipeline": candidate_pipeline}},
            }

            with open(config_file, "w") as f:
                json.dump(candidate, f, ensure_ascii=False)

            results: dict[str, tuple[int, str]] = {}
            try:
                for engine in engines:
                    rc, out, _ = engine.run(config_file, request_file)
                    results[engine.name] = (rc, out)
            except (subprocess.TimeoutExpired, Exception):
                continue

            ref_name = engines[0].name
            ref_rc, ref_out = results[ref_name]
            still_diverges = False
            for engine in engines[1:]:
                e_rc, e_out = results[engine.name]
                if ref_rc != 0 and e_rc != 0:
                    continue
                if ref_rc != e_rc:
                    still_diverges = True
                    break
                if normalize_json(ref_out) != normalize_json(e_out):
                    still_diverges = True
                    break

            if still_diverges:
                best = candidate
                improved = True
                break

    return best


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main():
    parser = argparse.ArgumentParser(description="Differential fuzzer: multi-engine parity")
    parser.add_argument("--rounds", type=int, default=1000)
    parser.add_argument("--seed", type=int, default=None)
    parser.add_argument("--engines", type=str, default="go,python,java",
                        help="Comma-separated engines to compare (go,python,java)")
    parser.add_argument("--go-bin", type=str, default="")
    parser.add_argument("--save-dir", type=str, default="")
    parser.add_argument("--verbose", "-v", action="store_true")
    parser.add_argument("--stability-runs", type=int, default=0,
                        help="Run each pipeline N extra times per engine to detect non-determinism (0=disabled)")
    parser.add_argument("--shrink", action="store_true",
                        help="Minimize diverging pipelines by removing operators")
    args = parser.parse_args()

    seed = args.seed if args.seed is not None else random.randint(0, 2**32 - 1)
    rng = random.Random(seed)

    engine_names = [e.strip() for e in args.engines.split(",")]
    print(f"Differential fuzz: {args.rounds} rounds, seed={seed}, engines={engine_names}")

    # Build engines
    engines: list[Engine] = []
    for name in engine_names:
        if name == "go":
            go_bin = args.go_bin or str(REPO_ROOT / "pine-go" / "pineapple-run")
            if not Path(go_bin).exists():
                print("  Building Go binary...")
                subprocess.run(
                    ["go", "build", "-o", go_bin, "./cmd/pineapple-run/"],
                    cwd=str(REPO_ROOT / "pine-go"), check=True,
                )
            engines.append(GoEngine(go_bin))
        elif name == "python":
            engines.append(PythonEngine())
        elif name == "java":
            try:
                cp = _resolve_java_cp()
                engines.append(JavaEngine(cp))
            except (FileNotFoundError, subprocess.TimeoutExpired) as e:
                print(f"  WARNING: Java not available ({e}), skipping")
        else:
            print(f"  WARNING: unknown engine '{name}', skipping")

    if len(engines) < 2:
        print("ERROR: need at least 2 engines to compare")
        sys.exit(1)

    print(f"  Active engines: {[e.name for e in engines]}")

    save_dir = Path(args.save_dir) if args.save_dir else Path(tempfile.mkdtemp(prefix="diff-fuzz-"))
    print(f"  Divergences saved to: {save_dir}")

    passed = 0
    failed = 0
    errors = 0
    unstable = 0
    stats = {"flat": 0, "subflow": 0, "nested": 0, "deep_nested": 0, "column": 0, "skip": 0,
             "data_parallel": 0, "strict_order": 0, "error_path": 0}
    start_time = time.time()

    def _progress(i: int):
        elapsed = time.time() - start_time
        rate = (i + 1) / elapsed if elapsed > 0 else 0
        remaining = (args.rounds - i - 1) / rate if rate > 0 else 0
        pct = (i + 1) * 100 // args.rounds
        bar_len = 30
        filled = bar_len * (i + 1) // args.rounds
        bar = "█" * filled + "░" * (bar_len - filled)
        status = f"\r  [{bar}] {pct:3d}% ({i+1}/{args.rounds}) " \
                 f"pass={passed} fail={failed} unstable={unstable} err={errors} " \
                 f"[{rate:.1f} rnd/s, ETA {int(remaining)}s]"
        sys.stderr.write(status)
        sys.stderr.flush()

    with tempfile.TemporaryDirectory() as tmpdir:
        config_file = os.path.join(tmpdir, "config.json")
        request_file = os.path.join(tmpdir, "request.json")

        for i in range(args.rounds):
            try:
                # ~20% error-path pipelines
                is_error_path = rng.random() < 0.2
                if is_error_path:
                    config, common, items, strict_order, scenario = gen_error_pipeline(rng)
                    stats["error_path"] += 1
                else:
                    config, common, items, strict_order = gen_pipeline(rng)
                request = {"common": common, "items": items}

                # Track stats
                if not is_error_path:
                    if config.get("storage_mode") == "column":
                        stats["column"] += 1
                    pm = config.get("pipeline_config", {}).get("pipeline_map", {})
                    if pm:
                        # Count nesting depth
                        depth = 0
                        for sub in pm.values():
                            for v in sub.get("pipeline", []):
                                if v in pm:
                                    depth += 1
                        if depth >= 2:
                            stats["deep_nested"] += 1
                        elif depth >= 1:
                            stats["nested"] += 1
                        else:
                            stats["subflow"] += 1
                    else:
                        stats["flat"] += 1
                    ops = config.get("pipeline_config", {}).get("operators", {})
                    if any(op.get("skip") for op in ops.values()):
                        stats["skip"] += 1
                    if any(op.get("data_parallel", 1) > 1 for op in ops.values()):
                        stats["data_parallel"] += 1
                    if strict_order:
                        stats["strict_order"] += 1

                with open(config_file, "w") as f:
                    json.dump(config, f, ensure_ascii=False)
                with open(request_file, "w") as f:
                    json.dump(request, f, ensure_ascii=False)

                # Run all engines
                results: dict[str, tuple[int, str, str]] = {}
                for engine in engines:
                    rc, out, err = engine.run(config_file, request_file)
                    results[engine.name] = (rc, out, err)

                # Stability check
                if args.stability_runs > 0:
                    stable, detail = check_stability(engines, config_file, request_file,
                                                    args.stability_runs)
                    if not stable:
                        unstable += 1
                        d = save_divergence(save_dir, i, config, request,
                                            {n: (rc, out) for n, (rc, out, _) in results.items()},
                                            ("", ""), kind="unstable")
                        if args.verbose:
                            sys.stderr.write("\n")
                            print(f"  [{i+1}] UNSTABLE: {detail} → {d}")
                        _progress(i)
                        continue

                # Compare pairwise (reference = first engine)
                ref_name = engines[0].name
                ref_rc, ref_out, ref_err = results[ref_name]

                all_match = True
                divergent_pair = ("", "")

                for engine in engines[1:]:
                    e_rc, e_out, e_err = results[engine.name]

                    # Both fail → ok
                    if ref_rc != 0 and e_rc != 0:
                        continue

                    # Exit code mismatch
                    if ref_rc != e_rc:
                        all_match = False
                        divergent_pair = (ref_name, engine.name)
                        break

                    # Both succeed → compare output
                    ref_norm = normalize_json(ref_out, sort_items=not strict_order)
                    e_norm = normalize_json(e_out, sort_items=not strict_order)
                    if ref_norm != e_norm:
                        all_match = False
                        divergent_pair = (ref_name, engine.name)
                        break

                if all_match:
                    passed += 1
                    if args.verbose:
                        sys.stderr.write("\n")
                        print(f"  [{i+1}] match")
                else:
                    failed += 1
                    outputs = {name: (rc, out) for name, (rc, out, _) in results.items()}

                    # Shrink if enabled
                    if args.shrink:
                        shrunk = shrink_pipeline(config, request, engines, tmpdir)
                        d = save_divergence(save_dir, i, shrunk, request, outputs, divergent_pair)
                    else:
                        d = save_divergence(save_dir, i, config, request, outputs, divergent_pair)

                    sys.stderr.write("\n")
                    print(f"  [{i+1}] DIVERGENCE ({divergent_pair[0]} vs {divergent_pair[1]}): → {d}")
                    if args.verbose:
                        for name, (rc, out, err) in results.items():
                            print(f"       {name}: rc={rc} out={out[:100]}")

            except subprocess.TimeoutExpired:
                errors += 1
                if args.verbose:
                    sys.stderr.write("\n")
                    print(f"  [{i+1}] timeout")
            except Exception as e:
                errors += 1
                if args.verbose:
                    sys.stderr.write("\n")
                    print(f"  [{i+1}] error: {e}")

            _progress(i)

        sys.stderr.write("\n")

    print(f"\n{'='*60}")
    print(f"Results: {args.rounds} rounds, seed={seed}")
    print(f"  Engines: {[e.name for e in engines]}")
    print(f"  PASS:  {passed}")
    print(f"  FAIL:  {failed}")
    if args.stability_runs > 0:
        print(f"  UNSTABLE: {unstable}")
    print(f"  ERROR: {errors}")
    print(f"  Coverage: flat={stats['flat']} subflow={stats['subflow']} nested={stats['nested']} deep_nested={stats['deep_nested']}")
    print(f"            column={stats['column']} skip={stats['skip']} data_parallel={stats['data_parallel']}")
    print(f"            strict_order={stats['strict_order']} error_path={stats['error_path']}")
    if failed > 0:
        print(f"  Divergences: {save_dir}")
    print(f"{'='*60}")

    sys.exit(1 if failed > 0 else 0)


if __name__ == "__main__":
    main()
