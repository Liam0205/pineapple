"""Compile a Flow into the Pine JSON config format.

Steps:
1. Recursively traverse Flow/SubFlow tree, collecting all ops in global order
2. Auto-generate unique operator names: {type_name}_{hash6}
3. Validate field coverage and write-without-read
4. Dead-code detection (if output contract declared)
5. Emit JSON with pipeline_config, pipeline_group, flow_contract
"""
from __future__ import annotations

import json
from datetime import datetime, timezone
from typing import Any

from apple._version import __version__
from apple.base import OpCall
from apple.validator import (
    ValidationError,
    detect_dead_code,
    validate_data_parallel,
    validate_field_coverage,
    validate_no_underscore_output,
    validate_param_metadata_consistency,
    validate_write_without_read,
)


def compile_flow(flow: Any) -> dict[str, Any]:
    """Compile a Flow object into a Pine JSON config dict."""
    # 1. Check unclosed control blocks (recursive)
    _check_unclosed_control(flow, flow._name)

    # 2. Recursive structure traversal
    all_ops: list[OpCall] = []
    structures: dict[str, list[tuple[str, Any]]] = {}
    visited: set[int] = set()
    _traverse(flow, "", all_ops, structures, visited)

    # 3. Generate unique names
    named_ops: list[tuple[str, OpCall]] = []
    name_counts: dict[str, int] = {}
    for op in all_ops:
        name = op.unique_name()
        if op.name:
            if name in name_counts:
                raise ValidationError(
                    f"duplicate explicit operator name: {name!r}"
                )
        if name in name_counts:
            name_counts[name] += 1
            name = f"{name}_{name_counts[name]}"
        else:
            name_counts[name] = 0
        named_ops.append((name, op))

    # 4. Validate
    validate_no_underscore_output(
        named_ops,
        flow._common_output,
        flow._item_output,
    )
    validate_field_coverage(
        named_ops,
        flow._common_input or [],
        flow._item_input or [],
    )
    validate_write_without_read(
        named_ops,
        flow._common_input or [],
        flow._item_input or [],
    )
    validate_data_parallel(named_ops)
    validate_param_metadata_consistency(named_ops)
    dead = detect_dead_code(
        named_ops,
        flow._common_output,
        flow._item_output,
    )
    if dead:
        raise ValidationError(
            f"dead operators detected (output not consumed): {dead}"
        )

    # 5. Build operators dict
    operators: dict[str, Any] = {}
    for name, op in named_ops:
        entry: dict[str, Any] = {
            "type_name": op.type_name,
            "$metadata": {
                "common_input": op.common_input,
                "common_output": op.common_output,
                "item_input": op.item_input,
                "item_output": op.item_output,
            },
        }
        if op.code_info:
            entry["$code_info"] = op.code_info
        if op.recall:
            entry["recall"] = True
        if op.sources:
            entry["sources"] = list(op.sources)
        if op.skip:
            entry["skip"] = op.skip
        if op.for_branch_control:
            entry["for_branch_control"] = True
        if op.row_dependency:
            entry["row_dependency"] = True
        if op.item_defaults:
            entry["item_defaults"] = op.item_defaults
        if op.common_defaults:
            entry["common_defaults"] = op.common_defaults
        if op.debug:
            entry["debug"] = True
        if op.data_parallel > 1:
            entry["data_parallel"] = op.data_parallel
        for k, v in op.params.items():
            entry[k] = v
        operators[name] = entry

    # 6. Build pipeline_map (SubFlow paths only)
    pipeline_map: dict[str, Any] = {}
    for path, entries in structures.items():
        if path == "":
            continue
        pipeline_map[path] = {
            "pipeline": [_resolve_entry(e, named_ops) for e in entries]
        }

    # 7. Build pipeline_group (top-level entries)
    top_entries = structures.get("", [])
    pipeline_group = {
        "main": {
            "pipeline": [_resolve_entry(e, named_ops) for e in top_entries]
        }
    }

    # 8. Build flow_contract
    flow_contract: dict[str, Any] = {
        "common_input": flow._common_input or [],
        "item_input": flow._item_input or [],
    }
    if flow._common_output is not None:
        flow_contract["common_output"] = flow._common_output
    else:
        flow_contract["common_output"] = []
    if flow._item_output is not None:
        flow_contract["item_output"] = flow._item_output
    else:
        flow_contract["item_output"] = []

    # 9. Validate resource references
    declared_resources: set[str] = set()
    if hasattr(flow, '_resources'):
        declared_resources = {r.name for r in flow._resources}
    _validate_resource_refs(named_ops, declared_resources)

    # 10. Build result
    result: dict[str, Any] = {
        "_PINEAPPLE_VERSION": __version__,
        "_PINEAPPLE_CREATE_TIME": datetime.now(timezone.utc).strftime(
            "%Y-%m-%dT%H:%M:%SZ"
        ),
        "pipeline_config": {
            "operators": operators,
            "pipeline_map": pipeline_map,
        },
        "pipeline_group": pipeline_group,
        "flow_contract": flow_contract,
    }

    if hasattr(flow, '_storage_mode') and flow._storage_mode:
        result["storage_mode"] = flow._storage_mode
    if hasattr(flow, '_log_prefix') and flow._log_prefix:
        result["log_prefix"] = flow._log_prefix
    if hasattr(flow, '_debug') and flow._debug:
        result["debug"] = True
    if hasattr(flow, '_resources') and flow._resources:
        result["resource_config"] = {
            r.name: {
                "type": r.resource_type,
                "interval": r.interval,
                "params": r.params,
            }
            for r in flow._resources
        }

    return result


