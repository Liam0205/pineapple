"""Differential fuzzer: generates random configs+requests, runs all engines, reports divergences.

Supported engines: Go, Java, Python, C++ (extensible via --engines flag).
Randomization dimensions:
  - Pipeline topology (1-8 ops, 15 operator types, random combinations)
  - Operator parameters (type-specific random values)
  - Data shape (item count 1-50, field count 2-6, sparse items with dropped fields)
  - Data values (edge floats, unicode, large strings, null, booleans, nested dicts/arrays)
  - data_parallel (1-4 for ConcurrentSafe operators)
  - storage_mode (row / column)
  - SubFlow / nested pipeline_map
  - skip / for_branch_control conditional execution
  - Edge numerics (near-zero, large, negative zero boundary)
  - Request-provided items (bypass recall, items in request directly)
  - Explicit sources (DAG edges to prior operators)
  - common_defaults / item_defaults on operators
  - debug=true on random operators
  - _return_trace=true in request common
  - Resource operators (transform_resource_lookup, recall_resource with static resource_config)
  - merge_dedup, reorder_shuffle_by_salt, filter_paginate operators
  - transform_copy all 4 directions (common_to_item, item_to_common, common_to_common, item_to_item)

Usage:
    python3 scripts/differential-fuzz.py [--rounds N] [--seed S] [--engines go,python,java,cpp]
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
    # Table input/output cases
    ("function compute()\n  return #item_tags\nend", ["item_tags"], ["item_result"]),
    ("function compute()\n  local s = 0\n  for i = 1, #item_vals do s = s + item_vals[i] end\n  return s\nend", ["item_vals"], ["item_result"]),
    ("function compute()\n  return {item_score * 2, item_score * 3}\nend", ["item_score"], ["item_result"]),
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
    choice = rng.randint(0, 6)
    if choice == 0:
        return rng.randint(-1000, 1000)
    elif choice == 1:
        return round(rng.uniform(-1000, 1000), 6)
    elif choice == 2:
        return rng.choice(["hello", "world", "test", "foo", "bar", "", "café", "🎉"])
    elif choice == 3:
        return rng.choice([True, False])
    elif choice == 4:
        return None
    elif choice == 5:
        return [rng.randint(1, 10), rng.choice(["a", "b"]), None]
    else:
        return {"nested_key": rng.randint(1, 100), "nested_str": rng.choice(["x", "y"])}


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
            elif f == "item_tags":
                n = rng.randint(0, 5)
                item[f] = [rng.choice(["a", "b", "c", "d", "e"]) for _ in range(n)]
            elif f == "item_vals":
                n = rng.randint(1, 5)
                item[f] = [random_numeric(rng, edge_weight) for _ in range(n)]
            elif "tag" in f or "name" in f:
                if rng.random() < edge_weight:
                    item[f] = rng.choice(["", "café", "🎉", "a\"b", "x\ny", None])
                else:
                    item[f] = rng.choice(["a", "b", "c", "d"])
            else:
                item[f] = random_scalar(rng, edge_weight)
        # Sparse items: randomly drop a field to exercise missing-field paths
        if rng.random() < 0.15 and len(item) > 1:
            drop_field = rng.choice(list(item.keys()))
            if drop_field != "_fuzz_distinctive_score":  # keep the sort key
                del item[drop_field]
        items.append(item)
    return items


# ---------------------------------------------------------------------------
# Operator generation
# ---------------------------------------------------------------------------


def gen_operator(rng: random.Random, name: str,
                 prev_item_outputs: list[str], prev_common_outputs: list[str],
                 allow_data_parallel: bool = True,
                 prev_op_names: list[str] | None = None) -> tuple[dict, list[str], list[str]]:
    """Generate a random operator config. Returns (config, new_item_outputs, new_common_outputs)."""
    op_types = [
        "filter_truncate", "filter_condition", "recall_static", "reorder_sort",
        "transform_by_lua", "transform_copy", "transform_dispatch",
        "transform_size", "transform_normalize", "observe_log",
        # New operator types (appended for seed reproducibility)
        "merge_dedup", "reorder_shuffle_by_salt", "filter_paginate",
        "transform_resource_lookup", "recall_resource",
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
        direction = rng.choice(["common_to_item", "item_to_common", "common_to_common", "item_to_item"])
        config["direction"] = direction
        if direction == "common_to_item":
            field = rng.choice(prev_common_outputs) if prev_common_outputs else "tag"
            common_in = [field]
            item_out = [field]
        elif direction == "item_to_common":
            field = rng.choice(prev_item_outputs) if prev_item_outputs else "item_score"
            item_in = [field]
            common_out = [field]
            # data_parallel requires empty common_output
            if "data_parallel" in config:
                del config["data_parallel"]
        elif direction == "common_to_common":
            src_field = rng.choice(prev_common_outputs) if prev_common_outputs else "tag"
            dst_field = src_field + "_copy"
            common_in = [src_field]
            common_out = [dst_field]
            # data_parallel requires empty common_output
            if "data_parallel" in config:
                del config["data_parallel"]
        else:  # item_to_item
            src_field = rng.choice(prev_item_outputs) if prev_item_outputs else "item_score"
            dst_field = src_field + "_copy"
            item_in = [src_field]
            item_out = [dst_field]

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

    elif op_type == "merge_dedup":
        config["strategy"] = "first"
        if prev_item_outputs:
            dedup_keys = rng.sample(prev_item_outputs, min(rng.randint(1, 2), len(prev_item_outputs)))
            item_in = dedup_keys
        else:
            item_in = ["item_id"]
        config.pop("data_parallel", None)

    elif op_type == "reorder_shuffle_by_salt":
        if prev_item_outputs:
            item_in = [rng.choice(prev_item_outputs)]
        else:
            item_in = ["item_id"]
        if prev_common_outputs:
            common_in = rng.sample(prev_common_outputs, min(rng.randint(1, 2), len(prev_common_outputs)))
        config.pop("data_parallel", None)

    elif op_type == "filter_paginate":
        # filter_paginate reads page (0-indexed) and size from common fields by position:
        # common_input[0] = page field, common_input[1] = size field
        config.pop("data_parallel", None)
        page_val = rng.randint(0, 3)
        size_val = rng.randint(1, 20)
        common_in = ["_fuzz_page", "_fuzz_size"]
        # These will be injected into request.common by gen_pipeline
        config["_fuzz_paginate_page"] = page_val
        config["_fuzz_paginate_size"] = size_val

    elif op_type == "transform_resource_lookup":
        # Needs a resource with static lookup table
        resource_name = f"_fuzz_lookup_{name}"
        n_entries = rng.randint(2, 6)
        lookup_table: dict[str, Any] = {}
        for j in range(n_entries):
            lookup_table[f"key_{j}"] = random_scalar(rng, edge_weight=0.1)
        config["resource_name"] = resource_name
        if prev_item_outputs:
            lookup_key = rng.choice(prev_item_outputs)
        else:
            lookup_key = "item_id"
        config["lookup_key"] = lookup_key
        output_field = f"_fuzz_looked_up_{name}"
        config["output_field"] = output_field
        if rng.random() < 0.5:
            config["default_value"] = random_scalar(rng, edge_weight=0.1)
        item_in = [lookup_key]
        item_out = [output_field]
        # Store resource info for gen_pipeline to inject into resource_config
        config["_fuzz_resource"] = {
            "name": resource_name,
            "value": lookup_table,
        }

    elif op_type == "recall_resource":
        # Needs a resource with static item list
        resource_name = f"_fuzz_recall_res_{name}"
        n_res_items = rng.randint(2, 15)
        res_fields = rng.sample(ITEM_FIELD_POOL, min(rng.randint(2, 4), len(ITEM_FIELD_POOL)))
        res_items = random_items(rng, res_fields, n_res_items)
        # Add distinctive score for stable ordering
        base = rng.uniform(1e6, 1e9)
        for idx, ri in enumerate(res_items):
            ri["_fuzz_distinctive_score"] = base + idx * 1000.0 + rng.uniform(0.1, 999.9)
        config["resource_name"] = resource_name
        item_out = res_fields + ["_fuzz_distinctive_score"]
        config.pop("data_parallel", None)
        # Store resource info for gen_pipeline to inject into resource_config
        config["_fuzz_resource"] = {
            "name": resource_name,
            "value": res_items,
        }

    config["$metadata"] = {
        "common_input": common_in,
        "common_output": common_out,
        "item_input": item_in,
        "item_output": item_out,
    }

    # Randomly add defaults for declared input fields (20% chance each)
    if rng.random() < 0.2 and item_in:
        defaults = {}
        for f in item_in:
            if rng.random() < 0.5:
                defaults[f] = random_scalar(rng)
        if defaults:
            config["item_defaults"] = defaults
    if rng.random() < 0.2 and common_in:
        defaults = {}
        for f in common_in:
            if rng.random() < 0.5:
                defaults[f] = random_scalar(rng)
        if defaults:
            config["common_defaults"] = defaults

    # Randomly add explicit sources (DAG edges) ~15% of the time
    if prev_op_names and len(prev_op_names) >= 2 and rng.random() < 0.15:
        config["sources"] = [rng.choice(prev_op_names)]

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

    # ~20% of rounds use request-provided items instead of recall_static
    use_request_items = rng.random() < 0.2

    operators: dict[str, Any] = {}
    pipeline: list[str] = []
    resource_configs: dict[str, Any] = {}

    if use_request_items:
        # Generate items directly in request.items
        req_fields = rng.sample(ITEM_FIELD_POOL, min(rng.randint(2, 5), len(ITEM_FIELD_POOL)))
        n_req_items = rng.randint(3, 50)
        req_items = random_items(rng, req_fields, n_req_items)
        for idx, item in enumerate(req_items):
            item["_fuzz_distinctive_score"] = idx * 1000.0 + rng.uniform(0.1, 999.9)
        item_outputs = req_fields + ["_fuzz_distinctive_score"]
        request_items: list[dict] = req_items
    else:
        # Start with recall_static — always include _fuzz_distinctive_score with unique values
        n_recall_items = rng.randint(3, 50)
        recall_fields = rng.sample(ITEM_FIELD_POOL, min(rng.randint(2, 5), len(ITEM_FIELD_POOL)))
        recall_items = random_items(rng, recall_fields, n_recall_items)
        for idx, item in enumerate(recall_items):
            item["_fuzz_distinctive_score"] = idx * 1000.0 + rng.uniform(0.1, 999.9)
        recall_fields_with_score = recall_fields + ["_fuzz_distinctive_score"]

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
        request_items = []

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
            rng, op_name, item_outputs, common_outputs,
            prev_op_names=list(pipeline),
        )

        # Handle filter_paginate: inject page/size fields into common
        if op_config.get("type_name") == "filter_paginate":
            page_val = op_config.pop("_fuzz_paginate_page", 0)
            size_val = op_config.pop("_fuzz_paginate_size", 10)
            common["_fuzz_page"] = page_val
            common["_fuzz_size"] = size_val
            if "_fuzz_page" not in common_outputs:
                common_outputs.append("_fuzz_page")
            if "_fuzz_size" not in common_outputs:
                common_outputs.append("_fuzz_size")

        # Collect resource configs from resource operators
        fuzz_resource = op_config.pop("_fuzz_resource", None)
        if fuzz_resource:
            resource_configs[fuzz_resource["name"]] = {
                "type": "static",
                "interval": 3600,
                "params": {"value": fuzz_resource["value"]},
            }

        # Randomly attach skip to some ops — but only if the operator has
        # at least one non-skip common_input field. Otherwise skip filtering
        # would empty CommonInput, causing operators that read CommonInput[0]
        # (dispatch, paginate) to panic on empty access.
        if use_skip and skip_field and rng.random() < 0.3:
            existing_common = op_config["$metadata"]["common_input"]
            non_skip_common = [f for f in existing_common if f != skip_field]
            if non_skip_common or not existing_common:
                op_config["skip"] = skip_field
                op_config["for_branch_control"] = True
                if skip_field not in op_config["$metadata"]["common_input"]:
                    op_config["$metadata"]["common_input"].append(skip_field)

        # ~15% chance add debug=true on this operator
        if rng.random() < 0.15:
            op_config["debug"] = True

        operators[op_name] = op_config
        pipeline.append(op_name)

    # Row-set mutators/consumers split the pipeline into sections.
    # Within a section, multiple recall ops can run in parallel, producing
    # non-deterministic item ordering. For each section that contains ≥2
    # recalls, insert a stabilizing sort just before the section's ending
    # boundary (the next row-set mutator/consumer).
    #
    # Boundary = ConsumesRowSet or MutatesRowSet marker (from Go operator
    # definitions in pine-go/operators/). OR implicit consumer: any op with
    # item_input or item_output (the DAG auto-injects _row_set_ read).
    # The explicit set must be updated when new operators are added.
    recall_types = ("recall_static", "recall_resource")
    # ConsumesRowSet and/or MutatesRowSet — from pine-go operator markers
    _row_set_boundary_types = frozenset({
        # ConsumesRowSet + MutatesRowSet
        "filter_truncate", "filter_paginate", "filter_condition",
        "reorder_sort", "reorder_shuffle_by_salt",
        "merge_dedup",
        # ConsumesRowSet only
        "transform_size",
        "transform_by_remote_pineapple",
    })

    def _is_row_set_boundary(op_config: dict) -> bool:
        tn = op_config.get("type_name", "")
        if tn in _row_set_boundary_types:
            return True
        meta = op_config.get("$metadata", {})
        if meta.get("item_input") or meta.get("item_output"):
            return True
        return False

    if "_fuzz_distinctive_score" in item_outputs:
        section_recalls = 0
        insert_positions: list[int] = []
        for i, name in enumerate(pipeline):
            op = operators.get(name, {})
            tn = op.get("type_name", "")
            is_recall = tn in recall_types or op.get("recall")
            if is_recall:
                section_recalls += 1
            elif _is_row_set_boundary(op):
                if section_recalls >= 2:
                    insert_positions.append(i)
                section_recalls = 0
        # Insert in reverse order to preserve indices
        for pos in reversed(insert_positions):
            stab_name = f"_stabilize_sort_{pos}"
            operators[stab_name] = {
                "type_name": "reorder_sort",
                "order": "asc",
                "$metadata": {
                    "common_input": [],
                    "common_output": [],
                    "item_input": ["_fuzz_distinctive_score"],
                    "item_output": [],
                },
            }
            pipeline.insert(pos, stab_name)

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
    # 50/50 storage_mode split. The prior 75% row / 25% column bias
    # was inherited from when pine-python and pine-cpp lacked RowFrame
    # impls — the row mode silently downgraded to column on those engines,
    # so 75% bias was a way to over-sample what was effectively column.
    # With both impls real on every engine,
    # equal weighting exercises each path equally and surfaces row-vs-
    # column drift faster.
    storage_mode = rng.choice(["row", "column"])

    config: dict[str, Any] = {
        "_PINEAPPLE_VERSION": "0.9.5",
        "pipeline_config": {
            "operators": operators,
            "pipeline_map": pipeline_map,
        },
        "pipeline_group": {"main": {"pipeline": group_pipeline}},
    }

    if storage_mode == "column":
        config["storage_mode"] = "column"

    # Inject resource_config if any operators need resources
    if resource_configs:
        config["resource_config"] = resource_configs

    # ~15% chance add _return_trace to common
    if rng.random() < 0.15:
        common["_return_trace"] = True

    items: list[dict] = request_items
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
        "_PINEAPPLE_VERSION": "0.9.0",
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


class CppEngine(Engine):
    """Pine-cpp backend. Invokes the same pineapple-run binary
    shape as GoEngine; built via CMake into pine-cpp/build/pineapple-run.
    """
    name = "cpp"

    def __init__(self, binary: str):
        self.binary = binary

    def run(self, config_file: str, request_file: str) -> tuple[int, str, str]:
        result = subprocess.run(
            [self.binary, "-config", config_file, "-request", request_file],
            capture_output=True, text=True, timeout=10,
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


def _strip_trace_timing(obj: Any) -> Any:
    """Strip timing-sensitive fields from trace output for comparison.
    Keeps trace structure (operator names, order) but removes exact durations.
    """
    if isinstance(obj, dict):
        return {k: _strip_trace_timing(v) for k, v in obj.items()
                if k not in ("duration_ns", "start_ns", "end_ns", "duration_us",
                             "start_us", "end_us", "elapsed_ns", "elapsed_us")}
    if isinstance(obj, list):
        return [_strip_trace_timing(item) for item in obj]
    return obj


def _sort_items(items: list) -> list:
    """Sort items by stable key for order-independent comparison."""
    def sort_key(item):
        if isinstance(item, dict):
            return json.dumps(item, sort_keys=True, ensure_ascii=False)
        return str(item)
    return sorted(items, key=sort_key)


def normalize_json(data: str, sort_items: bool = False, strip_trace: bool = False) -> str:
    """Parse and re-serialize JSON for comparison."""
    try:
        obj = json.loads(data)
    except json.JSONDecodeError:
        return data.strip()

    obj = _normalize_value(obj)
    if strip_trace and isinstance(obj, dict) and "trace" in obj:
        obj["trace"] = _strip_trace_timing(obj["trace"])
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
    parser.add_argument("--engines", type=str, default="go,python,java,cpp",
                        help="Comma-separated engines to compare (go,python,java,cpp)")
    parser.add_argument("--go-bin", type=str, default="")
    parser.add_argument("--cpp-bin", type=str, default="",
                        help="Path to pine-cpp pineapple-run binary")
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
        elif name == "cpp":
            cpp_bin = args.cpp_bin or str(REPO_ROOT / "pine-cpp" / "build" / "pineapple-run")
            if not Path(cpp_bin).exists():
                print(f"  WARNING: pine-cpp binary not found at {cpp_bin}, skipping")
                print(f"           build first: cmake -S pine-cpp -B pine-cpp/build && cmake --build pine-cpp/build")
                continue
            engines.append(CppEngine(cpp_bin))
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
             "data_parallel": 0, "strict_order": 0, "error_path": 0,
             # Row mode count + per-storage-mode pass/fail buckets so
             # the summary surfaces stratification across the two impls.
             "row": 0, "pass_row": 0, "pass_column": 0, "fail_row": 0, "fail_column": 0,
             # New dimensions
             "request_items": 0, "resources": 0, "debug": 0, "return_trace": 0,
             "sources": 0, "defaults": 0, "sparse_items": 0,
             "merge_dedup": 0, "shuffle_by_salt": 0, "paginate": 0,
             "resource_lookup": 0, "recall_resource": 0}
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
                    else:
                        stats["row"] += 1
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
                    # Track new dimensions
                    if config.get("resource_config"):
                        stats["resources"] += 1
                    if common.get("_return_trace"):
                        stats["return_trace"] += 1
                    if items:  # request-provided items
                        stats["request_items"] += 1
                    op_types_in_pipeline = {op.get("type_name") for op in ops.values()}
                    if "merge_dedup" in op_types_in_pipeline:
                        stats["merge_dedup"] += 1
                    if "reorder_shuffle_by_salt" in op_types_in_pipeline:
                        stats["shuffle_by_salt"] += 1
                    if "filter_paginate" in op_types_in_pipeline:
                        stats["paginate"] += 1
                    if "transform_resource_lookup" in op_types_in_pipeline:
                        stats["resource_lookup"] += 1
                    if "recall_resource" in op_types_in_pipeline:
                        stats["recall_resource"] += 1
                    if any(op.get("debug") for op in ops.values()):
                        stats["debug"] += 1
                    if any(op.get("sources") for op in ops.values()):
                        stats["sources"] += 1
                    if any(op.get("item_defaults") or op.get("common_defaults") for op in ops.values()):
                        stats["defaults"] += 1

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
                    has_trace = common.get("_return_trace") is True
                    ref_norm = normalize_json(ref_out, sort_items=not strict_order, strip_trace=has_trace)
                    e_norm = normalize_json(e_out, sort_items=not strict_order, strip_trace=has_trace)
                    if ref_norm != e_norm:
                        all_match = False
                        divergent_pair = (ref_name, engine.name)
                        break

                if all_match:
                    passed += 1
                    # Stratify pass/fail by storage_mode so the summary
                    # exposes row-vs-column divergence rates.
                    if config.get("storage_mode") == "column":
                        stats["pass_column"] += 1
                    else:
                        stats["pass_row"] += 1
                    if args.verbose:
                        sys.stderr.write("\n")
                        print(f"  [{i+1}] match")

                    # TEST-1: ~20% of passed rounds, re-run with the opposite
                    # storage_mode and compare Go outputs (row vs column).
                    if not is_error_path and ref_rc == 0 and rng.random() < 0.2:
                        orig_mode = config.get("storage_mode", "row")
                        alt_mode = "column" if orig_mode == "row" else "row"
                        alt_config = dict(config)
                        if alt_mode == "column":
                            alt_config["storage_mode"] = "column"
                        else:
                            alt_config.pop("storage_mode", None)
                        alt_config_file = os.path.join(tmpdir, "alt_config.json")
                        with open(alt_config_file, "w") as f:
                            json.dump(alt_config, f, ensure_ascii=False)
                        go_engine = engines[0]
                        try:
                            alt_rc, alt_out, _ = go_engine.run(alt_config_file, request_file)
                            if alt_rc == ref_rc:
                                has_trace = common.get("_return_trace") is True
                                ref_norm2 = normalize_json(ref_out, sort_items=not strict_order, strip_trace=has_trace)
                                alt_norm2 = normalize_json(alt_out, sort_items=not strict_order, strip_trace=has_trace)
                                if ref_norm2 != alt_norm2:
                                    stats["cross_storage_diverge"] = stats.get("cross_storage_diverge", 0) + 1
                                    d = save_divergence(save_dir, i, config, request,
                                                        {f"go_{orig_mode}": (ref_rc, ref_out),
                                                         f"go_{alt_mode}": (alt_rc, alt_out)},
                                                        (f"go_{orig_mode}", f"go_{alt_mode}"),
                                                        kind="cross_storage")
                                    print(f"  [{i+1}] CROSS-STORAGE divergence ({orig_mode} vs {alt_mode}): → {d}")
                                else:
                                    stats["cross_storage_pass"] = stats.get("cross_storage_pass", 0) + 1
                        except (subprocess.TimeoutExpired, Exception):
                            pass  # skip cross-storage check on timeout
                else:
                    failed += 1
                    if config.get("storage_mode") == "column":
                        stats["fail_column"] += 1
                    else:
                        stats["fail_row"] += 1
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
    print(f"            row={stats['row']} column={stats['column']} skip={stats['skip']} data_parallel={stats['data_parallel']}")
    print(f"            strict_order={stats['strict_order']} error_path={stats['error_path']}")
    print(f"  New dims: request_items={stats['request_items']} resources={stats['resources']} debug={stats['debug']} return_trace={stats['return_trace']}")
    print(f"            sources={stats['sources']} defaults={stats['defaults']}")
    print(f"            merge_dedup={stats['merge_dedup']} shuffle_by_salt={stats['shuffle_by_salt']} paginate={stats['paginate']}")
    print(f"            resource_lookup={stats['resource_lookup']} recall_resource={stats['recall_resource']}")
    # Stratified summary:
    print(f"  Storage stratified pass/fail:  row={stats['pass_row']}/{stats['fail_row']}  column={stats['pass_column']}/{stats['fail_column']}")
    if failed > 0:
        print(f"  Divergences: {save_dir}")
    print(f"{'='*60}")

    sys.exit(1 if failed > 0 else 0)


if __name__ == "__main__":
    main()
