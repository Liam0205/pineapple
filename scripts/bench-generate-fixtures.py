#!/usr/bin/env python3
"""生成跨引擎 benchmark fixture 文件。

将三个层级（small/medium/large）的管道配置和请求分别写入 fixtures/benchmarks/。
每个 fixture 包含 config（管道配置，可直接用于 server 启动）和 request（HTTP 请求体）。
"""
from __future__ import annotations

import json
import os
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
BENCH_DIR = REPO_ROOT / "fixtures" / "benchmarks"

_version_file = REPO_ROOT / "pine-go" / "version.go"
_match = __import__("re").search(r'const Version = "([^"]+)"', _version_file.read_text())
VERSION = _match.group(1) if _match else "0.0.0"


def make_items(n: int, *, offset: int = 0) -> list[dict]:
    """生成 N 个测试 item，item_id 从 offset 开始编号。"""
    items = []
    for i in range(n):
        items.append({
            "item_id": f"item_{offset + i}",
            "item_score": float(n - i),
            "item_status": "offline" if i % 10 == 0 else "online",
            "item_category": f"cat_{i % 5}",
            "item_price": float(100 + i),
        })
    return items


# ─── Small pipeline: recall → filter → sort (3 ops) ─────────────────────────

def small_config(num_items: int) -> dict:
    """小管道：recall_static → filter_truncate → reorder_sort"""
    return {
        "_PINEAPPLE_VERSION": VERSION,
        "pipeline_config": {
            "operators": {
                "recall": {
                    "type_name": "recall_static",
                    "recall": True,
                    "items": make_items(num_items),
                    "$metadata": {
                        "item_output": ["item_id", "item_score", "item_status", "item_category", "item_price"],
                    },
                },
                "filter": {
                    "type_name": "filter_truncate",
                    "top_n": num_items,  # 保留全部
                    "$metadata": {
                        "item_input": ["item_id"],
                        "item_output": ["item_id", "item_score"],
                    },
                },
                "sort": {
                    "type_name": "reorder_sort",
                    "order": "desc",
                    "$metadata": {
                        "item_input": ["item_score"],
                    },
                },
            },
            "pipeline_map": {
                "stage1": {"pipeline": ["recall", "filter", "sort"]},
            },
        },
        "pipeline_group": {
            "main": {"pipeline": ["stage1"]},
        },
        "flow_contract": {},
    }


def small_request() -> dict:
    """小管道请求：recall_static 内嵌 items，所以请求体为空。"""
    return {"common": {}, "items": []}


# ─── Medium pipeline: recall_a + recall_b → merge → dispatch → normalize → sort (6 ops) ──

def medium_config(num_items: int) -> dict:
    """中管道：两路 recall → merge_dedup → transform_dispatch → transform_normalize → reorder_sort"""
    half = num_items // 2
    return {
        "_PINEAPPLE_VERSION": VERSION,
        "pipeline_config": {
            "operators": {
                "recall_a": {
                    "type_name": "recall_static",
                    "recall": True,
                    "items": make_items(half, offset=0),
                    "$metadata": {
                        "item_output": ["item_id", "item_score", "item_status", "item_category", "item_price"],
                    },
                },
                "recall_b": {
                    "type_name": "recall_static",
                    "recall": True,
                    "items": make_items(num_items - half, offset=half),
                    "$metadata": {
                        "item_output": ["item_id", "item_score", "item_status", "item_category", "item_price"],
                    },
                },
                "merge": {
                    "type_name": "merge_dedup",
                    "sources": ["recall_a", "recall_b"],
                    "$metadata": {
                        "item_input": ["item_id", "_source"],
                        "item_output": ["item_id", "item_score", "item_status", "item_category", "item_price"],
                    },
                },
                "dispatch": {
                    "type_name": "transform_dispatch",
                    "$metadata": {
                        "common_input": ["scene"],
                        "item_input": ["item_status"],
                        "item_output": ["item_scene"],
                    },
                },
                "normalize": {
                    "type_name": "transform_normalize",
                    "$metadata": {
                        "item_input": ["item_score"],
                        "item_output": ["item_score_norm"],
                    },
                },
                "sort": {
                    "type_name": "reorder_sort",
                    "order": "desc",
                    "$metadata": {
                        "item_input": ["item_score", "item_score_norm"],
                    },
                },
            },
            "pipeline_map": {
                "stage1": {"pipeline": ["recall_a", "recall_b", "merge", "dispatch", "normalize", "sort"]},
            },
        },
        "pipeline_group": {
            "main": {"pipeline": ["stage1"]},
        },
        "flow_contract": {"common_input": ["scene"]},
    }


def medium_request() -> dict:
    """中管道请求：需要 common.scene"""
    return {"common": {"scene": "bench"}, "items": []}


# ─── Large pipeline: 10 ops with Lua ─────────────────────────────────────────

LUA_DISCOUNT_SCRIPT = """\
function apply_discount()
  return item_price * 0.9
end
"""