def _check_unclosed_control(node: Any, display_name: str, visited: set[int] | None = None) -> None:
    """Recursively check for unclosed control blocks in all nodes."""
    if visited is None:
        visited = set()
    obj_id = id(node)
    if obj_id in visited:
        return
    visited.add(obj_id)
    if node._ctrl_stack:
        raise ValidationError(
            f"unclosed if_ block in {display_name!r}: "
            f"missing end_if_() for {len(node._ctrl_stack)} block(s)"
        )
    for sf in node._sub_flows:
        _check_unclosed_control(sf, sf._name, visited)


def _traverse(
    node: Any,
    path: str,
    all_ops: list[OpCall],
    structures: dict[str, list[tuple[str, Any]]],
    visited: set[int],
) -> None:
    """Recursively traverse _child_order, collecting ops and structure."""
    obj_id = id(node)
    if obj_id in visited:
        raise ValidationError(
            f"SubFlow cycle or reuse detected: {node._name!r}"
        )
    visited.add(obj_id)

    entries: list[tuple[str, Any]] = []
    for kind, idx in node._child_order:
        if kind == "op":
            op = node._ops[idx]
            op.subflow_path = path
            global_idx = len(all_ops)
            all_ops.append(op)
            entries.append(("op", global_idx))
        else:
            sf = node._sub_flows[idx]
            sf_path = f"{path}/{sf._name}" if path else sf._name
            if sf_path in structures:
                raise ValidationError(
                    f"duplicate SubFlow path: {sf_path!r}"
                )
            entries.append(("sf", sf_path))
            _traverse(sf, sf_path, all_ops, structures, visited)

    structures[path] = entries


def _resolve_entry(
    entry: tuple[str, Any],
    named_ops: list[tuple[str, OpCall]],
) -> str:
    """Resolve a structure entry to its final pipeline string."""
    kind, val = entry
    if kind == "op":
        return named_ops[val][0]
    return val


def _validate_resource_refs(
    named_ops: list[tuple[str, OpCall]],
    declared_resources: set[str],
) -> None:
    """Validate that all resource_name params reference declared resources."""
    for op_name, op in named_ops:
        res_name = op.params.get("resource_name")
        if res_name is not None and res_name not in declared_resources:
            raise ValidationError(
                f"operator {op_name!r} references resource_name={res_name!r} "
                f"but no such resource was declared via flow.resource(). "
                f"Declared resources: {sorted(declared_resources) or '(none)'}"
            )


def compile_to_json(flow: Any, indent: int = 2) -> str:
    """Compile a Flow and return the JSON string."""
    return json.dumps(compile_flow(flow), indent=indent, ensure_ascii=False)
