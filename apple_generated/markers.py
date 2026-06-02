# auto-generated from pine operator schema — DO NOT EDIT
"""Row-set marker bools per operator, probed from Go factories at codegen time.

The Go side declares row-set semantics via marker interfaces
(AdditiveWritesRowSet, ConsumesRowSet, MutatesRowSet). This file mirrors
those flags so Apple OpCall and the validator can judge row-set behavior
directly instead of inferring from operator name prefix.
"""
from __future__ import annotations

OPERATOR_MARKERS: dict[str, dict[str, bool]] = {
    "filter_condition": {
        "additive_writes_row_set": False,
        "consumes_row_set": True,
        "mutates_row_set": True,
    },
    "filter_paginate": {
        "additive_writes_row_set": False,
        "consumes_row_set": True,
        "mutates_row_set": True,
    },
    "filter_truncate": {
        "additive_writes_row_set": False,
        "consumes_row_set": True,
        "mutates_row_set": True,
    },
    "merge_dedup": {
        "additive_writes_row_set": False,
        "consumes_row_set": True,
        "mutates_row_set": True,
    },
    "observe_log": {
        "additive_writes_row_set": False,
        "consumes_row_set": False,
        "mutates_row_set": False,
    },
    "recall_resource": {
        "additive_writes_row_set": True,
        "consumes_row_set": False,
        "mutates_row_set": False,
    },
    "recall_static": {
        "additive_writes_row_set": True,
        "consumes_row_set": False,
        "mutates_row_set": False,
    },
    "reorder_shuffle_by_salt": {
        "additive_writes_row_set": False,
        "consumes_row_set": True,
        "mutates_row_set": True,
    },
    "reorder_sort": {
        "additive_writes_row_set": False,
        "consumes_row_set": True,
        "mutates_row_set": True,
    },
    "transform_bench_cpu": {
        "additive_writes_row_set": False,
        "consumes_row_set": False,
        "mutates_row_set": False,
    },
    "transform_bench_sleep": {
        "additive_writes_row_set": False,
        "consumes_row_set": False,
        "mutates_row_set": False,
    },
    "transform_by_lua": {
        "additive_writes_row_set": False,
        "consumes_row_set": False,
        "mutates_row_set": False,
    },
    "transform_by_remote_pineapple": {
        "additive_writes_row_set": False,
        "consumes_row_set": True,
        "mutates_row_set": False,
    },
    "transform_copy": {
        "additive_writes_row_set": False,
        "consumes_row_set": False,
        "mutates_row_set": False,
    },
    "transform_dispatch": {
        "additive_writes_row_set": False,
        "consumes_row_set": False,
        "mutates_row_set": False,
    },
    "transform_normalize": {
        "additive_writes_row_set": False,
        "consumes_row_set": False,
        "mutates_row_set": False,
    },
    "transform_redis_get": {
        "additive_writes_row_set": False,
        "consumes_row_set": False,
        "mutates_row_set": False,
    },
    "transform_redis_set": {
        "additive_writes_row_set": False,
        "consumes_row_set": False,
        "mutates_row_set": False,
    },
    "transform_resource_lookup": {
        "additive_writes_row_set": False,
        "consumes_row_set": False,
        "mutates_row_set": False,
    },
    "transform_size": {
        "additive_writes_row_set": False,
        "consumes_row_set": True,
        "mutates_row_set": False,
    },
}


def get_markers(type_name: str) -> dict[str, bool]:
    """Return the marker dict for type_name, or all-False defaults if unknown.

    Unknown operators (e.g., custom ops registered after codegen) are
    treated as having no row-set semantics; the Go side remains authoritative.
    """
    return OPERATOR_MARKERS.get(type_name, {
        "additive_writes_row_set": False,
        "consumes_row_set": False,
        "mutates_row_set": False,
    })