def large_config(num_items: int) -> dict:
    """大管道：recall_a + recall_b → merge → copy → dispatch → lua_transform → normalize → filter → sort → truncate"""
    half = num_items // 2
    items_a = make_items(half, offset=0)
    items_b = make_items(num_items - half, offset=half)

    return {
        "_PINEAPPLE_VERSION": VERSION,
        "pipeline_config": {
            "operators": {
                "recall_a": {
                    "type_name": "recall_static",
                    "recall": True,
                    "items": items_a,
                    "$metadata": {
                        "item_output": ["item_id", "item_score", "item_status", "item_category", "item_price"],
                    },
                },
                "recall_b": {
                    "type_name": "recall_static",
                    "recall": True,
                    "items": items_b,
                    "$metadata": {
                        "item_output": ["item_id", "item_score", "item_status", "item_category", "item_price"],
                    },
                },
                "merge": {
                    "type_name": "merge_dedup",
                    "sources": ["recall_a", "recall_b"],
                    "$metadata": {
                        "item_input": ["item_id", "_source"],
                        "item_output": ["item_id", "item_score", "item_status", "item_category", "item_price"],
                    },
                },
                "copy": {
                    "type_name": "transform_copy",
                    "direction": "item_to_item",
                    "$metadata": {
                        "item_input": ["item_score"],
                        "item_output": ["item_score_copy"],
                    },
                },
                "dispatch": {
                    "type_name": "transform_dispatch",
                    "$metadata": {
                        "common_input": ["scene"],
                        "item_input": ["item_status"],
                        "item_output": ["item_scene"],
                    },
                },
                "lua_transform": {
                    "type_name": "transform_by_lua",
                    "lua_script": LUA_DISCOUNT_SCRIPT,
                    "function_for_item": "apply_discount",
                    "function_for_common": "",
                    "$metadata": {
                        "item_input": ["item_price"],
                        "item_output": ["item_final_price"],
                    },
                },
                "normalize": {
                    "type_name": "transform_normalize",
                    "$metadata": {
                        "item_input": ["item_score"],
                        "item_output": ["item_score_norm"],
                    },
                },
                "filter": {
                    "type_name": "filter_condition",
                    "value": "offline",
                    "$metadata": {
                        "item_input": ["item_status"],
                        "item_output": ["item_status", "item_score"],
                    },
                },
                "sort": {
                    "type_name": "reorder_sort",
                    "order": "desc",
                    "$metadata": {
                        "item_input": ["item_score", "item_score_norm"],
                    },
                },
                "truncate": {
                    "type_name": "filter_truncate",
                    "top_n": max(num_items // 2, 10),
                    "$metadata": {
                        "item_input": ["item_id"],
                        "item_output": ["item_id", "item_score"],
                    },
                },
            },
            "pipeline_map": {
                "stage1": {"pipeline": [
                    "recall_a", "recall_b", "merge", "copy", "dispatch",
                    "lua_transform", "normalize", "filter", "sort", "truncate",
                ]},
            },
        },
        "pipeline_group": {
            "main": {"pipeline": ["stage1"]},
        },
        "flow_contract": {"common_input": ["scene"]},
    }


def large_request() -> dict:
    """大管道请求：需要 common.scene"""
    return {"common": {"scene": "bench"}, "items": []}


# ─── 生成所有 fixtures ────────────────────────────────────────────────────────

FIXTURES = [
    # (名称, 配置生成函数, 请求生成函数, item 数量列表)
    ("small", small_config, small_request, [10, 50, 100]),
    ("medium", medium_config, medium_request, [100, 500, 1000]),
    ("large", large_config, large_request, [100, 500, 1000, 5000]),
]


def write_fixture(name: str, config: dict, request: dict, output_dir: Path):
    """将 config 和 request 分别写入独立文件。"""
    output_dir.mkdir(parents=True, exist_ok=True)

    config_path = output_dir / f"{name}_config.json"
    request_path = output_dir / f"{name}_request.json"

    with open(config_path, "w", encoding="utf-8") as f:
        json.dump(config, f, indent=2, ensure_ascii=False)
        f.write("\n")

    with open(request_path, "w", encoding="utf-8") as f:
        json.dump(request, f, indent=2, ensure_ascii=False)
        f.write("\n")


def main():
    BENCH_DIR.mkdir(parents=True, exist_ok=True)
    generated = []

    for tier, config_fn, request_fn, item_counts in FIXTURES:
        # 用零填充保证文件名自然排序
        max_width = len(str(max(item_counts)))
        for n in item_counts:
            name = f"{tier}_{str(n).zfill(max_width)}"
            config = config_fn(n)
            request = request_fn()
            write_fixture(name, config, request, BENCH_DIR)
            generated.append(name)
            print(f"  generated: {name}")

    print(f"\n共生成 {len(generated)} 个 benchmark fixture 于 {BENCH_DIR.relative_to(REPO_ROOT)}/")
    return 0


if __name__ == "__main__":
    sys.exit(main())
